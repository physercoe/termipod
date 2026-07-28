// Package handoff carries a session's host-owned byte-stores (the git worktree
// is moved via git; the engine-state directory is moved through here) between
// two hosts during a teleport (ADR-057). The hub's blob store caps a single
// blob at 25 MiB and buffers the whole upload in memory
// (server/handlers_blobs.go), while an engine-state tar can exceed that — so a
// bundle is split into ≤chunk-size parts, each uploaded as an ordinary
// content-addressed blob, described by a small Manifest that is itself stored
// as a blob. The receiving host fetches the parts sequentially (bounded memory
// on both sides), verifies each part against its content address and the whole
// stream against the manifest's tar hash, and reassembles.
//
// This package is deliberately transport-only: it moves an opaque byte stream
// and knows nothing about tar, git, or engine layouts. Callers tar into Pack's
// reader and untar out of Unpack's writer. It imports only the standard library
// so both the host-runner and the hub server can depend on it without a cycle.
package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
)

// ManifestVersion is the on-the-wire schema version of Manifest. Bump it only
// on an incompatible change; Unpack refuses a version it doesn't understand.
const ManifestVersion = 1

// DefaultChunkSize is the target size of each blob part. It sits safely under
// the hub's 25 MiB blob cap (server/handlers_blobs.go maxBlobBytes) so an
// off-by-one in framing can never push a part over the limit.
const DefaultChunkSize = 24 << 20 // 24 MiB

// Blob MIME types. The manifest carries a distinct type so an operator reading
// the blob store can tell an index from a data part.
const (
	ManifestMime = "application/vnd.termipod.handoff-manifest+json"
	PartMime     = "application/octet-stream"
)

// Manifest indexes the ordered blob parts of one bundle plus the integrity
// checks the receiver verifies before trusting the reassembled stream.
type Manifest struct {
	Version int `json:"v"`
	// TarSHA256 is the hex SHA-256 of the WHOLE reassembled stream (not of any
	// individual part) — the end-to-end integrity check.
	TarSHA256 string `json:"tar_sha256"`
	// TotalSize is the reassembled stream length in bytes.
	TotalSize int64 `json:"total_size"`
	// Parts are the content addresses (hex SHA-256) of the ordered parts. Each
	// address is also a per-part integrity check, since the blob store is
	// content-addressed.
	Parts []string `json:"parts"`
}

// BlobStore is the subset of the hub blob API this package needs. The
// host-runner implements it against POST/GET /v1/blobs; tests use an in-memory
// map. Put returns the content address (hex SHA-256) the store assigned.
type BlobStore interface {
	Put(ctx context.Context, body []byte, mime string) (sha string, err error)
	Get(ctx context.Context, sha string) ([]byte, error)
}

// Pack streams r in ≤chunkSize parts, uploads each as a blob, and returns a
// Manifest describing them. Memory use is bounded by chunkSize regardless of
// the total stream length. A non-positive chunkSize uses DefaultChunkSize. An
// empty stream yields a valid manifest with no parts (the SHA-256 of the empty
// string), which Unpack round-trips to an empty output.
func Pack(ctx context.Context, r io.Reader, store BlobStore, chunkSize int) (Manifest, error) {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	h := sha256.New()
	var total int64
	parts := []string{}
	buf := make([]byte, chunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		n, err := io.ReadFull(r, buf)
		if n > 0 {
			chunk := buf[:n]
			h.Write(chunk)
			total += int64(n)
			// Copy: buf is reused next iteration and Put may retain the slice.
			part := make([]byte, n)
			copy(part, chunk)
			sha, perr := store.Put(ctx, part, PartMime)
			if perr != nil {
				return Manifest{}, fmt.Errorf("upload part %d: %w", len(parts), perr)
			}
			parts = append(parts, sha)
		}
		// io.ReadFull reports EOF (read 0) or ErrUnexpectedEOF (short final
		// read); both mean the stream is exhausted after this iteration.
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("read stream: %w", err)
		}
	}
	return Manifest{
		Version:   ManifestVersion,
		TarSHA256: hex.EncodeToString(h.Sum(nil)),
		TotalSize: total,
		Parts:     parts,
	}, nil
}

// Unpack fetches m's parts in order, writes the reassembled stream to w, and
// verifies both each part's content address and the whole-stream tar hash +
// size. It returns an error (having possibly written a prefix to w) if any
// check fails — callers must not trust w's contents unless Unpack returns nil.
func Unpack(ctx context.Context, m Manifest, store BlobStore, w io.Writer) error {
	if m.Version != ManifestVersion {
		return fmt.Errorf("unsupported manifest version %d (want %d)", m.Version, ManifestVersion)
	}
	h := sha256.New()
	mw := io.MultiWriter(w, h)
	var total int64
	for i, sha := range m.Parts {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := store.Get(ctx, sha)
		if err != nil {
			return fmt.Errorf("fetch part %d (%s): %w", i, sha, err)
		}
		// The store is content-addressed, but a compromised or buggy store
		// could hand back the wrong bytes; re-verify the part address.
		got := sha256.Sum256(body)
		if h := hex.EncodeToString(got[:]); h != sha {
			return fmt.Errorf("part %d content-address mismatch: got %s want %s", i, h, sha)
		}
		n, err := mw.Write(body)
		if err != nil {
			return fmt.Errorf("write part %d: %w", i, err)
		}
		total += int64(n)
	}
	if total != m.TotalSize {
		return fmt.Errorf("size mismatch: reassembled %d, manifest says %d", total, m.TotalSize)
	}
	if sum := hex.EncodeToString(h.Sum(nil)); sum != m.TarSHA256 {
		return fmt.Errorf("tar hash mismatch: reassembled %s, manifest says %s", sum, m.TarSHA256)
	}
	return nil
}

// PutManifest serialises m and stores it as a blob, returning its content
// address — the single handle a teleport passes from the pack step to the
// unpack step.
func PutManifest(ctx context.Context, m Manifest, store BlobStore) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return store.Put(ctx, b, ManifestMime)
}

// GetManifest fetches and parses a manifest blob by its content address.
func GetManifest(ctx context.Context, sha string, store BlobStore) (Manifest, error) {
	b, err := store.Get(ctx, sha)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse manifest %s: %w", sha, err)
	}
	return m, nil
}
