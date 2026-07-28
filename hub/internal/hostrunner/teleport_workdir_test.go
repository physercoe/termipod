package hostrunner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// packWorkdir/restoreWorkdir round-trip a nested tree: files, subdirs, and an
// executable bit all survive; the restored tree matches the source byte-for-byte
// and structurally.
func TestWorkdirPackRestore_RoundTrip(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "notes.txt"), "hello workdir", 0o644)
	writeFile(t, filepath.Join(src, "sub", "a.json"), `{"k":1}`, 0o644)
	writeFile(t, filepath.Join(src, "sub", "deep", "b.log"), "line1\nline2\n", 0o644)
	writeFile(t, filepath.Join(src, "run.sh"), "#!/bin/sh\necho hi\n", 0o755)
	// An empty directory must survive too (a bare data/ scratch dir).
	if err := os.MkdirAll(filepath.Join(src, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}

	bundle, err := packWorkdir(src, maxWorkdirBundleBytes)
	if err != nil {
		t.Fatalf("packWorkdir: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "target-workdir")
	if err := restoreWorkdir(dst, bundle); err != nil {
		t.Fatalf("restoreWorkdir: %v", err)
	}

	assertFile(t, filepath.Join(dst, "notes.txt"), "hello workdir")
	assertFile(t, filepath.Join(dst, "sub", "a.json"), `{"k":1}`)
	assertFile(t, filepath.Join(dst, "sub", "deep", "b.log"), "line1\nline2\n")
	assertFile(t, filepath.Join(dst, "run.sh"), "#!/bin/sh\necho hi\n")
	if fi, err := os.Stat(filepath.Join(dst, "empty")); err != nil || !fi.IsDir() {
		t.Fatalf("empty dir not restored: %v", err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dst, "run.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o100 == 0 {
			t.Fatalf("run.sh lost its executable bit: %v", fi.Mode())
		}
	}
}

// A symlink in the source is skipped, not followed — its target must not leak
// into the bundle (it could point outside the workdir).
func TestWorkdirPack_SkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	src := t.TempDir()
	secret := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, secret, "TOP SECRET", 0o600)
	writeFile(t, filepath.Join(src, "ok.txt"), "fine", 0o644)
	if err := os.Symlink(secret, filepath.Join(src, "leak")); err != nil {
		t.Fatal(err)
	}

	bundle, err := packWorkdir(src, maxWorkdirBundleBytes)
	if err != nil {
		t.Fatalf("packWorkdir: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := restoreWorkdir(dst, bundle); err != nil {
		t.Fatalf("restoreWorkdir: %v", err)
	}
	assertFile(t, filepath.Join(dst, "ok.txt"), "fine")
	if _, err := os.Lstat(filepath.Join(dst, "leak")); !os.IsNotExist(err) {
		t.Fatalf("symlink leaked into the bundle (lstat err=%v)", err)
	}
}

// A tampered bundle whose entry name escapes the workdir ("../") is refused —
// restoreWorkdir must never write outside the target dir.
func TestWorkdirRestore_RefusesTraversal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte("pwned")
	if err := tw.WriteHeader(&tar.Header{
		Name: "../escape.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	dst := filepath.Join(t.TempDir(), "wd")
	err := restoreWorkdir(dst, buf.Bytes())
	if err == nil {
		t.Fatal("restoreWorkdir accepted a path-traversal entry")
	}
	// The escape target must not have been written.
	if _, serr := os.Stat(filepath.Join(filepath.Dir(dst), "escape.txt")); !os.IsNotExist(serr) {
		t.Fatalf("traversal wrote outside the workdir (stat err=%v)", serr)
	}
}

// packWorkdir aborts with errWorkdirTooLarge once the compressed output crosses
// the cap, rather than buffering an unbounded tree.
func TestWorkdirPack_CapExceeded(t *testing.T) {
	src := t.TempDir()
	// Incompressible-ish content so gzip can't shrink it under a tiny cap.
	big := make([]byte, 64*1024)
	for i := range big {
		big[i] = byte((i*2654435761 + 40503) >> 8) // cheap pseudo-random, no Rand dep
	}
	writeFile(t, filepath.Join(src, "blob.bin"), string(big), 0o644)

	_, err := packWorkdir(src, 1024) // 1 KiB cap — the 64 KiB blob won't fit
	if err == nil {
		t.Fatal("packWorkdir accepted a bundle over the cap")
	}
	var tooLarge errWorkdirTooLarge
	if !errors.As(err, &tooLarge) {
		t.Fatalf("want errWorkdirTooLarge, got %T: %v", err, err)
	}
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Fatalf("%s:\n got %q\nwant %q", path, got, want)
	}
}
