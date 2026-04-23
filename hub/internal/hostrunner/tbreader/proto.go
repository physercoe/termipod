package tbreader

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Hand-rolled protobuf decoder for the narrow slice of tensorflow.Event
// we care about. Only the fields the sparkline digest needs are
// decoded; everything else is skipped by wire-type.
//
// Wire types (protobuf spec):
//
//	0 = varint        (int32/64, uint32/64, bool, enum)
//	1 = fixed64       (sfixed64, fixed64, double)
//	2 = length-delim  (string, bytes, embedded messages, packed repeated)
//	5 = fixed32       (sfixed32, fixed32, float)
//
// tensorflow.Event (subset):
//
//	double wall_time   = 1;   // wire type 1
//	int64  step        = 2;   // wire type 0
//	Summary summary    = 5;   // wire type 2
//
// Other oneof siblings (file_version tag 3, graph_def tag 4, ...) are
// valid inputs — we simply skip them.
//
// Summary (subset):
//
//	repeated Value value = 1; // wire type 2
//
// Summary.Value (subset):
//
//	string tag               = 1;  // wire type 2
//	float  simple_value      = 2;  // wire type 5
//	TensorProto tensor       = 8;  // wire type 2
//
// TensorProto (subset):
//
//	DataType dtype            = 2;   // wire type 0, DT_FLOAT == 1
//	repeated float float_val  = 4;   // wire type 2 (packed) or 5 (single)
//
// The decoder is intentionally forgiving — unknown fields, wrong types
// for fields we recognise, truncated messages — all are skipped rather
// than raising. Raising would turn one bad Event into a whole-file
// failure on a live-writing log.

const dtFloat = 1 // tensorflow.DataType.DT_FLOAT

// parseEvent decodes one Event payload and returns its training step
// plus a map of tag → float value for every scalar Summary.Value
// inside. An Event with no scalar values (file_version, graph_def,
// tensors whose dtype isn't DT_FLOAT, histograms, ...) returns an
// empty map, not an error.
func parseEvent(b []byte) (int64, map[string]float64, error) {
	var step int64
	scalars := map[string]float64{}
	r := &pbReader{buf: b}
	for !r.eof() {
		tag, wt, err := r.readTag()
		if err != nil {
			return 0, nil, err
		}
		switch {
		case tag == 1 && wt == 1: // wall_time double — not needed
			if err := r.skipFixed64(); err != nil {
				return 0, nil, err
			}
		case tag == 2 && wt == 0: // step varint
			v, err := r.readVarint()
			if err != nil {
				return 0, nil, err
			}
			// protobuf int64 is serialised as varint; the low 64 bits
			// cast directly.
			step = int64(v)
		case tag == 5 && wt == 2: // summary
			sub, err := r.readBytes()
			if err != nil {
				return 0, nil, err
			}
			if err := parseSummary(sub, scalars); err != nil {
				return 0, nil, err
			}
		default:
			if err := r.skip(wt); err != nil {
				return 0, nil, err
			}
		}
	}
	return step, scalars, nil
}

// parseSummary walks Summary.value (repeated) and folds each scalar
// into out. Non-scalar values (histograms, images, audio, non-float
// tensors) are silently ignored.
func parseSummary(b []byte, out map[string]float64) error {
	r := &pbReader{buf: b}
	for !r.eof() {
		tag, wt, err := r.readTag()
		if err != nil {
			return err
		}
		if tag == 1 && wt == 2 {
			sub, err := r.readBytes()
			if err != nil {
				return err
			}
			if err := parseValue(sub, out); err != nil {
				return err
			}
			continue
		}
		if err := r.skip(wt); err != nil {
			return err
		}
	}
	return nil
}

// parseValue decodes one Summary.Value and, if it carries a scalar,
// folds (tag → value) into out. Both the legacy simple_value path and
// the newer single-element DT_FLOAT TensorProto path are supported.
func parseValue(b []byte, out map[string]float64) error {
	var (
		tagName       string
		haveSimple    bool
		simple        float32
		haveTensorVal bool
		tensorVal     float32
		tensorDtype   uint64 // default 0 = DT_INVALID until we see it
	)
	r := &pbReader{buf: b}
	for !r.eof() {
		fieldTag, wt, err := r.readTag()
		if err != nil {
			return err
		}
		switch {
		case fieldTag == 1 && wt == 2: // tag (metric name)
			s, err := r.readString()
			if err != nil {
				return err
			}
			tagName = s
		case fieldTag == 2 && wt == 5: // simple_value float32
			f, err := r.readFixed32()
			if err != nil {
				return err
			}
			simple = math.Float32frombits(f)
			haveSimple = true
		case fieldTag == 8 && wt == 2: // tensor TensorProto
			sub, err := r.readBytes()
			if err != nil {
				return err
			}
			dt, v, ok, perr := parseTensorScalar(sub)
			if perr != nil {
				return perr
			}
			if ok {
				tensorDtype = dt
				tensorVal = v
				haveTensorVal = true
			}
		default:
			if err := r.skip(wt); err != nil {
				return err
			}
		}
	}
	if tagName == "" {
		return nil
	}
	if haveSimple {
		out[tagName] = float64(simple)
		return nil
	}
	if haveTensorVal && tensorDtype == dtFloat {
		out[tagName] = float64(tensorVal)
	}
	return nil
}

// parseTensorScalar pulls the dtype and first float out of a
// TensorProto. Returns ok=false if the proto has no float_val at all
// (e.g. a dense double tensor, a string tensor); non-float dtypes
// return their dtype but ok=false so the caller can decide (current
// policy: drop).
func parseTensorScalar(b []byte) (dtype uint64, value float32, ok bool, err error) {
	r := &pbReader{buf: b}
	for !r.eof() {
		tag, wt, rerr := r.readTag()
		if rerr != nil {
			return 0, 0, false, rerr
		}
		switch {
		case tag == 2 && wt == 0: // dtype
			v, rerr := r.readVarint()
			if rerr != nil {
				return 0, 0, false, rerr
			}
			dtype = v
		case tag == 4 && wt == 5: // single float_val (non-packed)
			if !ok {
				f, rerr := r.readFixed32()
				if rerr != nil {
					return 0, 0, false, rerr
				}
				value = math.Float32frombits(f)
				ok = true
			} else if err := r.skipFixed32(); err != nil {
				return 0, 0, false, err
			}
		case tag == 4 && wt == 2: // packed float_val
			sub, rerr := r.readBytes()
			if rerr != nil {
				return 0, 0, false, rerr
			}
			if !ok && len(sub) >= 4 {
				value = math.Float32frombits(binary.LittleEndian.Uint32(sub[:4]))
				ok = true
			}
		default:
			if err := r.skip(wt); err != nil {
				return 0, 0, false, err
			}
		}
	}
	return dtype, value, ok, nil
}

// ---- low level wire reader ----

type pbReader struct {
	buf []byte
	i   int
}

func (r *pbReader) eof() bool { return r.i >= len(r.buf) }

func (r *pbReader) readVarint() (uint64, error) {
	var v uint64
	var shift uint
	for {
		if r.i >= len(r.buf) {
			return 0, errors.New("proto: truncated varint")
		}
		b := r.buf[r.i]
		r.i++
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, errors.New("proto: varint overflow")
		}
	}
}

func (r *pbReader) readTag() (int, int, error) {
	v, err := r.readVarint()
	if err != nil {
		return 0, 0, err
	}
	return int(v >> 3), int(v & 0x7), nil
}

func (r *pbReader) readBytes() ([]byte, error) {
	n, err := r.readVarint()
	if err != nil {
		return nil, err
	}
	if uint64(len(r.buf)-r.i) < n {
		return nil, errors.New("proto: truncated length-delimited field")
	}
	out := r.buf[r.i : r.i+int(n)]
	r.i += int(n)
	return out, nil
}

func (r *pbReader) readString() (string, error) {
	b, err := r.readBytes()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *pbReader) readFixed32() (uint32, error) {
	if len(r.buf)-r.i < 4 {
		return 0, errors.New("proto: truncated fixed32")
	}
	v := binary.LittleEndian.Uint32(r.buf[r.i : r.i+4])
	r.i += 4
	return v, nil
}

func (r *pbReader) skipFixed32() error {
	if len(r.buf)-r.i < 4 {
		return errors.New("proto: truncated fixed32")
	}
	r.i += 4
	return nil
}

func (r *pbReader) skipFixed64() error {
	if len(r.buf)-r.i < 8 {
		return errors.New("proto: truncated fixed64")
	}
	r.i += 8
	return nil
}

func (r *pbReader) skip(wt int) error {
	switch wt {
	case 0:
		_, err := r.readVarint()
		return err
	case 1:
		return r.skipFixed64()
	case 2:
		_, err := r.readBytes()
		return err
	case 5:
		return r.skipFixed32()
	default:
		return fmt.Errorf("proto: unsupported wire type %d", wt)
	}
}
