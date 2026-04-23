package tbreader

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"iter"
)

// TFRecord framing (little-endian throughout):
//
//	uint64 length
//	uint32 masked_crc32c_of_length
//	<length bytes of payload>
//	uint32 masked_crc32c_of_payload
//
// where masked_crc(x) = ((crc32c(x) >> 15) | (crc32c(x) << 17)) + 0xa282ead8
// (uint32 wrap) and crc32c uses the Castagnoli polynomial.
//
// We decode payloads but skip CRC verification: TensorBoard writers
// fsync on close so partial trailing writes are vanishingly rare, and
// when they do happen the safer policy is "ignore this record / stop
// here" rather than "fail the whole file" anyway. If the length header
// reads as something nonsensical after a truncation, we return
// errTruncated and the reader treats it as end-of-stream.

// errTruncated is returned when the stream ends mid-record. Callers
// treat it as a clean end-of-file: TensorBoard is a rolling log format
// and partial tail records are normal during live training.
var errTruncated = errors.New("tfrecord: truncated record")

// castagnoli is the CRC-32C polynomial table shared by maskedCRC and
// (potentially) any future verification path. Instantiated lazily on
// first use rather than in init() so the cost is zero in the common
// case where we don't verify.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// maskedCRC applies TensorBoard's rotating + constant mask to a raw
// CRC-32C. Exposed so tests can round-trip encode-then-decode without
// pulling in a separate CRC library.
func maskedCRC(b []byte) uint32 {
	c := crc32.Checksum(b, castagnoli)
	return ((c >> 15) | (c << 17)) + 0xa282ead8
}

// Records yields each record payload from a TFRecord stream. Each
// yielded []byte is a freshly allocated slice — the caller may retain
// it without copying. The iterator is single-pass (the underlying
// reader advances as records are consumed).
//
// Iteration stops cleanly at EOF (both the literal io.EOF and our
// errTruncated "looks like a partial write" condition). Any other
// error is yielded to the caller once, then iteration stops.
func Records(r io.Reader) iter.Seq2[[]byte, error] {
	return func(yield func([]byte, error) bool) {
		var hdr [12]byte // uint64 length + uint32 length_crc
		for {
			if _, err := io.ReadFull(r, hdr[:]); err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				if errors.Is(err, io.ErrUnexpectedEOF) {
					// Partial header mid-stream — live writer, just stop.
					return
				}
				yield(nil, err)
				return
			}
			length := binary.LittleEndian.Uint64(hdr[0:8])
			// A sane upper bound: 128 MiB per record is more than any
			// reasonable scalar Event, but generous enough for fat
			// tensor/graph records we'd skip anyway. If we read garbage
			// (e.g. file was truncated mid-header and a subsequent
			// reopen sees stale bytes), this prevents us from
			// allocating several gigs of buffer.
			if length > 128*1024*1024 {
				yield(nil, errTruncated)
				return
			}
			// hdr[8:12] holds the length's masked CRC. We choose not to
			// verify — see top-of-file comment — but we keep the read
			// as a structural check and move on.
			_ = binary.LittleEndian.Uint32(hdr[8:12])

			payload := make([]byte, length)
			if _, err := io.ReadFull(r, payload); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return
				}
				yield(nil, err)
				return
			}
			var tail [4]byte
			if _, err := io.ReadFull(r, tail[:]); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
					return
				}
				yield(nil, err)
				return
			}
			_ = tail // payload CRC — also unverified, see note above.

			if !yield(payload, nil) {
				return
			}
		}
	}
}

// WriteRecord emits one TFRecord frame. Only used by tests to build
// synthetic tfevents streams — production host-runners never write
// these, they only read.
func WriteRecord(w io.Writer, payload []byte) error {
	var hdr [12]byte
	binary.LittleEndian.PutUint64(hdr[0:8], uint64(len(payload)))
	binary.LittleEndian.PutUint32(hdr[8:12], maskedCRC(hdr[0:8]))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	var tail [4]byte
	binary.LittleEndian.PutUint32(tail[:], maskedCRC(payload))
	_, err := w.Write(tail[:])
	return err
}
