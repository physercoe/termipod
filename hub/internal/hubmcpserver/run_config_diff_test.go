package hubmcpserver

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

// jsNumber is the reason the two implementations can share a row model: the
// desktop renders leaves with JavaScript's Number→string, so Go must agree
// digit for digit. The interesting cases are the thresholds — JS switches to
// exponential only outside [1e-6, 1e21) and writes the exponent without
// leading zeros, where Go's %e would pad it.
func TestJSNumber_MatchesJavaScript(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{math.Copysign(0, -1), "0"}, // JS prints -0 as "0"
		{10, "10"},
		{0.0003, "0.0003"},
		{0.30000000000000004, "0.30000000000000004"},
		{1000000, "1000000"},
		{1e-6, "0.000001"}, // still decimal — the boundary is exclusive
		{1e-7, "1e-7"},     // Go's %e would say 1e-07
		{1e20, "100000000000000000000"},
		{1e21, "1e+21"},
		{-2.5, "-2.5"},
		{math.NaN(), "NaN"},
		{math.Inf(1), "Infinity"},
	}
	for _, c := range cases {
		if got := jsNumber(c.in); got != c.want {
			t.Errorf("jsNumber(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCfgFlatten_TextAndValueShapes(t *testing.T) {
	// runs.config_json is TEXT; the /config digest is a JSON value. Both must
	// flatten the same way, or the union below would compare a run against
	// itself and find differences.
	fromText := cfgFlattenText(`{"lr":0.001,"model":{"depth":12}}`)
	fromRaw := cfgFlattenRaw(json.RawMessage(`{"lr":0.001,"model":{"depth":12}}`))
	if !reflect.DeepEqual(fromText, fromRaw) {
		t.Errorf("text and value shapes disagree:\n text=%+v\n raw =%+v", fromText, fromRaw)
	}
	// A digest that arrives as a JSON string is unwrapped once.
	if got := cfgFlattenRaw(json.RawMessage(`"{\"lr\":0.001,\"model\":{\"depth\":12}}"`)); !reflect.DeepEqual(got, fromText) {
		t.Errorf("quoted digest did not unwrap: %+v", got)
	}
	// Garbage degrades to no rows rather than failing the call.
	for _, bad := range []string{"", "   ", "not json", "42", "null"} {
		if got := cfgFlattenText(bad); len(got) != 0 {
			t.Errorf("cfgFlattenText(%q) = %+v, want no rows", bad, got)
		}
	}
	if got := cfgFlattenRaw(nil); len(got) != 0 {
		t.Errorf("cfgFlattenRaw(nil) = %+v, want no rows", got)
	}
}

func TestCfgMerge_LoggedWinsAndNamesTheConflict(t *testing.T) {
	registered := cfgFlattenText(`{"lr":0.001,"epochs":10}`)
	logged := cfgFlattenText(`{"lr":0.0003,"seed":7}`)
	entries, conflicts := cfgMerge(registered, logged)
	byKey := map[string]string{}
	for _, e := range entries {
		byKey[e.Key] = e.Value
	}
	if byKey["lr"] != "0.0003" {
		t.Errorf("lr = %q, want the LOGGED value (it is what ran)", byKey["lr"])
	}
	if byKey["epochs"] != "10" || byKey["seed"] != "7" {
		t.Errorf("single-source keys did not survive: %+v", byKey)
	}
	if !reflect.DeepEqual(conflicts, []string{"lr"}) {
		t.Errorf("conflicts = %+v, want [lr] — a disagreement is a finding, not something to swallow", conflicts)
	}
	if _, none := cfgMerge(registered, registered); len(none) != 0 {
		t.Errorf("identical sources reported a conflict: %+v", none)
	}
}

// The shared fixture (see its own `note`). This is the whole point of writing
// the comparer twice: one file, two languages, one row model.
func TestConfigDiff_SharedFixture(t *testing.T) {
	blob, err := os.ReadFile("testdata/config_diff_fixture.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var fx struct {
		Runs []struct {
			ID         string          `json:"id"`
			Registered json.RawMessage `json:"registered"`
			Logged     json.RawMessage `json:"logged"`
		} `json:"runs"`
		ExpectConflicts map[string][]string `json:"expect_conflicts"`
		ExpectDiffering int                 `json:"expect_differing"`
		ExpectRows      []cfgDiffRow        `json:"expect_rows"`
	}
	if err := json.Unmarshal(blob, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	flat := make([]cfgRun, 0, len(fx.Runs))
	conflicts := map[string][]string{}
	for _, r := range fx.Runs {
		entries, conflicted := cfgMerge(cfgFlattenRaw(r.Registered), cfgFlattenRaw(r.Logged))
		if len(conflicted) > 0 {
			conflicts[r.ID] = conflicted
		}
		flat = append(flat, cfgRun{ID: r.ID, Entries: entries})
	}
	rows := cfgDiffRows(flat)

	if len(rows) != len(fx.ExpectRows) {
		t.Fatalf("got %d rows, want %d\n got: %s", len(rows), len(fx.ExpectRows), mustJSON(t, rows))
	}
	for i, want := range fx.ExpectRows {
		if !reflect.DeepEqual(rows[i], want) {
			t.Errorf("row %d:\n got  %s\n want %s", i, mustJSON(t, rows[i]), mustJSON(t, want))
		}
	}
	if !reflect.DeepEqual(conflicts, fx.ExpectConflicts) {
		t.Errorf("conflicts = %s, want %s", mustJSON(t, conflicts), mustJSON(t, fx.ExpectConflicts))
	}
	differing := 0
	for _, r := range rows {
		if !r.Identical {
			differing++
		}
	}
	if differing != fx.ExpectDiffering {
		t.Errorf("differing = %d, want %d", differing, fx.ExpectDiffering)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// The tool reads two endpoints per run and returns the row model. Fewer than
// two runs is refused before any hub call — a "diff" of one run is a config
// dump wearing the wrong name.
func TestToolsCall_RunConfigDiff(t *testing.T) {
	paths := []string{}
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/config"):
			if strings.Contains(r.URL.Path, "r_1") {
				_, _ = w.Write([]byte(`{"config":{"lr":0.001}}`))
				return
			}
			_, _ = w.Write([]byte(`{"config":null}`))
		default:
			if strings.Contains(r.URL.Path, "r_1") {
				_, _ = w.Write([]byte(`{"id":"r_1","config_json":"{\"lr\":0.1,\"seed\":1}"}`))
				return
			}
			_, _ = w.Write([]byte(`{"id":"r_2","config_json":"{\"lr\":0.1,\"seed\":2}"}`))
		}
	})
	tools := buildTools()

	line := []byte(`{"jsonrpc":"2.0","id":21,"method":"tools/call","params":{"name":"run_config_diff","arguments":{"runs":["r_1","r_2"]}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, raw)
	}
	if resp.Result.IsError {
		t.Fatalf("tool reported isError: %+v", resp.Result.Content)
	}
	var out struct {
		Runs      []string            `json:"runs"`
		Rows      []cfgDiffRow        `json:"rows"`
		Differing int                 `json:"differing"`
		Conflicts map[string][]string `json:"conflicts"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &out); err != nil {
		t.Fatalf("decode tool result: %v (%s)", err, resp.Result.Content[0].Text)
	}
	if len(paths) != 4 {
		t.Errorf("hit %d endpoints, want 4 (run + digest per run): %v", len(paths), paths)
	}
	if !reflect.DeepEqual(out.Runs, []string{"r_1", "r_2"}) {
		t.Errorf("column order lost: %v", out.Runs)
	}
	byKey := map[string]cfgDiffRow{}
	for _, r := range out.Rows {
		byKey[r.Key] = r
	}
	// r_1's digest logged lr=0.001 over the registered 0.1 — so the runs differ
	// on lr, and the override is reported as a conflict rather than hidden.
	if byKey["lr"].Identical {
		t.Errorf("lr should differ once the digest overrides it: %+v", byKey["lr"])
	}
	if !reflect.DeepEqual(out.Conflicts["r_1"], []string{"lr"}) {
		t.Errorf("conflicts = %+v, want r_1:[lr]", out.Conflicts)
	}
	if byKey["seed"].Identical || out.Differing != 2 {
		t.Errorf("expected lr + seed to differ, got differing=%d rows=%+v", out.Differing, out.Rows)
	}
}

func TestToolsCall_RunConfigDiff_RefusesFewerThanTwo(t *testing.T) {
	called := false
	c := newTestHub(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	tools := buildTools()

	line := []byte(`{"jsonrpc":"2.0","id":22,"method":"tools/call","params":{"name":"run_config_diff","arguments":{"runs":["r_1"]}}}` + "\n")
	raw, ok := handleLine(c, tools, line)
	if !ok {
		t.Fatalf("expected a response")
	}
	if called {
		t.Error("a one-run diff still reached the hub")
	}
	// The schema's minItems fires first and the closure's own guard backs it
	// up; this asserts the outcome, not which layer spoke. Decoded rather than
	// grepped: encoding/json escapes the greater-than sign in the envelope,
	// so a substring match on the raw bytes would fail for reasons that have
	// nothing to do with the refusal.
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal envelope: %v (%s)", err, raw)
	}
	if !resp.Result.IsError {
		t.Fatalf("a one-run diff reported success: %s", raw)
	}
	if len(resp.Result.Content) == 0 || !strings.Contains(resp.Result.Content[0].Text, ">= 2 item") {
		t.Errorf("refusal does not say what is wrong: %s", raw)
	}
}
