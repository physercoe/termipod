package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Environments (plan docs/plans/environments-and-embodiments.md, wedge E2).
// The registry E0's handles resolve into.
//
// Three rules from §1 shape every handler here:
//
//  1. Identity, not semantics. The row says which task/scene/version this is;
//     it never says what the task means. task_ref is a pointer plus a format
//     tag, success_desc is one human line, config_json and scene_refs_json are
//     opaque blobs the hub stores and never reads.
//  2. The handle is the identity. (family, env_id, version) is unique per team
//     and immutable: changed content is a NEW VERSION, not an edited row —
//     which is why two registrations of one handle claiming different content
//     hashes are a 409 rather than a silent merge.
//  3. Team-scoped always; project-scoped only optionally, and never for a real
//     site. A bench outlives any one project.

// maxEnvBodyBytes bounds a write. The opaque blobs are a randomization spec and
// a list of asset links — kilobytes. This cap is what keeps the registry from
// quietly becoming a byte store, which the hub-index/host-bytes law forbids.
const maxEnvBodyBytes = 64 << 10

// familyRealSite is the one family with a scope rule attached: a physical lab
// bench is shared, so it is team-scoped, and the plan makes that a review
// anchor rather than a convention.
const familyRealSite = "real-site"

// maxResolveRefs bounds one resolve call. A UI resolves the handles on a page
// of rows, not a corpus.
const maxResolveRefs = 200

type environmentIn struct {
	ProjectID     string          `json:"project_id,omitempty"`
	Family        string          `json:"family"`
	EnvID         string          `json:"env_id"`
	Version       string          `json:"version,omitempty"`
	ContentHash   string          `json:"content_hash,omitempty"`
	EmbodimentRef string          `json:"embodiment_ref,omitempty"`
	SceneRefsJSON json.RawMessage `json:"scene_refs,omitempty"`
	ConfigJSON    json.RawMessage `json:"config,omitempty"`
	TaskRef       string          `json:"task_ref,omitempty"`
	TaskFormat    string          `json:"task_format,omitempty"`
	SuccessDesc   string          `json:"success_desc,omitempty"`
	TwinOf        string          `json:"twin_of,omitempty"`
}

// environmentPatch edits what a human or a later host verb owns. The three
// identity fields are present ONLY so a caller who tries to change them gets
// told: dropping them silently (encoding/json's default for unknown fields)
// would let someone believe they had renamed an environment.
type environmentPatch struct {
	Family  *string `json:"family,omitempty"`
	EnvID   *string `json:"env_id,omitempty"`
	Version *string `json:"version,omitempty"`

	ProjectID     *string          `json:"project_id,omitempty"`
	ContentHash   *string          `json:"content_hash,omitempty"`
	EmbodimentRef *string          `json:"embodiment_ref,omitempty"`
	SceneRefsJSON *json.RawMessage `json:"scene_refs,omitempty"`
	ConfigJSON    *json.RawMessage `json:"config,omitempty"`
	TaskRef       *string          `json:"task_ref,omitempty"`
	TaskFormat    *string          `json:"task_format,omitempty"`
	SuccessDesc   *string          `json:"success_desc,omitempty"`
	TwinOf        *string          `json:"twin_of,omitempty"`
}

type environmentOut struct {
	ID        string `json:"id"`
	TeamID    string `json:"team_id"`
	ProjectID string `json:"project_id,omitempty"`
	Family    string `json:"family"`
	EnvID     string `json:"env_id"`
	Version   string `json:"version,omitempty"`
	// EnvRef is the handle this row answers to, assembled from the three parts
	// above. Returned rather than left to the client so every consumer builds
	// it the one way — a chip that formatted it differently would compare
	// unequal to the stored handle it came from.
	EnvRef string `json:"env_ref"`

	ContentHash   string          `json:"content_hash,omitempty"`
	EmbodimentRef string          `json:"embodiment_ref,omitempty"`
	SceneRefs     json.RawMessage `json:"scene_refs,omitempty"`
	Config        json.RawMessage `json:"config,omitempty"`
	TaskRef       string          `json:"task_ref,omitempty"`
	TaskFormat    string          `json:"task_format,omitempty"`
	SuccessDesc   string          `json:"success_desc,omitempty"`
	TwinOf        string          `json:"twin_of,omitempty"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const environmentCols = `e.id, e.team_id, COALESCE(e.project_id, ''), e.family,
	e.env_id, e.version, e.content_hash, e.embodiment_ref, e.scene_refs_json,
	e.config_json, e.task_ref, e.task_format, e.success_desc,
	COALESCE(e.twin_of, ''), e.created_at, e.updated_at`

func scanEnvironment(sc interface{ Scan(...any) error }) (environmentOut, error) {
	var e environmentOut
	var sceneRefs, config string
	err := sc.Scan(&e.ID, &e.TeamID, &e.ProjectID, &e.Family, &e.EnvID,
		&e.Version, &e.ContentHash, &e.EmbodimentRef, &sceneRefs, &config,
		&e.TaskRef, &e.TaskFormat, &e.SuccessDesc, &e.TwinOf,
		&e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return e, err
	}
	// Stored as TEXT so an unset blob is "" rather than SQL NULL; emitting that
	// as a JSON field would produce invalid JSON downstream (same shape as
	// datasets' digest column).
	if sceneRefs != "" {
		e.SceneRefs = json.RawMessage(sceneRefs)
	}
	if config != "" {
		e.Config = json.RawMessage(config)
	}
	e.EnvRef = formatEnvRef(envRefParts{Family: e.Family, EnvID: e.EnvID, Version: e.Version})
	return e, nil
}

func (s *Server) environmentInTeam(ctx context.Context, team, id string) (environmentOut, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+environmentCols+`
		FROM environments e
		WHERE e.id = ? AND e.team_id = ?`, id, team)
	return scanEnvironment(row)
}

func (s *Server) environmentByHandle(ctx context.Context, team string, p envRefParts) (environmentOut, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT `+environmentCols+`
		FROM environments e
		WHERE e.team_id = ? AND e.family = ? AND e.env_id = ? AND e.version = ?`,
		team, p.Family, p.EnvID, p.Version)
	return scanEnvironment(row)
}

// handleListEnvironments is GET /v1/teams/{team}/environments.
//
// `?project=P` means "everything P can reference": P's own rows plus the
// team-scoped ones (which is where every real site lives). Filtering to
// project_id = P alone would hide exactly the benches a project runs on.
func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	q := `SELECT ` + environmentCols + ` FROM environments e WHERE e.team_id = ?`
	args := []any{team}
	if project := r.URL.Query().Get("project"); project != "" {
		q += " AND (e.project_id = ? OR e.project_id IS NULL)"
		args = append(args, project)
	}
	if family := r.URL.Query().Get("family"); family != "" {
		q += " AND e.family = ?"
		args = append(args, family)
	}
	// Sim families first, sites last, then by handle — the rail's order (plan
	// §2), decided here so two clients cannot disagree about it. The handle
	// triple is unique per team, so no further tiebreak can ever fire. The
	// family name is bound rather than interpolated: it is a constant today,
	// and a query built by concatenation stays wrong even when its inputs are
	// safe.
	q += ` ORDER BY CASE WHEN e.family = ? THEN 1 ELSE 0 END,
		e.family, e.env_id, e.version`
	args = append(args, familyRealSite)

	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	defer rows.Close()
	out := []environmentOut{}
	for rows.Next() {
		e, err := scanEnvironment(rows)
		if err != nil {
			s.writeDBErr(w, err)
			return
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateEnvironment is POST /v1/teams/{team}/environments — idempotent on
// the handle.
//
// Re-registering an existing handle returns the existing row (200) rather than
// editing it: the same handle IS the same environment, and E3's recalibration
// story says changed content bumps the version. The one exception is a body
// claiming a DIFFERENT content_hash for a handle that already has one — that is
// the env-drift §0 warns about, so it fails loudly instead of picking a winner.
//
// Register never edits, and that is the whole rule: on a hit the rest of the
// body is NOT applied, because PATCH is the only writer of the non-identity
// fields. One writer per field is what keeps a curated success_desc from being
// flattened by a script that re-registers with a sparse body — and the 200
// carries the stored row, so a caller can see what it got.
func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in environmentIn
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEnvBodyBytes)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid or oversized JSON body")
		return
	}
	in.Family = strings.TrimSpace(in.Family)
	in.EnvID = strings.TrimSpace(in.EnvID)
	in.Version = strings.TrimSpace(in.Version)

	if !validEnvHandlePart(in.Family, false) {
		writeErr(w, http.StatusBadRequest,
			"family is required, must not contain ':' or '@', and is capped at 128 chars")
		return
	}
	if !validEnvHandlePart(in.EnvID, true) {
		writeErr(w, http.StatusBadRequest,
			"env_id is required, must not contain '@', and is capped at 128 chars")
		return
	}
	if in.Version != "" && !validEnvHandlePart(in.Version, true) {
		writeErr(w, http.StatusBadRequest, "version must not contain '@' and is capped at 128 chars")
		return
	}
	if code, msg := s.checkEnvScope(r.Context(), team, in.Family, in.ProjectID); code != 0 {
		writeErr(w, code, msg)
		return
	}
	if in.TwinOf != "" {
		if code, msg := s.checkEnvTwin(r.Context(), team, "", in.TwinOf); code != 0 {
			writeErr(w, code, msg)
			return
		}
	}

	handle := envRefParts{Family: in.Family, EnvID: in.EnvID, Version: in.Version}
	existing, err := s.environmentByHandle(r.Context(), team, handle)
	if err == nil {
		if in.ContentHash != "" && existing.ContentHash != "" && in.ContentHash != existing.ContentHash {
			writeErr(w, http.StatusConflict,
				"environment "+existing.EnvRef+" is registered with a different content_hash; "+
					"bump the version instead of redefining a handle")
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		s.writeDBErr(w, err)
		return
	}

	id := NewID()
	now := NowUTC()
	if _, err := s.writeDB.ExecContext(r.Context(), `
		INSERT INTO environments (id, team_id, project_id, family, env_id, version,
			content_hash, embodiment_ref, scene_refs_json, config_json,
			task_ref, task_format, success_desc, twin_of, created_at, updated_at)
		VALUES (?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?)`,
		id, team, in.ProjectID, in.Family, in.EnvID, in.Version,
		in.ContentHash, in.EmbodimentRef, string(in.SceneRefsJSON), string(in.ConfigJSON),
		in.TaskRef, in.TaskFormat, in.SuccessDesc, in.TwinOf, now, now); err != nil {
		s.writeDBErr(w, err)
		return
	}
	s.recordAudit(r.Context(), team, "environment.create", "environment", id,
		"register environment "+formatEnvRef(handle),
		map[string]any{"family": in.Family, "env_id": in.EnvID, "version": in.Version})

	out, err := s.environmentInTeam(r.Context(), team, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) handleGetEnvironment(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	out, err := s.environmentInTeam(r.Context(), team, chi.URLParam(r, "env"))
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "environment not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUpdateEnvironment is PATCH /v1/teams/{team}/environments/{env} — the
// non-identity fields only. The handle is immutable: rows elsewhere already
// point at it as an opaque string, and moving it would silently unresolve every
// one of them.
func (s *Server) handleUpdateEnvironment(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "env")
	current, err := s.environmentInTeam(r.Context(), team, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "environment not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	var in environmentPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEnvBodyBytes)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid or oversized JSON body")
		return
	}
	if in.Family != nil || in.EnvID != nil || in.Version != nil {
		writeErr(w, http.StatusBadRequest,
			"family, env_id and version are the environment's identity and cannot be patched; "+
				"register a new version instead")
		return
	}

	sets := []string{}
	args := []any{}
	add := func(col string, v any) { sets = append(sets, col+" = ?"); args = append(args, v) }

	if in.ProjectID != nil {
		if code, msg := s.checkEnvScope(r.Context(), team, current.Family, *in.ProjectID); code != 0 {
			writeErr(w, code, msg)
			return
		}
		add("project_id", nullIfEmpty(*in.ProjectID))
	}
	if in.ContentHash != nil {
		// Filling in a hash nobody knew at registration is an edit; changing —
		// or CLEARING — a known one means the content moved under a handle
		// that promised not to. Clearing must be refused like any change, or
		// "" followed by a new hash would redefine the handle in two PATCHes,
		// which is exactly what the drift 409 exists to prevent.
		if current.ContentHash != "" && *in.ContentHash != current.ContentHash {
			writeErr(w, http.StatusConflict,
				"content_hash already recorded for "+current.EnvRef+
					"; register a new version rather than redefining this one")
			return
		}
		add("content_hash", *in.ContentHash)
	}
	if in.EmbodimentRef != nil {
		add("embodiment_ref", *in.EmbodimentRef)
	}
	if in.SceneRefsJSON != nil {
		add("scene_refs_json", string(*in.SceneRefsJSON))
	}
	if in.ConfigJSON != nil {
		add("config_json", string(*in.ConfigJSON))
	}
	if in.TaskRef != nil {
		add("task_ref", *in.TaskRef)
	}
	if in.TaskFormat != nil {
		add("task_format", *in.TaskFormat)
	}
	if in.SuccessDesc != nil {
		add("success_desc", *in.SuccessDesc)
	}
	if in.TwinOf != nil {
		if code, msg := s.checkEnvTwin(r.Context(), team, id, *in.TwinOf); code != 0 {
			writeErr(w, code, msg)
			return
		}
		add("twin_of", nullIfEmpty(*in.TwinOf))
	}
	if len(sets) == 0 {
		writeErr(w, http.StatusBadRequest, "no updatable fields in body")
		return
	}
	add("updated_at", NowUTC())
	args = append(args, id, team)
	if _, err := s.writeDB.ExecContext(r.Context(),
		`UPDATE environments SET `+joinComma(sets)+` WHERE id = ? AND team_id = ?`,
		args...); err != nil {
		s.writeDBErr(w, err)
		return
	}
	s.recordAudit(r.Context(), team, "environment.update", "environment", id,
		"update environment "+current.EnvRef, map[string]any{"fields": len(sets) - 1})

	out, err := s.environmentInTeam(r.Context(), team, id)
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDeleteEnvironment removes the registry row.
//
// The env_ref strings on runs and datasets are NOT foreign keys, so they are
// left alone and simply become unresolved again — which is the designed
// behaviour, not a dangling reference: E0 accumulated those handles before any
// row existed, and "unresolved" is a state the UI already has to render. Twin
// edges pointing here are nulled by the FK.
func (s *Server) handleDeleteEnvironment(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	id := chi.URLParam(r, "env")
	current, err := s.environmentInTeam(r.Context(), team, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "environment not found")
		return
	}
	if err != nil {
		s.writeDBErr(w, err)
		return
	}
	// The twin FK only fires with foreign_keys ON; clearing it explicitly makes
	// the outcome the same either way rather than leaving a pointer to a row
	// that no longer exists.
	if _, err := s.writeDB.ExecContext(r.Context(),
		`UPDATE environments SET twin_of = NULL WHERE twin_of = ?`, id); err != nil {
		s.writeDBErr(w, err)
		return
	}
	if _, err := s.writeDB.ExecContext(r.Context(),
		`DELETE FROM environments WHERE id = ? AND team_id = ?`, id, team); err != nil {
		s.writeDBErr(w, err)
		return
	}
	s.recordAudit(r.Context(), team, "environment.delete", "environment", id,
		"delete environment "+current.EnvRef, nil)
	w.WriteHeader(http.StatusNoContent)
}

// envResolution is one answer in a resolve response. Status is "resolved" or
// "unresolved"; an unresolved answer always carries a reason, because "we could
// not find it" and "that is not a handle" send a reader to different fixes.
type envResolution struct {
	EnvRef      string          `json:"env_ref"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason,omitempty"`
	Environment *environmentOut `json:"environment,omitempty"`
}

// handleResolveEnvironments is GET
// /v1/teams/{team}/environments/resolve?env_ref=…&env_ref=… — the E2 half of
// E0's promise.
//
// One answer per input ref, in input order, so a caller zipping the results
// back onto its rows cannot misalign them. Unresolved is a normal answer with a
// 200: an old row carrying a handle nobody registered is exactly what E0 said
// would accumulate, and failing the request would make the UI's "unresolved"
// chip unreachable.
func (s *Server) handleResolveEnvironments(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	refs := r.URL.Query()["env_ref"]
	if len(refs) == 0 {
		writeErr(w, http.StatusBadRequest, "at least one env_ref query parameter is required")
		return
	}
	if len(refs) > maxResolveRefs {
		writeErr(w, http.StatusBadRequest, "too many env_ref parameters (max 200)")
		return
	}
	// One DB round trip per DISTINCT ref: a page of 100 episodes usually shares
	// one handle, and re-asking per row would turn a chip into N queries.
	seen := map[string]envResolution{}
	out := make([]envResolution, 0, len(refs))
	for _, ref := range refs {
		if cached, ok := seen[ref]; ok {
			out = append(out, cached)
			continue
		}
		res := envResolution{EnvRef: ref, Status: "unresolved"}
		p, ok := parseEnvRef(ref)
		if !ok {
			res.Reason = "malformed"
		} else {
			row, err := s.environmentByHandle(r.Context(), team, p)
			switch {
			case err == nil:
				env := row
				res.Status = "resolved"
				res.Environment = &env
			case errors.Is(err, sql.ErrNoRows):
				res.Reason = "no_match"
			default:
				s.writeDBErr(w, err)
				return
			}
		}
		seen[ref] = res
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// checkEnvScope enforces the plan's scope-honesty anchor. A real site is
// team-scoped, full stop: two projects sharing a bench must share its
// calibration history and its twin edge, and a project-scoped interim is the
// migration this decision exists to avoid.
func (s *Server) checkEnvScope(ctx context.Context, team, family, projectID string) (int, string) {
	if family == familyRealSite && projectID != "" {
		return http.StatusBadRequest,
			"a real-site environment is team-scoped: a bench outlives any one project, " +
				"so project_id must be empty"
	}
	if projectID == "" {
		return 0, ""
	}
	var found string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM projects WHERE id = ? AND team_id = ?`, projectID, team).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusBadRequest, "project not found in team"
	}
	if err != nil {
		return http.StatusInternalServerError, "project lookup failed"
	}
	return 0, ""
}

// checkEnvTwin validates a twin target: same team, and never the row itself.
// A self-twin would read as "this scene is its own sim counterpart", which is
// not a claim the model can express.
func (s *Server) checkEnvTwin(ctx context.Context, team, selfID, twinID string) (int, string) {
	if twinID == "" {
		return 0, ""
	}
	if selfID != "" && twinID == selfID {
		return http.StatusBadRequest, "twin_of cannot point at the environment itself"
	}
	var found string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM environments WHERE id = ? AND team_id = ?`, twinID, team).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return http.StatusBadRequest, "twin_of environment not found in team"
	}
	if err != nil {
		return http.StatusInternalServerError, "twin lookup failed"
	}
	return 0, ""
}
