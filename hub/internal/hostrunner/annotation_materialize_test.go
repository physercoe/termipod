// Tests for the D5 image-materialization fallback
// (docs/plans/desktop-ui-context-and-pointing.md §3.5): the drivers
// that cannot take an image block still deliver the user's annotation,
// by writing it where the agent's own file tools can open it.

package hostrunner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testStamp = time.Date(2026, 7, 31, 4, 5, 6, 0, time.UTC)

func TestMaterializeImageInputs_WritesUnderTheAgentsOwnWorkdir(t *testing.T) {
	wd := t.TempDir()
	// "aGVsbG8=" is "hello".
	paths, err := materializeImageInputs(wd, []imageInput{
		{mime: "image/png", data: "aGVsbG8="},
		{mime: "image/jpeg", data: "aGVsbG8="},
	}, testStamp)
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("want 2 paths, got %v", paths)
	}
	// Under .termipod/ — a directory the agent already has, so no new
	// permission surface appears for the sake of an annotation.
	want := filepath.Join(wd, ".termipod", "annotations")
	for _, p := range paths {
		if filepath.Dir(p) != want {
			t.Errorf("path %q is not under %q", p, want)
		}
		info, serr := os.Stat(p)
		if serr != nil {
			t.Fatalf("stat %s: %v", p, serr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600 (it is a frame of the user's screen)", p, info.Mode().Perm())
		}
	}
	// The extension follows the mime, so the agent's reader picks the
	// right decoder.
	if filepath.Ext(paths[0]) != ".png" || filepath.Ext(paths[1]) != ".jpg" {
		t.Errorf("extensions do not follow the mime types: %v", paths)
	}
	body, rerr := os.ReadFile(paths[0])
	if rerr != nil || string(body) != "hello" {
		t.Errorf("decoded bytes = %q (err %v), want %q", string(body), rerr, "hello")
	}
	// Stamped + indexed, so a workdir's annotations read chronologically.
	if !strings.HasPrefix(filepath.Base(paths[0]), "20260731T040506Z-1") {
		t.Errorf("unexpected filename %q", filepath.Base(paths[0]))
	}
}

func TestMaterializeImageInputs_FailsLoudlyWithNowhereToWrite(t *testing.T) {
	// No workdir derived: the caller must be able to tell this from
	// success, because its fallback is to warn the principal that the
	// annotation did not arrive (plan §3.5).
	paths, err := materializeImageInputs("", []imageInput{{mime: "image/png", data: "aGVsbG8="}}, testStamp)
	if err == nil {
		t.Fatal("want an error when there is no workdir")
	}
	if len(paths) != 0 {
		t.Errorf("want no paths, got %v", paths)
	}

	// A workdir that cannot hold a directory (it is a FILE) fails the
	// same way rather than panicking.
	f := filepath.Join(t.TempDir(), "not-a-dir")
	if werr := os.WriteFile(f, []byte("x"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	if _, err = materializeImageInputs(f, []imageInput{{mime: "image/png", data: "aGVsbG8="}}, testStamp); err == nil {
		t.Error("want an error when the workdir is not a directory")
	}
}

func TestMaterializeImageInputs_PartialWriteReportsWhatLanded(t *testing.T) {
	// The second image is malformed. The first still landed, and the
	// caller needs its path so the note can name what the agent CAN
	// open — reporting zero would throw away a delivered image.
	wd := t.TempDir()
	paths, err := materializeImageInputs(wd, []imageInput{
		{mime: "image/png", data: "aGVsbG8="},
		{mime: "image/png", data: "!!!not base64!!!"},
	}, testStamp)
	if err == nil {
		t.Fatal("want an error for the malformed image")
	}
	if len(paths) != 1 {
		t.Fatalf("want the one good path back, got %v", paths)
	}
}

func TestMaterializeImageInputs_ToleratesWhitespaceInBase64(t *testing.T) {
	// The MCP attach path is known to produce whitespace-bearing base64;
	// a payload that survived hub validation must not die here on a
	// newline.
	wd := t.TempDir()
	paths, err := materializeImageInputs(wd, []imageInput{{mime: "image/png", data: "aGVs\nbG8 ="}}, testStamp)
	if err != nil || len(paths) != 1 {
		t.Fatalf("materialize: %v (paths %v)", err, paths)
	}
	body, _ := os.ReadFile(paths[0])
	if string(body) != "hello" {
		t.Errorf("decoded %q, want %q", string(body), "hello")
	}
}

func TestMaterializeImageInputs_NoImagesIsNotAnError(t *testing.T) {
	paths, err := materializeImageInputs("", nil, testStamp)
	if err != nil || paths != nil {
		t.Errorf("empty input must be a no-op: %v / %v", paths, err)
	}
}

func TestAnnotationNote_PutsTheUsersWordsFirst(t *testing.T) {
	one := annotationNote("why is this red?", []string{"/w/.termipod/annotations/a.png"})
	if !strings.HasPrefix(one, "why is this red?") {
		t.Errorf("the user's message must lead: %q", one)
	}
	if !strings.Contains(one, "/w/.termipod/annotations/a.png") {
		t.Errorf("the note must name the path: %q", one)
	}
	if !strings.Contains(one, "file tools") {
		t.Errorf("the note must say HOW to open it: %q", one)
	}

	// "Just point at this" — an empty body still yields a usable turn.
	bare := annotationNote("", []string{"/w/a.png"})
	if strings.TrimSpace(bare) == "" || !strings.Contains(bare, "/w/a.png") {
		t.Errorf("image-only annotation must still say something: %q", bare)
	}

	multi := annotationNote("look", []string{"/w/a.png", "/w/b.png"})
	if !strings.Contains(multi, "/w/a.png") || !strings.Contains(multi, "/w/b.png") {
		t.Errorf("every path must be named: %q", multi)
	}

	// Nothing materialized: the body is untouched, so a driver that
	// failed to write does not append an empty bracket.
	if got := annotationNote("look", nil); got != "look" {
		t.Errorf("no paths must leave the body alone, got %q", got)
	}
}

func TestAnnotationExt_UnknownMimeIsGenericNotWrong(t *testing.T) {
	if got := annotationExt("image/heic"); got != ".bin" {
		// A wrong extension is a worse lie than a generic one: the note
		// names the path either way, and the agent sniffs the bytes.
		t.Errorf("unknown mime ext = %q, want .bin", got)
	}
	if got := annotationExt("IMAGE/PNG"); got != ".png" {
		t.Errorf("mime matching must be case-insensitive, got %q", got)
	}
}
