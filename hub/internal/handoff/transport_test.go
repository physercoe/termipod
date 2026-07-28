package handoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
)

// memStore is an in-memory content-addressed BlobStore for the transport tests
// — the same content-address contract as the hub blob store, no HTTP.
type memStore struct {
	m map[string][]byte
}

func newMemStore() *memStore { return &memStore{m: map[string][]byte{}} }

func (s *memStore) Put(_ context.Context, body []byte, _ string) (string, error) {
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	cp := make([]byte, len(body))
	copy(cp, body)
	s.m[sha] = cp
	return sha, nil
}

func (s *memStore) Get(_ context.Context, sha string) ([]byte, error) {
	b, ok := s.m[sha]
	if !ok {
		return nil, fmt.Errorf("blob not found: %s", sha)
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp, nil
}

func mkPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}

func TestPackUnpackRoundTrip(t *testing.T) {
	const chunk = 1024
	cases := []struct {
		name      string
		size      int
		wantParts int
	}{
		{"empty", 0, 0},
		{"one-byte", 1, 1},
		{"under-chunk", 1000, 1},
		{"exact-chunk", chunk, 1},
		{"exact-two-chunks", 2 * chunk, 2},
		{"non-multiple", 2*chunk + 13, 3},
		{"many-parts", 10*chunk + 1, 11},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newMemStore()
			payload := mkPayload(tc.size)
			m, err := Pack(context.Background(), bytes.NewReader(payload), store, chunk)
			if err != nil {
				t.Fatalf("Pack: %v", err)
			}
			if len(m.Parts) != tc.wantParts {
				t.Fatalf("parts: got %d want %d", len(m.Parts), tc.wantParts)
			}
			if m.TotalSize != int64(tc.size) {
				t.Fatalf("total size: got %d want %d", m.TotalSize, tc.size)
			}
			sum := sha256.Sum256(payload)
			if m.TarSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("tar sha mismatch")
			}

			var out bytes.Buffer
			if err := Unpack(context.Background(), m, store, &out); err != nil {
				t.Fatalf("Unpack: %v", err)
			}
			if !bytes.Equal(out.Bytes(), payload) {
				t.Fatalf("round-trip mismatch: got %d bytes want %d", out.Len(), len(payload))
			}
		})
	}
}

func TestPackDefaultChunkSize(t *testing.T) {
	store := newMemStore()
	// A non-positive chunk size falls back to DefaultChunkSize; a small payload
	// then fits in a single part.
	m, err := Pack(context.Background(), bytes.NewReader(mkPayload(100)), store, 0)
	if err != nil {
		t.Fatalf("Pack: %v", err)
	}
	if len(m.Parts) != 1 {
		t.Fatalf("expected 1 part with default chunk, got %d", len(m.Parts))
	}
}

func TestPackDeterministic(t *testing.T) {
	payload := mkPayload(5000)
	a, err := Pack(context.Background(), bytes.NewReader(payload), newMemStore(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Pack(context.Background(), bytes.NewReader(payload), newMemStore(), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if a.TarSHA256 != b.TarSHA256 || len(a.Parts) != len(b.Parts) {
		t.Fatal("pack not deterministic")
	}
	for i := range a.Parts {
		if a.Parts[i] != b.Parts[i] {
			t.Fatalf("part %d differs across packs", i)
		}
	}
}

func TestUnpackRejectsBadVersion(t *testing.T) {
	m := Manifest{Version: 99}
	if err := Unpack(context.Background(), m, newMemStore(), &bytes.Buffer{}); err == nil {
		t.Fatal("expected version rejection")
	}
}

func TestUnpackRejectsTamperedPart(t *testing.T) {
	store := newMemStore()
	payload := mkPayload(3000)
	m, err := Pack(context.Background(), bytes.NewReader(payload), store, 1024)
	if err != nil {
		t.Fatal(err)
	}
	// Corrupt the bytes behind the first part's address in the store; Get now
	// returns bytes whose hash != the address. Flip a byte of the real part so
	// the content genuinely differs (a fresh mkPayload of the same length would
	// be byte-identical, since mkPayload is index-derived).
	tampered := make([]byte, len(store.m[m.Parts[0]]))
	copy(tampered, store.m[m.Parts[0]])
	tampered[0] ^= 0xff
	store.m[m.Parts[0]] = tampered
	err = Unpack(context.Background(), m, store, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected content-address mismatch")
	}
}

func TestUnpackRejectsWrongTotalSize(t *testing.T) {
	store := newMemStore()
	m, err := Pack(context.Background(), bytes.NewReader(mkPayload(2500)), store, 1024)
	if err != nil {
		t.Fatal(err)
	}
	m.TotalSize = 9999
	if err := Unpack(context.Background(), m, store, &bytes.Buffer{}); err == nil {
		t.Fatal("expected size mismatch")
	}
}

func TestUnpackRejectsWrongTarHash(t *testing.T) {
	store := newMemStore()
	m, err := Pack(context.Background(), bytes.NewReader(mkPayload(2500)), store, 1024)
	if err != nil {
		t.Fatal(err)
	}
	m.TarSHA256 = hex.EncodeToString(make([]byte, 32))
	if err := Unpack(context.Background(), m, store, &bytes.Buffer{}); err == nil {
		t.Fatal("expected tar hash mismatch")
	}
}

func TestUnpackMissingPart(t *testing.T) {
	store := newMemStore()
	m, err := Pack(context.Background(), bytes.NewReader(mkPayload(2500)), store, 1024)
	if err != nil {
		t.Fatal(err)
	}
	delete(store.m, m.Parts[1])
	if err := Unpack(context.Background(), m, store, &bytes.Buffer{}); err == nil {
		t.Fatal("expected missing-part error")
	}
}

func TestManifestBlobRoundTrip(t *testing.T) {
	store := newMemStore()
	m, err := Pack(context.Background(), bytes.NewReader(mkPayload(4096)), store, 1024)
	if err != nil {
		t.Fatal(err)
	}
	sha, err := PutManifest(context.Background(), m, store)
	if err != nil {
		t.Fatal(err)
	}
	got, err := GetManifest(context.Background(), sha, store)
	if err != nil {
		t.Fatal(err)
	}
	if got.TarSHA256 != m.TarSHA256 || got.TotalSize != m.TotalSize || len(got.Parts) != len(m.Parts) {
		t.Fatal("manifest blob round-trip mismatch")
	}
}

// A failing store surfaces as a Pack error naming the part index.
func TestPackPropagatesStoreError(t *testing.T) {
	fs := &failStore{failAt: 2}
	_, err := Pack(context.Background(), bytes.NewReader(mkPayload(5000)), fs, 1024)
	if err == nil {
		t.Fatal("expected store error")
	}
}

type failStore struct {
	n      int
	failAt int
}

func (s *failStore) Put(context.Context, []byte, string) (string, error) {
	s.n++
	if s.n >= s.failAt {
		return "", errors.New("boom")
	}
	return fmt.Sprintf("fake-%d", s.n), nil
}
func (s *failStore) Get(context.Context, string) ([]byte, error) { return nil, errors.New("no") }
