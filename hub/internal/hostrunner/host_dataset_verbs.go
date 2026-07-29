package hostrunner

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	"github.com/termipod/hub/internal/hostrunner/a2a"
	"github.com/termipod/hub/internal/hostrunner/datasetmeta"
)

// Dataset verbs (replay plan W1). The hub owns the dataset row; the bytes stay
// here, so anything that needs to actually read a `meta/` tree runs on this
// side and returns a fold.
//
// Scope of what these expose: datasetmeta opens files under <root>/meta/ and,
// since the series verb (W2), the data/ parquet the dataset's own info.json
// points at — never a path a caller supplied. Its Source refuses any name that
// escapes the root, so the verbs cannot return arbitrary host files; the most a
// caller learns about a path it guesses is whether a LeRobot dataset sits
// there. What travels is summaries and decimated numbers: video frames and raw
// parquet bytes are never read whole and never sent.

// datasetDigestPayload is the verb-args schema for host.dataset_digest.
type datasetDigestPayload struct {
	RootPath string `json:"root_path"`
}

// datasetEpisodesPayload is the verb-args schema for host.dataset_episodes.
type datasetEpisodesPayload struct {
	RootPath string `json:"root_path"`
	Offset   int64  `json:"offset,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// datasetSeriesPayload is the verb-args schema for host.dataset_series.
type datasetSeriesPayload struct {
	RootPath  string   `json:"root_path"`
	Episode   int64    `json:"episode"`
	Features  []string `json:"features,omitempty"`
	MaxPoints int      `json:"max_points,omitempty"`
}

// handleHostDatasetDigest reads a dataset root and returns its digest plus the
// staleness fingerprint the hub stores alongside.
func (r *Runner) handleHostDatasetDigest(env *a2a.TunnelEnvelope) *a2a.TunnelResponseEnvelope {
	var p datasetDigestPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return datasetVerbError(env, http.StatusBadRequest, "bad_payload", err.Error())
	}
	root, err := cleanDatasetRoot(p.RootPath)
	if err != nil {
		return datasetVerbError(env, http.StatusBadRequest, "bad_root_path", err.Error())
	}
	src := datasetmeta.NewDirSource(root)

	digest, err := datasetmeta.ReadDigest(src)
	if err != nil {
		// An unsupported generation is a normal answer about an abnormal
		// dataset, not a host failure: the version string travels so the UI
		// can name what it refused instead of showing a bare error.
		var ufe *datasetmeta.UnsupportedFormatError
		if errors.As(err, &ufe) {
			return datasetVerbJSON(env, http.StatusUnprocessableEntity, map[string]any{
				"error":            "unsupported_format",
				"codebase_version": ufe.CodebaseVersion,
				"detail":           ufe.Error(),
			})
		}
		return datasetVerbError(env, http.StatusBadRequest, "read_failed", err.Error())
	}
	// A fingerprint that cannot be taken is not worth failing the digest over;
	// the hub simply has no staleness hint until the next refresh.
	fp, fpErr := datasetmeta.ReadFingerprint(src)
	out := map[string]any{"digest": digest}
	if fpErr == nil {
		out["fingerprint"] = fp
	}
	return datasetVerbJSON(env, http.StatusOK, out)
}

// handleHostDatasetEpisodes returns one window of a dataset's episodes table.
func (r *Runner) handleHostDatasetEpisodes(env *a2a.TunnelEnvelope) *a2a.TunnelResponseEnvelope {
	var p datasetEpisodesPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return datasetVerbError(env, http.StatusBadRequest, "bad_payload", err.Error())
	}
	root, err := cleanDatasetRoot(p.RootPath)
	if err != nil {
		return datasetVerbError(env, http.StatusBadRequest, "bad_root_path", err.Error())
	}
	page, err := datasetmeta.ReadEpisodes(datasetmeta.NewDirSource(root),
		datasetmeta.EpisodeRequest{Offset: p.Offset, Limit: p.Limit})
	if err != nil {
		var ufe *datasetmeta.UnsupportedFormatError
		if errors.As(err, &ufe) {
			return datasetVerbJSON(env, http.StatusUnprocessableEntity, map[string]any{
				"error":            "unsupported_format",
				"codebase_version": ufe.CodebaseVersion,
				"detail":           ufe.Error(),
			})
		}
		return datasetVerbError(env, http.StatusBadRequest, "read_failed", err.Error())
	}
	return datasetVerbJSON(env, http.StatusOK, page)
}

// handleHostDatasetSeries returns one episode's numeric channels, decimated.
//
// The only verb here that opens a data file. It stays a *host* read for the
// same reason the digest does: an episode is megabytes of parquet, the answer
// is kilobytes of decimated floats, and the difference is exactly what the
// data-ownership law says must not cross to the hub.
func (r *Runner) handleHostDatasetSeries(env *a2a.TunnelEnvelope) *a2a.TunnelResponseEnvelope {
	var p datasetSeriesPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil {
		return datasetVerbError(env, http.StatusBadRequest, "bad_payload", err.Error())
	}
	root, err := cleanDatasetRoot(p.RootPath)
	if err != nil {
		return datasetVerbError(env, http.StatusBadRequest, "bad_root_path", err.Error())
	}
	page, err := datasetmeta.ReadSeries(datasetmeta.NewDirSource(root), datasetmeta.SeriesRequest{
		Episode:   p.Episode,
		Features:  p.Features,
		MaxPoints: p.MaxPoints,
	})
	if err != nil {
		var ufe *datasetmeta.UnsupportedFormatError
		if errors.As(err, &ufe) {
			return datasetVerbJSON(env, http.StatusUnprocessableEntity, map[string]any{
				"error":            "unsupported_format",
				"codebase_version": ufe.CodebaseVersion,
				"detail":           ufe.Error(),
			})
		}
		return datasetVerbError(env, http.StatusBadRequest, "read_failed", err.Error())
	}
	return datasetVerbJSON(env, http.StatusOK, page)
}

// cleanDatasetRoot validates the root path a verb was handed.
//
// Absolute only: a relative path would resolve against the host-runner's
// working directory, which is an implementation detail no caller can see and
// which changes between a systemd unit and a shell. Refusing is clearer than
// resolving somewhere surprising.
func cleanDatasetRoot(p string) (string, error) {
	if p == "" {
		return "", errors.New("root_path is required")
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("root_path must be absolute")
	}
	return filepath.Clean(p), nil
}

func datasetVerbJSON(env *a2a.TunnelEnvelope, status int, body any) *a2a.TunnelResponseEnvelope {
	b, err := json.Marshal(body)
	if err != nil {
		b = []byte(`{"error":"encode_failed"}`)
		status = http.StatusInternalServerError
	}
	return &a2a.TunnelResponseEnvelope{
		ReqID:   env.ReqID,
		Status:  status,
		Headers: map[string]string{"Content-Type": "application/json"},
		BodyB64: base64.StdEncoding.EncodeToString(b),
	}
}

func datasetVerbError(env *a2a.TunnelEnvelope, status int, code, detail string) *a2a.TunnelResponseEnvelope {
	return datasetVerbJSON(env, status, map[string]any{"error": code, "detail": detail})
}
