package tbreader

import (
	"encoding/binary"
	"math"
	"testing"
)

// ---- tiny protobuf encoder helpers for tests ----
//
// These are not exported from the package because the host-runner
// only ever reads tfevents streams; production code never serialises
// Events. Keeping them test-only avoids a dead code path in the
// shipped binary.

func pbVarint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func pbTag(fieldNum, wireType int) []byte {
	return pbVarint(uint64(fieldNum)<<3 | uint64(wireType))
}

func pbField(fieldNum int, wireType int, body []byte) []byte {
	return append(pbTag(fieldNum, wireType), body...)
}

func pbLenDelim(fieldNum int, body []byte) []byte {
	out := pbTag(fieldNum, 2)
	out = append(out, pbVarint(uint64(len(body)))...)
	return append(out, body...)
}

func pbFixed32(fieldNum int, v uint32) []byte {
	out := pbTag(fieldNum, 5)
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	return append(out, b[:]...)
}

func pbFixed64(fieldNum int, v uint64) []byte {
	out := pbTag(fieldNum, 1)
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	return append(out, b[:]...)
}

func buildValueSimple(tag string, value float32) []byte {
	var out []byte
	out = append(out, pbLenDelim(1, []byte(tag))...)
	out = append(out, pbFixed32(2, math.Float32bits(value))...)
	return out
}

func buildValueTensor(tag string, dtype uint64, value float32) []byte {
	tensor := pbField(2, 0, pbVarint(dtype))
	tensor = append(tensor, pbFixed32(4, math.Float32bits(value))...)

	var out []byte
	out = append(out, pbLenDelim(1, []byte(tag))...)
	out = append(out, pbLenDelim(8, tensor)...)
	return out
}

func buildSummary(values ...[]byte) []byte {
	var out []byte
	for _, v := range values {
		out = append(out, pbLenDelim(1, v)...)
	}
	return out
}

func buildEvent(step int64, wallTime float64, summary []byte) []byte {
	var out []byte
	out = append(out, pbFixed64(1, math.Float64bits(wallTime))...)
	out = append(out, pbField(2, 0, pbVarint(uint64(step)))...)
	out = append(out, pbLenDelim(5, summary)...)
	return out
}

// ---- tests ----

func TestParseEvent_SimpleValue(t *testing.T) {
	ev := buildEvent(42, 1.0,
		buildSummary(buildValueSimple("loss/train", 0.25)))

	step, scalars, err := parseEvent(ev)
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if step != 42 {
		t.Errorf("step=%d, want 42", step)
	}
	got, ok := scalars["loss/train"]
	if !ok {
		t.Fatalf("loss/train missing: %+v", scalars)
	}
	if math.Abs(got-0.25) > 1e-6 {
		t.Errorf("loss/train=%v, want 0.25", got)
	}
}

func TestParseEvent_TensorScalarFloat(t *testing.T) {
	ev := buildEvent(100, 2.0,
		buildSummary(buildValueTensor("acc", dtFloat, 0.9)))

	step, scalars, err := parseEvent(ev)
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if step != 100 {
		t.Errorf("step=%d, want 100", step)
	}
	got, ok := scalars["acc"]
	if !ok {
		t.Fatalf("acc missing: %+v", scalars)
	}
	if math.Abs(got-0.9) > 1e-6 {
		t.Errorf("acc=%v, want ~0.9", got)
	}
}

func TestParseEvent_TensorNonFloatIsSkipped(t *testing.T) {
	// dtype = 2 (DT_DOUBLE). Our reader only picks up DT_FLOAT scalars;
	// other types should leave the map empty.
	ev := buildEvent(7, 1.0,
		buildSummary(buildValueTensor("double_metric", 2, 0.5)))

	_, scalars, err := parseEvent(ev)
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if _, ok := scalars["double_metric"]; ok {
		t.Errorf("non-float tensor leaked into scalars: %+v", scalars)
	}
}

func TestParseEvent_MultipleValuesInOneSummary(t *testing.T) {
	ev := buildEvent(5, 1.0, buildSummary(
		buildValueSimple("loss", 1.5),
		buildValueSimple("acc", 0.6),
	))

	step, scalars, err := parseEvent(ev)
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if step != 5 {
		t.Errorf("step=%d, want 5", step)
	}
	if len(scalars) != 2 {
		t.Fatalf("scalars=%+v, want 2 entries", scalars)
	}
	if scalars["loss"] != float64(float32(1.5)) {
		t.Errorf("loss=%v, want 1.5", scalars["loss"])
	}
	if math.Abs(scalars["acc"]-float64(float32(0.6))) > 1e-6 {
		t.Errorf("acc=%v, want 0.6", scalars["acc"])
	}
}

func TestParseEvent_FileVersionIsSkipped(t *testing.T) {
	// First record in a real tfevents file is always
	//   Event{ wall_time, file_version = "brain.Event:2" }
	// with no summary. The reader should return step=0 and an empty
	// scalar map, not an error.
	var ev []byte
	ev = append(ev, pbFixed64(1, math.Float64bits(1.0))...)
	ev = append(ev, pbLenDelim(3, []byte("brain.Event:2"))...)

	step, scalars, err := parseEvent(ev)
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if step != 0 {
		t.Errorf("step=%d, want 0", step)
	}
	if len(scalars) != 0 {
		t.Errorf("scalars=%+v, want empty", scalars)
	}
}

func TestParseEvent_UnknownFieldsAreSkipped(t *testing.T) {
	// Append an unknown varint field (tag 99) — the decoder should
	// skip it by wire-type and still surface the known fields.
	ev := buildEvent(11, 1.0, buildSummary(buildValueSimple("m", 0.1)))
	ev = append(ev, pbField(99, 0, pbVarint(12345))...)

	step, scalars, err := parseEvent(ev)
	if err != nil {
		t.Fatalf("parseEvent: %v", err)
	}
	if step != 11 {
		t.Errorf("step=%d, want 11", step)
	}
	if _, ok := scalars["m"]; !ok {
		t.Errorf("m missing despite trailing unknown field: %+v", scalars)
	}
}
