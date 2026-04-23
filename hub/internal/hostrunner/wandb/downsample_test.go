package wandb

import "testing"

func TestDownsample_UnderMax(t *testing.T) {
	in := []Point{{0, 1}, {1, 2}, {2, 3}}
	got := Downsample(in, 10)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	// Must be a copy — mutating the output should not touch the input.
	got[0].Value = 99
	if in[0].Value != 1 {
		t.Errorf("downsample returned a view, not a copy")
	}
}

func TestDownsample_ZeroMaxKeepsAll(t *testing.T) {
	in := []Point{{0, 1}, {1, 2}, {2, 3}, {3, 4}}
	got := Downsample(in, 0)
	if len(got) != 4 {
		t.Errorf("len=%d want 4 (zero max should keep all)", len(got))
	}
}

func TestDownsample_PreservesEndpoints(t *testing.T) {
	in := make([]Point, 1000)
	for i := range in {
		in[i] = Point{Step: int64(i), Value: float64(i)}
	}
	got := Downsample(in, 100)
	if len(got) > 100 {
		t.Fatalf("len=%d > 100", len(got))
	}
	if got[0].Step != 0 {
		t.Errorf("first=%d want 0", got[0].Step)
	}
	if got[len(got)-1].Step != 999 {
		t.Errorf("last=%d want 999", got[len(got)-1].Step)
	}
	// Steps must be strictly increasing — uniform stride shouldn't repeat.
	for i := 1; i < len(got); i++ {
		if got[i].Step <= got[i-1].Step {
			t.Fatalf("not monotonic at %d: %+v", i, got)
		}
	}
}

func TestDownsample_MaxOneReturnsLast(t *testing.T) {
	in := []Point{{0, 1}, {5, 2}, {10, 3}}
	got := Downsample(in, 1)
	if len(got) != 1 || got[0].Step != 10 {
		t.Errorf("got %+v, want [{10, 3}]", got)
	}
}

func TestDownsample_EmptyInput(t *testing.T) {
	got := Downsample(nil, 100)
	if len(got) != 0 {
		t.Errorf("empty input gave %+v", got)
	}
}
