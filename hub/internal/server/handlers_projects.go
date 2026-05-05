package server

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// projectIn is the request payload for create + (subset for) update.
// All new fields are optional; minimal {"name": "..."} payloads still work.
type projectIn struct {
	Name      string `json:"name"`
	DocsRoot  string `json:"docs_root,omitempty"`
	ConfigYML string `json:"config_yaml,omitempty"`

	// Blueprint §6.1 additions (P0.1).
	Goal                string          `json:"goal,omitempty"`
	Kind                string          `json:"kind,omitempty"` // 'goal' | 'standing'
	ParentProjectID     string          `json:"parent_project_id,omitempty"`
	TemplateID          string          `json:"template_id,omitempty"`
	ParametersJSON      json.RawMessage `json:"parameters_json,omitempty"`
	IsTemplate          bool            `json:"is_template,omitempty"`
	BudgetCents         *int64          `json:"budget_cents,omitempty"`
	PolicyOverridesJSON json.RawMessage `json:"policy_overrides_json,omitempty"`
	StewardAgentID      string          `json:"steward_agent_id,omitempty"`
	OnCreateTemplateID  string          `json:"on_create_template_id,omitempty"`
}

type projectOut struct {
	ID         string  `json:"id"`
	TeamID     string  `json:"team_id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	DocsRoot   string  `json:"docs_root,omitempty"`
	ConfigYAML string  `json:"config_yaml,omitempty"`
	CreatedAt  string  `json:"created_at"`
	ArchivedAt *string `json:"archived_at,omitempty"`

	Goal                string          `json:"goal,omitempty"`
	Kind                string          `json:"kind,omitempty"`
	ParentProjectID     string          `json:"parent_project_id,omitempty"`
	TemplateID          string          `json:"template_id,omitempty"`
	ParametersJSON      json.RawMessage `json:"parameters_json,omitempty"`
	IsTemplate          bool            `json:"is_template,omitempty"`
	BudgetCents         *int64          `json:"budget_cents,omitempty"`
	PolicyOverridesJSON json.RawMessage `json:"policy_overrides_json,omitempty"`
	StewardAgentID      string          `json:"steward_agent_id,omitempty"`
	OnCreateTemplateID  string          `json:"on_create_template_id,omitempty"`

	// OverviewWidget is the resolved pluggable hero kind for Project
	// Detail → Overview (A+B chassis, IA §6.2). Always populated on
	// the wire: unknown / missing template → overviewWidgetDefault.
	OverviewWidget string `json:"overview_widget"`

	// Phase + Phases + PhaseHistory carry lifecycle state (D1).
	// Phase is empty for legacy / lifecycle-disabled projects; mobile
	// renders the pre-lifecycle Overview when Phase is empty.
	Phase        string            `json:"phase,omitempty"`
	Phases       []string          `json:"phases,omitempty"`
	PhaseHistory []phaseTransition `json:"phase_history,omitempty"`
}

// resolveOverviewWidget returns the hero widget kind to surface on the
// project read payload. Projects with no template_id get the default;
// projects with a template_id look up the YAML-declared overview_widget
// and fall back to the default if the template isn't found or doesn't
// declare one. Unknown values are normalized away by loadProjectTemplates.
//
// Reads the template set off disk on each call. Cardinality is small
// (< 20 docs in practice) and the bulk handlers (list/get) are low-QPS
// compared to run metrics — caching is not worth the staleness risk.
func (s *Server) resolveOverviewWidget(templateID string) string {
	if templateID == "" {
		return overviewWidgetDefault
	}
	docs, err := loadProjectTemplates(s.cfg.DataRoot)
	if err != nil {
		// Fall back to default rather than 500'ing the whole project read;
		// the UI still renders, just without the template-specific hero.
		s.log.Warn("loadProjectTemplates failed during overview_widget resolve",
			"err", err, "template", templateID)
		return overviewWidgetDefault
	}
	for _, d := range docs {
		if d.Name == templateID {
			return normalizeOverviewWidget(d.OverviewWidget)
		}
	}
	return overviewWidgetDefault
}

// validKind returns the normalized project kind or empty string if invalid.
// SQLite can't add CHECK constraints via ALTER TABLE, so we enforce here.
func validKind(k string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "", "goal":
		return "goal", true
	case "standing":
		return "standing", true
	default:
		return "", false
	}
}

// nullStringIfEmpty returns a NullString valid iff s is non-empty. Keeps nullable
// TEXT columns genuinely NULL instead of empty strings (so partial indexes
// like idx_projects_template WHERE template_id IS NOT NULL behave correctly).
// Named to avoid collision with audit.go's nullStringIfEmpty(string) any.
func nullStringIfEmpty(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func nullRawJSON(b json.RawMessage) sql.NullString {
	if len(b) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(b), Valid: true}
}

func nullInt64(p *int64) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *p, Valid: true}
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	var in projectIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	kind, ok := validKind(in.Kind)
	if !ok {
		writeErr(w, http.StatusBadRequest, "kind must be 'goal' or 'standing'")
		return
	}
	// Enforce max tree depth = 2 (Blueprint §6.1 / IA §6.2 W5). A sub-project
	// may only parent off a top-level project: if the proposed parent already
	// has its own parent, reject the create. This keeps mobile UI flat enough
	// to show inline (12px indent × 2 levels, thin left rail) without collapse.
	if in.ParentProjectID != "" {
		var parentTeam string
		var parentParent sql.NullString
		err := s.db.QueryRowContext(r.Context(),
			`SELECT team_id, parent_project_id FROM projects
			 WHERE id = ?`, in.ParentProjectID).Scan(&parentTeam, &parentParent)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusBadRequest, "parent_project_id does not exist")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if parentTeam != team {
			writeErr(w, http.StatusBadRequest,
				"parent_project_id belongs to a different team")
			return
		}
		if parentParent.Valid && parentParent.String != "" {
			writeErr(w, http.StatusBadRequest,
				"max sub-project depth is 2: parent is already a sub-project")
			return
		}
	}
	id := NewID()
	now := NowUTC()
	isTpl := 0
	if in.IsTemplate {
		isTpl = 1
	}

	// Hydrate the initial phase from the template's phase set (D1).
	// Concrete projects (is_template=0) created from a phase-declaring
	// template land on phases[0]; everything else stays NULL so the UI
	// renders the legacy Overview. Per reference/project-phase-schema.md
	// §7.2 we also persist the first transition into phase_history and
	// emit a project.phase_set audit row.
	var initPhase string
	var initPhaseHistory sql.NullString
	if !in.IsTemplate {
		if phases := s.templatePhases(in.TemplateID); len(phases) > 0 {
			initPhase = phases[0]
			doc := phaseHistoryDoc{Transitions: []phaseTransition{{
				From: "", To: initPhase, At: now, ByActor: "system",
			}}}
			if b, err := json.Marshal(doc); err == nil {
				initPhaseHistory = sql.NullString{String: string(b), Valid: true}
			}
		}
	}

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO projects (
			id, team_id, name, config_yaml, docs_root, created_at,
			goal, kind, parent_project_id, template_id, parameters_json,
			is_template, budget_cents, policy_overrides_json,
			steward_agent_id, on_create_template_id,
			phase, phase_history
		) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?,
			?, ?, ?, ?, ?,
			?, ?, ?,
			?, ?,
			?, ?)`,
		id, team, in.Name, in.ConfigYML, in.DocsRoot, now,
		nullStringIfEmpty(in.Goal), kind, nullStringIfEmpty(in.ParentProjectID),
		nullStringIfEmpty(in.TemplateID), nullRawJSON(in.ParametersJSON),
		isTpl, nullInt64(in.BudgetCents), nullRawJSON(in.PolicyOverridesJSON),
		nullStringIfEmpty(in.StewardAgentID), nullStringIfEmpty(in.OnCreateTemplateID),
		nullStringIfEmpty(initPhase), initPhaseHistory,
	)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	s.recordAudit(r.Context(), team, "project.create", "project", id,
		"create project "+in.Name,
		map[string]any{"kind": kind, "is_template": in.IsTemplate})
	if initPhase != "" {
		s.recordAudit(r.Context(), team, "project.phase_set", "project", id,
			"set initial phase "+initPhase,
			map[string]any{"phase": initPhase, "by_template": in.TemplateID})
	}
	out := projectOut{
		ID: id, TeamID: team, Name: in.Name, Status: "active",
		DocsRoot: in.DocsRoot, ConfigYAML: in.ConfigYML, CreatedAt: now,
		Goal: in.Goal, Kind: kind,
		ParentProjectID: in.ParentProjectID, TemplateID: in.TemplateID,
		ParametersJSON: in.ParametersJSON, IsTemplate: in.IsTemplate,
		BudgetCents: in.BudgetCents, PolicyOverridesJSON: in.PolicyOverridesJSON,
		StewardAgentID: in.StewardAgentID, OnCreateTemplateID: in.OnCreateTemplateID,
		OverviewWidget: s.resolveOverviewWidget(in.TemplateID),
		Phase:          initPhase,
		Phases:         s.templatePhases(in.TemplateID),
	}
	if initPhase != "" {
		out.PhaseHistory = []phaseTransition{{
			From: "", To: initPhase, At: now, ByActor: "system",
		}}
	}
	writeJSON(w, http.StatusCreated, out)
}

// scanProjectRow reads one row selected via projectSelectCols into p.
// Accepts both *sql.Row and *sql.Rows via a shared Scan interface.
func scanProjectRow(sc interface {
	Scan(dest ...any) error
}, p *projectOut) error {
	var archived, goal, parentID, tplID, paramsJSON sql.NullString
	var policyJSON, stewardID, onCreateTplID, kind sql.NullString
	var phase, phaseHistory sql.NullString
	var budget sql.NullInt64
	var isTpl int64
	if err := sc.Scan(
		&p.ID, &p.TeamID, &p.Name, &p.Status,
		&p.DocsRoot, &p.ConfigYAML, &p.CreatedAt, &archived,
		&goal, &kind, &parentID, &tplID, &paramsJSON,
		&isTpl, &budget, &policyJSON, &stewardID, &onCreateTplID,
		&phase, &phaseHistory,
	); err != nil {
		return err
	}
	if archived.Valid {
		p.ArchivedAt = &archived.String
	}
	p.Goal = goal.String
	if kind.Valid {
		p.Kind = kind.String
	} else {
		p.Kind = "goal"
	}
	p.ParentProjectID = parentID.String
	p.TemplateID = tplID.String
	if paramsJSON.Valid {
		p.ParametersJSON = json.RawMessage(paramsJSON.String)
	}
	p.IsTemplate = isTpl != 0
	if budget.Valid {
		v := budget.Int64
		p.BudgetCents = &v
	}
	if policyJSON.Valid {
		p.PolicyOverridesJSON = json.RawMessage(policyJSON.String)
	}
	p.StewardAgentID = stewardID.String
	p.OnCreateTemplateID = onCreateTplID.String
	if phase.Valid {
		p.Phase = phase.String
	}
	if phaseHistory.Valid && phaseHistory.String != "" {
		var doc phaseHistoryDoc
		if err := json.Unmarshal([]byte(phaseHistory.String), &doc); err == nil {
			p.PhaseHistory = doc.Transitions
		}
	}
	return nil
}

const projectSelectCols = `
	id, team_id, name, status,
	COALESCE(docs_root, ''), COALESCE(config_yaml, ''),
	created_at, archived_at,
	goal, kind, parent_project_id, template_id, parameters_json,
	is_template, budget_cents, policy_overrides_json,
	steward_agent_id, on_create_template_id,
	phase, phase_history`

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")

	// is_template=true|false filters to rows usable as templates, or only
	// concrete projects. Absent = no filter (existing behavior).
	query := `SELECT ` + projectSelectCols + ` FROM projects WHERE team_id = ?`
	args := []any{team}
	if v := r.URL.Query().Get("is_template"); v != "" {
		switch v {
		case "true", "1":
			query += ` AND is_template = 1`
		case "false", "0":
			query += ` AND (is_template IS NULL OR is_template = 0)`
		default:
			writeErr(w, http.StatusBadRequest, "is_template must be true|false")
			return
		}
	}
	query += ` ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(r.Context(), query, args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []projectOut{}
	for rows.Next() {
		var p projectOut
		if err := scanProjectRow(rows, &p); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		p.OverviewWidget = s.resolveOverviewWidget(p.TemplateID)
		p.Phases = s.templatePhases(p.TemplateID)
		out = append(out, p)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	proj := chi.URLParam(r, "project")
	var p projectOut
	row := s.db.QueryRowContext(r.Context(), `
		SELECT `+projectSelectCols+`
		FROM projects WHERE team_id = ? AND id = ?`, team, proj)
	if err := scanProjectRow(row, &p); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, "project not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	p.OverviewWidget = s.resolveOverviewWidget(p.TemplateID)
	p.Phases = s.templatePhases(p.TemplateID)
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleArchiveProject(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	proj := chi.URLParam(r, "project")
	res, err := s.db.ExecContext(r.Context(), `
		UPDATE projects SET status='archived', archived_at=?
		WHERE team_id = ? AND id = ? AND status != 'archived'`,
		NowUTC(), team, proj)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "project not found or already archived")
		return
	}
	s.recordAudit(r.Context(), team, "project.archive", "project", proj,
		"archive project", nil)
	w.WriteHeader(http.StatusNoContent)
}

// projectPatch is the PATCH payload — only mutable fields. Create-time fields
// (kind, template_id, parent_project_id, is_template) are intentionally
// omitted so they can't be mutated after creation.
type projectPatch struct {
	Goal                *string          `json:"goal,omitempty"`
	ParametersJSON      *json.RawMessage `json:"parameters_json,omitempty"`
	BudgetCents         *int64           `json:"budget_cents,omitempty"`
	PolicyOverridesJSON *json.RawMessage `json:"policy_overrides_json,omitempty"`
	StewardAgentID      *string          `json:"steward_agent_id,omitempty"`
	OnCreateTemplateID  *string          `json:"on_create_template_id,omitempty"`
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	team := chi.URLParam(r, "team")
	proj := chi.URLParam(r, "project")
	var in projectPatch
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}

	sets := make([]string, 0, 6)
	args := make([]any, 0, 8)
	if in.Goal != nil {
		sets = append(sets, "goal = ?")
		args = append(args, nullStringIfEmpty(*in.Goal))
	}
	if in.ParametersJSON != nil {
		sets = append(sets, "parameters_json = ?")
		args = append(args, nullRawJSON(*in.ParametersJSON))
	}
	if in.BudgetCents != nil {
		sets = append(sets, "budget_cents = ?")
		args = append(args, *in.BudgetCents)
	}
	if in.PolicyOverridesJSON != nil {
		sets = append(sets, "policy_overrides_json = ?")
		args = append(args, nullRawJSON(*in.PolicyOverridesJSON))
	}
	if in.StewardAgentID != nil {
		sets = append(sets, "steward_agent_id = ?")
		args = append(args, nullStringIfEmpty(*in.StewardAgentID))
	}
	if in.OnCreateTemplateID != nil {
		sets = append(sets, "on_create_template_id = ?")
		args = append(args, nullStringIfEmpty(*in.OnCreateTemplateID))
	}
	if len(sets) == 0 {
		// Nothing to update — fall through to returning the current row so
		// clients can treat PATCH as idempotent-read.
		s.handleGetProject(w, r)
		return
	}
	args = append(args, team, proj)
	res, err := s.db.ExecContext(r.Context(),
		"UPDATE projects SET "+strings.Join(sets, ", ")+
			" WHERE team_id = ? AND id = ?", args...)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeErr(w, http.StatusNotFound, "project not found")
		return
	}
	changed := make([]string, 0, len(sets))
	for _, s := range sets {
		if i := strings.IndexByte(s, ' '); i > 0 {
			changed = append(changed, s[:i])
		}
	}
	s.recordAudit(r.Context(), team, "project.update", "project", proj,
		"update project", map[string]any{"fields": changed})
	s.handleGetProject(w, r)
}
