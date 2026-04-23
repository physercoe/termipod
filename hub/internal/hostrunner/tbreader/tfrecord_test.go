package tbreader

import (
	"bytes"
	"testing"
)

func TestTFRecordRoundTrip(t *testing.T) {
	payloads := [][]byte{
		[]byte("hello"),
		{}, // empty payload is valid
		bytes.Repeat([]byte{0xab}, 1024),
		[]byte("trailing"),
	}
	var buf bytes.Buffer
	for _, p := range payloads {
		if err := WriteRecord(&buf, p); err != nil {
			t.Fatalf("WriteRecord: %v", err)
		}
	}

	var got [][]byte
	for payload, err := range Records(&buf) {
		if err != nil {
			t.Fatalf("Records yielded err: %v", err)
		}
		// Records reuses buffers internally — dup before appending.
		cp := make([]byte, len(payload))
		copy(cp, payload)
		got = append(got, cp)
	}
	if len(got) != len(payloads) {
		t.Fatalf("got %d records, want %d", len(got), len(payloads))
	}
	for i, p := range payloads {
		if !bytes.Equal(got[i], p) {
			t.Errorf("record %d: got %x, want %x", i, got[i], p)
		}
	}
}

func TestTFRecord_TruncatedTailStopsCleanly(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteRecord(&buf, []byte("first")); err != nil {
		t.Fatalf("WriteRecord: %v", err)
	}
	// Simulate a live writer whose last record was only partially flushed:
	// append a valid length header but NO payload bytes.
	partial := buf.Bytes()
	// Append 12 bytes = length (8) + length_crc (4). Length = 100, but
	// we won't write the 100 payload bytes or the 4-byte payload CRC.
	var hdr [12]byte
	hdr[0] = 100 // little-endian uint64: just set low byte
	partial = append(partial, hdr[:]...)

	var yielded int
	for _, err := range Records(bytes.NewReader(partial)) {
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		yielded++
	}
	if yielded != 1 {
		t.Errorf("yielded=%d, want 1 (partial tail should stop cleanly)", yielded)
	}
}

func TestMaskedCRC_Deterministic(t *testing.T) {
	// Not checking a specific reference value — just that maskedCRC is
	// a pure function of input and that two identical inputs produce
	// the same output (catches a regression where we'd accidentally
	// mix in state from a shared hasher).
	a := maskedCRC([]byte("hello"))
	b := maskedCRC([]byte("hello"))
	if a != b {
		t.Errorf("maskedCRC nondeterministic: %x vs %x", a, b)
	}
	c := maskedCRC([]byte("world"))
	if a == c {
		t.Errorf("maskedCRC collided on distinct inputs")
	}
}
