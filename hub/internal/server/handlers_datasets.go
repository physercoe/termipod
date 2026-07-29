package server

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// Datasets (replay plan W1). The hub owns the row — name, location, folded
// digest — and the host owns the bytes. Two consequences shape this file:
//
//  1. The digest is never computed here. Registering or refreshing sends a
//     host verb and stores what comes back, so the hub never opens a parquet
//     file and never needs a dataset root to be reachable from it.
//  2. The episodes table is not stored at all. It is proxied per request,
//     windowed and capped, because a 50k-episode listing is bulk data and
//     bulk data does not live on the hub (blueprint §4).

// validDatasetSources are the roots a dataset can live on. Only "local" — a
// path on the host-runner's own filesystem — is servable in W1; the others are
// accepted so a row can be registered ahead of the SSH-forward wedge, and the
// digest/episodes paths say plainly that they cannot read them yet.
var validDatasetSources = map[string]struct{}{
	"local": {},
	"sftp":  {},
	"hf":    {},
}

// datasetVerbTimeout bounds a host round-trip. A v3.0 refold walks parquet
// metadata, so this is looser than a ping, but it is bounded: a wedged host
// must fail the request, not hold a hub connection open.
const datasetVerbTimeout = 60 * time.Second

type datasetIn struct {
	ProjectID string `json:"project_id"`
	HostID    string `json:"host_id,omitempty"`
	Name      string `json:"name,omitempty"`
	RootPath  string `json:"root_path"`
	Source    string `json:"source,omitempty"`
	EnvRef    string `json:"env_ref,omitempty"`
}

type datasetPatch struct {
	Name   *string `json:"name,omitempty"`
	EnvRef *string `json:"env_ref,omitempty"`
}

type datasetOut struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	HostID    string `json:"host_id,omitempty"`
	Name      string `json:"name"`
	RootPath  string `json:"root_path"`
	Source    string `json:"source"`
	Format    string `json:"format,omitempty"`
	EnvRef    string `json:"env_ref,omitempty"`

	Digest              json.RawMessage `json:"digest,omitempty"`
	DigestSchemaVersion int             `json:"digest_schema_version,omitempty"`
	DigestTS            string          `json:"digest_ts,omitempty"`
	Fingerprint         json.RawMessage `json:"fingerprint,omitempty"`

	RegisteredAt string `json:"registered_at"`
	UpdatedAt    string `json:"updated_at"`
}

const datasetCols = `d.id, d.project_id, COALESCE(d.host_id, ''), d.name, d.root_path,
	d.source, d.format, d.env_ref, d.digest_json, d.digest_schema_version,
	COALESCE(d.digest_ts, ''), d.fingerprint_json, d.registered_at, d.updated_at`

func scanDataset(sc interface{ Scan(...any) error }) (datasetOut, error) {
	var d datasetOut
	var digest, fingerprint string
	err := sc.Scan(&d.ID, &d.ProjectID, &d.HostID, &d.Name, &d.RootPath,
		&d.Source, &d.Format, &d.EnvRef, &digest, &d.DigestSchemaVersion,
		&d.DigestTS, &fingerprint, &d.RegisteredAt, &d.UpdatedAt)
	if err != nil {
		return d, err
	}
	// Stored as TEXT so an empty column is "" rather than SQL NULL; emitting
	// that as a JSON field would produce invalid JSON downstream.
	if digest != "" {
		d.Digest = json.RawMessage(digest)
	}
	if fingerprint != "" {
		d.Fingerprint = json.RawMessage(fingerprint)
	}
	return d, nil
}

// datasetInTeam loads a dataset, scoped through its project's team.
func (s *Server) datasetInTeam(ctx context.Context, team, id string) (datasetOut, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+datasetCols+`
		FROM datasets d
		JOIN projects p ON p.id = d.project_id
		WHERE d.id = ? AND p.team_id = ?`, id, team)
	return scanDataset(row)
}

func (s *Server) handleListDatasets(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	q := `SELECT ` + datasetCols + `
		FROM datasets d
		JOIN projects p ON p.id = d.project_id
		WHERE p.team_id = ?`
	args := []any{team}
	if project := r.URL.Query().Get("project"); project != "" {
		q += " AND d.project_id = ?"
		args = append(args, project)
	}
	if host := r.URL.Query().Get("host"); host != "" {
		q += " AND d.host_id = ?"
		args = append(args, host)
	}
	q += " ORDER BY d.registered_at DESC"

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	out := []datasetOut{}
	for rows.Next() {
		d, err := scanDataset(rows)
		if err != nil {
			s.writeDBErr(w, err)
			return
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateDataset registers a dataset root.
//
// Idempotent on (project, host, root): "Open in Replay" is a context-menu
// action on a tree row, so hitting it twice must select the dataset rather than
// mint a second row that then drifts from the first. A repeat returns 200 with
// the existing row; a fresh registration returns 201.
func (s *Server) handleCreateDataset(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in datasetIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if in.ProjectID == "" {
		writeErr(w, http.StatusBadRequest, "project_id required")
		return
	}
	in.RootPath = strings.TrimSpace(in.RootPath)
	if in.RootPath == "" {
		writeErr(w, http.StatusBadRequest, "root_path required")
		return
	}
	if in.Source == "" {
		in.Source = "local"
	}
	if _, ok := validDatasetSources[in.Source]; !ok {
		writeErr(w, http.StatusBadRequest, "invalid source")
		return
	}

	var projFound string
	err := s.db.QueryRowContext(r.Context(),
		`SELECT id FROM projects WHERE id = ? AND team_id = ?`,
		in.ProjectID, team).Scan(&projFound)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	if in.HostID != "" {
		var hostFound string
		err := s.db.QueryRowContext(r.Context(),
			`SELECT id FROM hosts WHERE id = ? AND team_id = ?`,
			in.HostID, team).Scan(&hostFound)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "host not found")
			return
		}
		if err != nil {
			s.writeDBErr(w, err)
			return
		}
	}

	// Existing identity wins before insert — checked rather than relying on
	// the unique index alone, so the response can carry the existing row.
	existing, err := s.datasetByIdentity(r.Context(), in.ProjectID, in.HostID, in.RootPath)
	if err == nil {
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		s.writeDBErr(w, err)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	id := NewID()
	name := in.Name
	if name == "" {
		name = datasetNameFromPath(in.RootPath)
	}
	var hostID any
	if in.HostID != "" {
		hostID = in.HostID
	}
	_, err = s.writeDB.ExecContext(r.Context(), `
		INSERT INTO datasets (id, project_id, host_id, name, root_path, source,
			env_ref, registered_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, in.ProjectID, hostID, name, in.RootPath, in.Source, in.EnvRef, now, now)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	out, err := s.datasetInTeam(r.Context(), team, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) datasetByIdentity(ctx context.Context, projectID, hostID, rootPath string) (datasetOut, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+datasetCols+`
		FROM datasets d
		WHERE d.project_id = ? AND COALESCE(d.host_id, '') = ? AND d.root_path = ?`,
		projectID, hostID, rootPath)
	return scanDataset(row)
}

// datasetNameFromPath gives an unnamed registration something readable: the
// last path segment, which for a LeRobot root is the dataset's own directory.
func datasetNameFromPath(p string) string {
	trimmed := strings.TrimRight(p, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 && i+1 < len(trimmed) {
		return trimmed[i+1:]
	}
	if trimmed == "" {
		return p
	}
	return trimmed
}

func (s *Server) handleGetDataset(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	out, err := s.datasetInTeam(r.Context(), team, chi.URLParam(r, "dataset"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUpdateDataset edits the fields a human owns. Location and digest are
// deliberately not patchable: moving a root is a re-registration, and a digest
// that did not come from a host read would be a fact nobody checked.
func (s *Server) handleUpdateDataset(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "dataset")
	if _, err := s.datasetInTeam(r.Context(), team, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "dataset not found")
			return
		}
		s.writeDBErr(w, err)
		return
	}
	var in datasetPatch
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *in.Name)
	}
	if in.EnvRef != nil {
		sets = append(sets, "env_ref = ?")
		args = append(args, *in.EnvRef)
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no updatable fields in body")
		return
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now().UTC().Format(time.RFC3339), id)
	if _, err := s.writeDB.ExecContext(r.Context(),
		`UPDATE datasets SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		s.writeDBErr(w, err)
		return
	}
	out, err := s.datasetInTeam(r.Context(), team, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteDataset removes the hub's index entry. It never touches the
// host: the dataset's bytes are not the hub's to delete, and a de-register that
// silently erased a researcher's recordings would be catastrophic and
// irreversible.
func (s *Server) handleDeleteDataset(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "dataset")
	if _, err := s.datasetInTeam(r.Context(), team, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "dataset not found")
			return
		}
		s.writeDBErr(w, err)
		return
	}
	if _, err := s.writeDB.ExecContext(r.Context(), `DELETE FROM datasets WHERE id = ?`, id); err != nil {
		s.writeDBErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRefreshDatasetDigest asks the owning host to re-read the root and
// stores the fold.
func (s *Server) handleRefreshDatasetDigest(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "dataset")
	ds, err := s.datasetInTeam(r.Context(), team, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	body, status, err := s.datasetHostVerb(r.Context(), ds, "host.dataset_digest", map[string]any{
		"root_path": ds.RootPath,
	})
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	if status != http.StatusOK {
		// Pass the host's own answer through — an unsupported codebase_version
		// names the version it refused, and flattening that to "failed" would
		// throw away the only actionable part.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
		return
	}

	var payload struct {
		Digest      json.RawMessage `json:"digest"`
		Fingerprint json.RawMessage `json:"fingerprint"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		writeErr(w, http.StatusBadGateway, "host returned an unreadable digest")
		return
	}
	var meta struct {
		Format        string `json:"format"`
		SchemaVersion int    `json:"schema_version"`
		EnvRef        string `json:"env_ref"`
	}
	_ = json.Unmarshal(payload.Digest, &meta)

	now := time.Now().UTC().Format(time.RFC3339)
	// env_ref is only ever filled in, never overwritten: the host derives it
	// from robot_type, but a human may have set something more specific, and a
	// refresh must not quietly undo that.
	_, err = s.writeDB.ExecContext(r.Context(), `
		UPDATE datasets
		SET digest_json = ?, digest_schema_version = ?, digest_ts = ?,
		    fingerprint_json = ?, format = ?, updated_at = ?,
		    env_ref = CASE WHEN env_ref = '' THEN ? ELSE env_ref END
		WHERE id = ?`,
		string(payload.Digest), meta.SchemaVersion, now,
		string(payload.Fingerprint), meta.Format, now, meta.EnvRef, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	out, err := s.datasetInTeam(r.Context(), team, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleGetDatasetEpisodes proxies a windowed episodes listing from the host.
func (s *Server) handleGetDatasetEpisodes(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	ds, err := s.datasetInTeam(r.Context(), team, chi.URLParam(r, "dataset"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	args := map[string]any{"root_path": ds.RootPath}
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeErr(w, http.StatusBadRequest, "invalid offset")
			return
		}
		args["offset"] = n
	}
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid limit")
			return
		}
		args["limit"] = n
	}
	body, status, err := s.datasetHostVerb(r.Context(), ds, "host.dataset_episodes", args)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// handleGetDatasetEpisodeSeries proxies one episode's decimated channels.
//
// Not stored, for the same reason the episodes table is not: this is bulk data
// derived from bytes the hub does not own. The host decimates to a point budget
// and the hub passes the answer through — so a 40,000-frame episode costs the
// same here as a 40-frame one.
func (s *Server) handleGetDatasetEpisodeSeries(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	ds, err := s.datasetInTeam(r.Context(), team, chi.URLParam(r, "dataset"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "dataset not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	episode, err := strconv.ParseInt(chi.URLParam(r, "episode"), 10, 64)
	if err != nil || episode < 0 {
		writeErr(w, http.StatusBadRequest, "invalid episode index")
		return
	}
	args := map[string]any{"root_path": ds.RootPath, "episode": episode}
	// Feature keys contain dots ("observation.state") but never commas, so a
	// comma list needs no escaping. Blank entries are dropped rather than
	// forwarded: "?features=" is how a UI spells "no filter", and passing an
	// empty key through would come back as a warning about a feature nobody
	// asked for.
	if v := r.URL.Query().Get("features"); v != "" {
		var keys []string
		for _, k := range strings.Split(v, ",") {
			if k = strings.TrimSpace(k); k != "" {
				keys = append(keys, k)
			}
		}
		if len(keys) > 0 {
			args["features"] = keys
		}
	}
	if v := r.URL.Query().Get("max_points"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid max_points")
			return
		}
		args["max_points"] = n
	}
	body, status, err := s.datasetHostVerb(r.Context(), ds, "host.dataset_series", args)
	if err != nil {
		writeErr(w, status, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// datasetHostVerb sends a dataset verb to the host owning the bytes and returns
// the host's raw body and status.
func (s *Server) datasetHostVerb(ctx context.Context, ds datasetOut, verb string, args map[string]any) ([]byte, int, error) {
	if ds.HostID == "" {
		return nil, http.StatusConflict, errors.New("dataset has no host; reading it requires one")
	}
	if ds.Source != "local" {
		// Honest refusal rather than a slow path that half-works: remote roots
		// ride the SSH-forward wedge, which has not landed.
		return nil, http.StatusNotImplemented,
			errors.New("only local dataset roots can be read today")
	}
	payload, err := json.Marshal(args)
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	callCtx, cancel := context.WithTimeout(ctx, datasetVerbTimeout)
	defer cancel()
	resp, err := s.tunnel.enqueueHostVerb(callCtx, ds.HostID, verb, payload)
	if err != nil {
		return nil, http.StatusGatewayTimeout, errors.New("host did not answer: " + err.Error())
	}
	body, err := base64.StdEncoding.DecodeString(resp.BodyB64)
	if err != nil {
		return nil, http.StatusBadGateway, errors.New("host returned an undecodable body")
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	return body, status, nil
}
