package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/termipod/hub/internal/agentfamilies"
	"github.com/termipod/hub/internal/auth"
	"github.com/termipod/hub/internal/buildinfo"
	"github.com/termipod/hub/internal/envelope"
	"github.com/termipod/hub/internal/otlptrace"
	"github.com/termipod/hub/internal/pricing"
)

const APIVersion = "v1"

// ServerVersion mirrors buildinfo.Version (single source of truth, kept
// in sync with pubspec.yaml via `make bump`). Exported here for callers
// that already import "internal/server" — mcp.go's tools/list payload
// and handleInfo both read this.
var ServerVersion = buildinfo.Version

type Config struct {
	Listen   string // e.g. 127.0.0.1:8443
	DBPath   string // e.g. ~/hub/hub.db
	DataRoot string // e.g. ~/hub
	// PublicURL is the externally reachable base URL clients use to hit
	// this hub, e.g. "https://hub.example.com". The A2A card directory
	// uses it to rewrite NAT'd host-runner URLs to the hub relay. Empty
	// means "derive from the request Host header" — fine for single-host
	// dev, less so when the directory is scraped by off-box peers.
	PublicURL string
	// OTLPEndpoint, when set, turns on the operator trace export (ADR-038
	// §4): the hub projects each session's turn index + tool/error events
	// into OTLP traces and ships them to this OTLP/HTTP base URL (e.g.
	// "http://localhost:4318") on an idle/terminal batch cadence. Empty =
	// disabled (the default — a backend buys query/viz UX, not storage).
	OTLPEndpoint string
	// OTLPServiceName overrides the resource service.name on exported
	// spans (default "termipod-hub").
	OTLPServiceName string
	Logger          *slog.Logger
}

type Server struct {
	cfg     Config
	db      *sql.DB // control reader pool — uncapped; reads + tests
	writeDB *sql.DB // control writer pool — SetMaxOpenConns(1); all writes, see New()
	// Store separation + per-team sharding (ADR-045 D2 — the event log + the
	// derived digest are separate data classes from the control plane, and both
	// are sharded per team). stores is the per-team registry: each team's
	// events.db / digest.db live under dataRoot/teams/<team>/ with their own
	// reader + single-writer pools, so the high-volume firehose and its fold
	// can't contend with control-plane CRUD, each other, or other teams on
	// SQLite's per-file write lock. All event/digest access routes through the
	// team-keyed accessors in store_route.go; hub.db (s.db / s.writeDB) stays the
	// global control plane.
	stores *teamStores
	// agentTeam caches the immutable (agent id → team) binding used to route an
	// agent-keyed event/digest access to its shard (store_route.go, ADR-045 P2).
	agentTeam     sync.Map
	router        chi.Router
	log           *slog.Logger
	bus           *eventBus
	sched         *Scheduler
	policy        *policyStore
	escalator     *Escalator
	tunnel        *TunnelManager
	agentFamilies *agentfamilies.Registry
	// pricing serves the session-cost chip (ADR-036 D8 chip 2). One
	// loader per server; thread-safe; mtime-hot-reloaded so an
	// operator-edited override file lights up without restart. See
	// hub/internal/pricing/loader.go for the three-tier resolution.
	pricing *pricing.Loader
	// envelope renders the engine-facing prose for every input.text
	// envelope (ADR-032 D-10 + v1.0.708-alpha). Same 3-tier hot-
	// reload as pricing; the operator override lives under
	// <HUB_DATA>/team/templates/envelope/active.yaml — reachable
	// through the existing /templates REST surface + mobile's
	// TemplateEditorScreen, so prompt iteration doesn't require a
	// rebuild. See hub/internal/envelope/loader.go.
	envelope *envelope.Loader
	// otlp is the operator trace exporter (ADR-038 §4), non-nil only when
	// Config.OTLPEndpoint is set. otlpWatermark tracks the max exported
	// turn end_ts per session so the loop re-ships only sessions that
	// grew; deterministic span IDs keep the re-export idempotent. See
	// otlp_export.go.
	otlp          *otlptrace.Client
	otlpMu        sync.Mutex
	otlpWatermark map[string]string
	// digestDirty tracks agents with events past their digest watermark and
	// the bounded-staleness trigger accounting (count / turn-closed / age),
	// scanned by the background fold worker (runDigestFold, ADR-038 amendment
	// / store-separation step 1). The ingest hot path only marks dirty; the
	// fold runs off-path. agentID -> pending state.
	digestDirtyMu sync.Mutex
	digestDirty   map[string]*digestPending
}

func New(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	// W10b: startup-time bundled-template audit. Refuse to start if
	// any bundled agent template can't produce a launchable spec —
	// e.g. backend.cmd is empty after the file's intended structure.
	// Pre-bundle the loader silent-skipped semantically-broken
	// templates (defensible for parse errors; insufficient for
	// missing-required-field), so a regression in a template file
	// only surfaced when a steward tried to spawn from it (the
	// v1.0.619 incident). The audit catches the regression at hub
	// start where operators can act on it. See
	// docs/discussions/validate-at-every-boundary.md §3 (Layer 2)
	// and plans/spawn-robustness-and-validators.md W10b.
	if err := auditBundledAgentTemplates(); err != nil {
		return nil, fmt.Errorf("bundled-template audit: %w", err)
	}
	// W10d (v1.0.625): the var-reference audit catches `{{var}}`
	// references in bundled templates / prompts that would silently
	// expand to "" at render time — the v1.0.625 incident class
	// (steward.research.v1.md used `{{project_id}}` unbound; four
	// worker prompts used `{{parent.handle}}` while the bound key
	// was `parent_handle`). See
	// docs/discussions/validate-at-every-boundary.md §3 (Layer 4).
	if err := auditBundledTemplateVarRefs(); err != nil {
		return nil, fmt.Errorf("bundled-template var-reference audit: %w", err)
	}
	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	// Writer/reader pool split — docs/discussions/hub-scaling-storage-and-
	// concurrency.md §6 lever 3. SQLite is a single-writer store. s.db stays
	// the uncapped general/reader pool (WAL lets readers run concurrently);
	// s.writeDB is a dedicated ONE-connection writer pool that ALL writes go
	// through, so writes queue cheaply in Go instead of colliding on the
	// write lock and exhausting busy_timeout under many concurrent agents
	// (the SQLITE_BUSY cliff measured at ~400 agents, §4.1). The writer pool
	// only ever runs Exec/BeginTx — it never holds an open *sql.Rows — so the
	// 1-connection cap can't deadlock on a read held across another acquire
	// (the BeginTx blocks are tx-local, audited). Reads (incl. tests) keep
	// using s.db unchanged.
	writeDB, err := OpenWriterDB(cfg.DBPath)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	writeDB.SetMaxOpenConns(1)
	writeDB.SetMaxIdleConns(1)
	// Per-team store sharding (ADR-045 D2 / plan §P2): the event firehose and its
	// derived digest are sharded into per-team files (dataRoot/teams/<team>/
	// {events.db,digest.db}), each with its own reader + 1-writer pool, so
	// cross-team ingest fans out across N writers instead of serializing on one.
	// ensurePerTeamLayout resolves the boot state — it drops the empty moving
	// tables from a fresh hub.db, and REFUSES to serve a populated global (P1) or
	// un-split (pre-P1) store, telling the operator which one-shot `hub-server db`
	// migration to run (deliberate, backed-up). The registry then opens each
	// team's shard lazily on first access.
	if err := ensurePerTeamLayout(cfg.DBPath); err != nil {
		_ = writeDB.Close()
		_ = db.Close()
		return nil, err
	}
	stores := newTeamStores(cfg.DataRoot, 0)
	// Seed bundled templates that don't yet exist on disk. Init() does
	// this once at `hub init`, but a hub upgraded across versions only
	// runs `hub serve` on subsequent boots, so newly-shipped built-ins
	// would otherwise be invisible until someone re-ran init. The call
	// is idempotent and never overwrites an existing file (user edits
	// stay), so it's safe to run on every start.
	if err := writeBuiltinTemplates(cfg.DataRoot); err != nil {
		cfg.Logger.Warn("seed builtin templates", "err", err)
	}
	// Seed + load the editable loop-hooks overlay (ADR-034 §7). The
	// bundled default is the seed; an operator edits
	// <dataRoot>/loop-hooks.yaml and SIGHUP hot-reloads it.
	if err := writeLoopHooksDefault(cfg.DataRoot); err != nil {
		cfg.Logger.Warn("seed loop-hooks.yaml", "err", err)
	}
	loopHooksConfig.Store(loadLoopHooks(cfg.DataRoot))
	s := &Server{cfg: cfg, db: db, writeDB: writeDB, log: cfg.Logger, bus: newEventBus(),
		stores: stores, digestDirty: map[string]*digestPending{}}
	// Operation-scope manifest (ADR-016) — load embedded + overlay so
	// dispatchTool's role-gating middleware has a manifest to consult.
	// Failure to parse is a hard error: without the manifest, every
	// agent MCP call would fail-closed, which is worse than refusing
	// to start.
	if err := initRoles(cfg.DataRoot); err != nil {
		return nil, fmt.Errorf("init roles manifest: %w", err)
	}
	s.policy = newPolicyStoreWithLogger(cfg.DataRoot, cfg.Logger)
	s.agentFamilies = agentfamilies.New(agentFamiliesOverlayDir(cfg.DataRoot))
	// Register as the package default so spawn_mode.go's call to
	// agentfamilies.ByName picks up the overlay too. Tests that need
	// embedded-only behaviour can call SetDefault(New("")) in cleanup.
	agentfamilies.SetDefault(s.agentFamilies)
	s.sched = NewScheduler(s, cfg.Logger)
	s.escalator = NewEscalator(s, cfg.Logger, 0)
	s.tunnel = newTunnelManager()
	// Pricing loader (ADR-036 D10). Warner closure adapts the
	// loader's action/summary/meta call into the server's recordAudit
	// row — operator-visible parse errors land in audit_events under
	// `pricing.config_error`. The chip itself still renders on the
	// embedded fallback even when the override file is broken.
	s.pricing = pricing.NewLoader(func(action, summary string, meta map[string]any) {
		// No team scope for hub-global config errors — recordAudit
		// silently drops rows with empty team_id, which is the wrong
		// shape for this case. Log instead until a per-team audit
		// channel exists.
		s.log.Warn("pricing config", "action", action, "msg", summary, "meta", meta)
	}).WithHubData(cfg.DataRoot)
	// Envelope template loader (ADR-032 D-10). Same audit-via-log
	// pattern as pricing — `envelope.config_error` rows are operator-
	// facing diagnostics for parse/validation failures on the
	// override file. The host-runner is unaffected by a bad template
	// because the legacy hardcoded prose remains on the consumer
	// side as a defence-in-depth fallback. WithHubData binds the
	// loader to the same on-disk root the server uses, so the
	// override path tracks the configured data root rather than
	// relying on $HUB_DATA being identical in every deploy + test.
	s.envelope = envelope.NewLoader(func(action, summary string, meta map[string]any) {
		s.log.Warn("envelope config", "action", action, "msg", summary, "meta", meta)
	}).WithHubData(cfg.DataRoot)
	// Operator OTLP trace export (ADR-038 §4) — opt-in. The export loop is
	// launched from Serve; here we just build the client + watermark map.
	if cfg.OTLPEndpoint != "" {
		svc := cfg.OTLPServiceName
		if svc == "" {
			svc = "termipod-hub"
		}
		s.otlp = &otlptrace.Client{
			Endpoint: cfg.OTLPEndpoint,
			Resource: otlptrace.Resource{ServiceName: svc},
		}
		s.otlpWatermark = map[string]string{}
		s.log.Info("otlp trace export enabled", "endpoint", s.otlp.TracesURL(), "service", svc)
	}
	s.router = s.buildRouter()
	return s, nil
}

func (s *Server) DB() *sql.DB { return s.db }

func (s *Server) Close() error {
	// Close the per-team shard pools (each team's events.db / digest.db
	// reader+writer), then the global control pools. closeAll waits for any
	// in-flight fold tx to finish before closing a team's pool.
	if s.stores != nil {
		s.stores.closeAll()
	}
	if s.writeDB != nil {
		_ = s.writeDB.Close()
	}
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Server) Serve(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	if err := s.sched.Start(ctx); err != nil {
		s.log.Warn("scheduler start failed", "err", err)
	}
	defer s.sched.Stop()
	// Escalator shares the same ctx lifetime — no explicit Stop needed.
	s.escalator.Start(ctx)
	// Host-liveness sweep: flip hosts to 'offline' when heartbeats stop.
	// Runs until ctx is cancelled, no Stop handle needed.
	go s.runHostSweep(ctx)
	// Loop-closure reconcile sweep: detect stalled loop-entities and
	// escalate / time them out (ADR-034). Same ctx lifetime.
	go s.runLoopSweep(ctx)
	// Deferred digest fold (ADR-038 amendment, hub-scaling lever 7): the
	// ingest path marks agents dirty; this worker folds them off the hot
	// path. Read-repair (ensureAgentDigest) is the backstop, so a missed
	// pass is never wrong — just lazily recomputed on read.
	go s.runDigestFold(ctx)
	// Operator OTLP trace export (ADR-038 §4) — only when configured.
	// Idle/terminal batch cadence; same ctx lifetime, no Stop handle.
	if s.otlp != nil {
		go s.runOTLPExport(ctx)
	}

	// SIGHUP → hot-reload policy.yaml. Lets an operator edit the file and
	// signal the daemon without restarting and losing in-flight connections.
	hupCh := make(chan os.Signal, 1)
	signal.Notify(hupCh, syscall.SIGHUP)
	defer signal.Stop(hupCh)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-hupCh:
				if s.policy != nil {
					s.policy.reload()
					s.log.Info("policy reloaded")
				}
				loopHooksConfig.Store(loadLoopHooks(s.cfg.DataRoot))
				s.log.Info("loop-hooks reloaded")
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("hub-server listening", "addr", s.cfg.Listen)
		errCh <- srv.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func (s *Server) buildRouter() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	// Note: no global request-timeout middleware — /events/stream is a
	// long-poll SSE endpoint. Non-streaming handlers get their own timeout
	// from http.Server.ReadHeaderTimeout + per-call context deadlines as
	// needed; we rely on the HTTP client to set its own read timeout.

	// /mcp/{token} bypasses Authorization-header auth: the token is
	// carried in the URL path so claude-code can be pointed at the
	// endpoint with no custom headers. handleMCP authenticates itself.
	r.Post("/mcp/{token}", s.handleMCP)

	// /a2a/relay/{host}/{agent}/* is the public A2A surface (P3.3b).
	// Unauthed per A2A v0.3 peer-to-peer spec — the host + agent ids in
	// the URL are the capability. Token-based peer auth is a follow-up.
	r.HandleFunc("/a2a/relay/{host}/{agent}", s.handleRelay)
	r.HandleFunc("/a2a/relay/{host}/{agent}/*", s.handleRelay)

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(s.db))
		s.buildAuthedRoutes(r)
	})
	return r
}

func (s *Server) buildAuthedRoutes(r chi.Router) {

	r.Get("/v1/_info", s.handleInfo)
	// /v1/hub/stats — hub-self capacity (machine + DB + live counts).
	// ADR-022 D2: purpose-built endpoint; the hub does not appear as a
	// synthetic row in the multi-tenant `hosts` table. Authed but not
	// team-scoped; Phase 1 punts on token-scope refinement, so any
	// authenticated bearer can read it.
	r.Get("/v1/hub/stats", s.handleHubStats)

	// /v1/hub/config — operator-only hub-wide governance files
	// (ADR-037 D2). MVP exposes the operation-scope manifest
	// (roles.yaml); pattern extends to other hub-level configs as they
	// appear. See handlers_hub_config.go for the validate-then-swap
	// contract.
	r.Get("/v1/hub/config/roles", s.handleGetRolesConfig)
	r.Put("/v1/hub/config/roles", s.handlePutRolesConfig)
	r.Delete("/v1/hub/config/roles", s.handleResetRolesConfig)

	// /v1/admin/* — hub-wide ops endpoints (ADR-028). Operator-scope is
	// enforced inside each handler (requireOperator, ADR-037 D2) — a
	// per-team owner cannot reach the fleet. Phases 1-3 wired the
	// fleet control verbs; Phase 4 adds read-side host inspection;
	// Phase 5 adds the per-host control verbs, db maintenance, and the
	// cross-team audit query the mobile Admin pane consumes.
	r.Post("/v1/admin/fleet/shutdown", s.handleAdminFleetShutdown)
	r.Post("/v1/admin/fleet/update", s.handleAdminFleetUpdate)
	r.Post("/v1/admin/fleet/restart", s.handleAdminFleetRestart)
	r.Get("/v1/admin/hosts", s.handleAdminListHosts)
	r.Post("/v1/admin/hosts/{host}/ping", s.handleAdminHostPing)
	r.Post("/v1/admin/hosts/{host}/shutdown", s.handleAdminHostShutdown)
	r.Post("/v1/admin/hosts/{host}/restart", s.handleAdminHostRestart)
	r.Post("/v1/admin/hosts/{host}/update", s.handleAdminHostUpdate)
	r.Get("/v1/admin/agents", s.handleAdminListAgents)
	r.Post("/v1/admin/agents/{agent}/kill", s.handleAdminKillAgent)
	r.Post("/v1/admin/tokens/rotate", s.handleAdminTokensRotate)
	r.Post("/v1/admin/db/vacuum", s.handleAdminDBVacuum)
	r.Get("/v1/admin/audit", s.handleAdminListAudit)
	// Team provisioning (ADR-037 D3 / W3). Operator-gated onboarding:
	// create a team + mint its first owner token. List enumerates teams.
	r.Post("/v1/admin/teams", s.handleAdminCreateTeam)
	r.Get("/v1/admin/teams", s.handleAdminListTeams)
	// Rotate a team's owner token (issue fresh + revoke prior). Operator-
	// gated; never touches the operator/host credentials.
	r.Post("/v1/admin/teams/{team}/rotate-token", s.handleAdminRotateTeamToken)

	// /v1/insights — scope-parameterized aggregator (ADR-022 D3).
	// Phase 1 W2 wires the project scope only:
	// /v1/insights?project_id=...&since=...&until=...
	// Same auth posture as /v1/hub/stats; the project_id is the
	// authorization unit and projects are team-owned, so a stray
	// cross-team read is bounded to "you have a hub bearer".
	r.Get("/v1/insights", s.handleInsights)

	r.Route("/v1/teams/{team}", func(r chi.Router) {
		// ADR-037 D1 — path-team authorization gate. A token may only
		// address the team in its scope_json; operators transcend it.
		// Mounted here so every team-scoped route below inherits it from
		// one chokepoint.
		r.Use(s.teamGate)
		r.Route("/hosts", func(r chi.Router) {
			r.Post("/", s.handleRegisterHost)
			r.Get("/", s.handleListHosts)
			r.Route("/{host}", func(r chi.Router) {
				r.Get("/", s.handleGetHost)
				r.Delete("/", s.handleDeleteHost)
				r.Post("/heartbeat", s.handleHostHeartbeat)
				r.Get("/commands", s.handleListHostCommands)
				r.Patch("/ssh_hint", s.handleUpdateHostSSHHint)
				r.Put("/capabilities", s.handleUpdateHostCapabilities)
				r.Put("/a2a/cards", s.handlePutHostA2ACards)
				r.Get("/a2a/tunnel/next", s.handleTunnelNext)
				r.Post("/a2a/tunnel/responses", s.handleTunnelResponse)
			})
		})
		r.Get("/a2a/cards", s.handleListTeamA2ACards)
		r.Patch("/commands/{cmd}", s.handlePatchHostCommand)
		// Idempotent ensure-spawn for the team's general steward
		// (W4 / ADR-001 D-amend-2). Returns the existing running
		// instance if any; otherwise spawns one. Mobile's home-tab
		// "Steward" card hits this on first interaction with the
		// team. Singleton — concurrent calls coalesce on the first
		// running instance.
		r.Post("/steward.general/ensure", s.handleEnsureGeneralSteward)

		// Agent-driven mobile UI prototype (v1.0.464+). The steward's
		// `mobile.navigate` MCP tool POSTs here to fan a URI out to
		// mobile clients via the general steward's existing SSE
		// channel. Read-only verbs only at this stage; write intents
		// are post-prototype per docs/discussions/agent-driven-mobile-ui.md.
		r.Post("/mobile/intent", s.handleMobileIntent)

		r.Route("/agents", func(r chi.Router) {
			r.Post("/", s.handleCreateAgent)
			r.Get("/", s.handleListAgents)
			r.Post("/spawn", s.handleSpawn)
			r.Get("/spawns", s.handleListSpawns)
			r.Route("/{agent}", func(r chi.Router) {
				r.Get("/", s.handleGetAgent)
				r.Patch("/", s.handlePatchAgent)
				r.Delete("/", s.handleArchiveAgent)
				r.Get("/journal", s.handleReadJournal)
				r.Post("/journal", s.handleAppendJournal)
				r.Post("/pause", s.handlePauseAgent)
				r.Post("/resume", s.handleResumeAgent)
				r.Post("/resume-session", s.handleResumeAgentSession)
				r.Post("/stop", s.handleStopAgent)
				r.Post("/terminate", s.handleTerminateAgent)
				r.Get("/pane", s.handleGetAgentPane)
				r.Post("/events", s.handlePostAgentEvent)
				r.Get("/events", s.handleListAgentEvents)
				r.Get("/stream", s.handleStreamAgentEvents)
				// ADR-038 §5: the per-agent run digest (canonical summary +
				// navigation anchors). Lazily (re)computed on read.
				r.Get("/digest", s.handleGetAgentDigest)
				// ADR-038 §3 / plan P2: the turn index as a keyset listing —
				// the "Turns" filtered view + jump-to-turn anchors.
				r.Get("/turns", s.handleListAgentTurns)
				r.Post("/input", s.handlePostAgentInput)
			})
		})
		// Team-scoped task-by-id lookup (ADR-033 W5). Tasks otherwise
		// live under /projects/{project}/tasks; this resolves one by
		// its ULID alone (team-scoped via a projects join) so the
		// tasks_get MCP tool works without project_id — which lets the
		// deprecated get_task (bare id) keep working as an alias.
		r.Get("/tasks/{task}", s.handleGetTaskByID)
		// Directive trace (ADR-034 D-7) — the per-directive timeline,
		// reconstructed by walking the cause/parent chain. A query, no
		// new event stream.
		r.Get("/directives/{task}/trace", s.handleDirectiveTrace)
		r.Route("/sessions", func(r chi.Router) {
			r.Post("/", s.handleOpenSession)
			r.Get("/", s.handleListSessions)
			// Full-text search across this team's session
			// transcripts (Phase 1.5c). Distinct from
			// /v1/search which targets channel events.
			r.Get("/search", s.handleSessionSearch)
			r.Route("/{session}", func(r chi.Router) {
				r.Get("/", s.handleGetSession)
				r.Patch("/", s.handlePatchSession)
				// /archive is the canonical archive action (ADR-009). The
				// deprecated /close alias was retired in the WS1.2 internal
				// tech-debt cleanup (no external API consumers).
				r.Post("/archive", s.handleArchiveSession)
				r.Post("/fork", s.handleForkSession)
				r.Post("/resume", s.handleResumeSession)
				r.Delete("/", s.handleDeleteSession)
				// /cost is the per-session imputed USD breakdown
				// (ADR-036 D8 chip 2). Read by the session-cost chip
				// for tooltip-level detail; the scalar total is also
				// inlined on the parent GET as session_cost_usd_imputed
				// to avoid an extra round-trip on first paint.
				r.Get("/cost", s.handleGetSessionCost)
				// ADR-038 §5: the session-scoped run digest — the
				// ts-ordered rollup of the session's agents' digests.
				r.Get("/digest", s.handleGetSessionDigest)
				// ADR-038 §3 / plan P2: the session's turn index — the
				// ts-ordered union of its agents' turns.
				r.Get("/turns", s.handleListSessionTurns)
			})
		})
		r.Route("/templates", func(r chi.Router) {
			r.Get("/", s.handleListTemplates)
			r.Post("/reset", s.handleResetBundledTemplates)
			r.Get("/{category}/{name}", s.handleGetTemplate)
			r.Put("/{category}/{name}", s.handlePutTemplate)
			r.Delete("/{category}/{name}", s.handleDeleteTemplate)
			r.Patch("/{category}/{name}", s.handleRenameTemplate)
		})
		r.Route("/agent-families", func(r chi.Router) {
			r.Get("/", s.handleListAgentFamilies)
			r.Post("/reset", s.handleResetAgentFamilies)
			r.Get("/{family}", s.handleGetAgentFamily)
			r.Put("/{family}", s.handlePutAgentFamily)
			r.Delete("/{family}", s.handleDeleteAgentFamily)
		})
		r.Route("/projects", func(r chi.Router) {
			r.Post("/", s.handleCreateProject)
			r.Get("/", s.handleListProjects)
			r.Route("/{project}", func(r chi.Router) {
				r.Get("/", s.handleGetProject)
				r.Patch("/", s.handleUpdateProject)
				r.Delete("/", s.handleArchiveProject)
				r.Route("/channels", func(r chi.Router) {
					r.Post("/", s.handleCreateChannel)
					r.Get("/", s.handleListChannels)
					r.Route("/{channel}", func(r chi.Router) {
						r.Get("/", s.handleGetChannel)
						r.Post("/events", s.handlePostEvent)
						r.Get("/events", s.handleListEvents)
						r.Get("/stream", s.handleStreamEvents)
					})
				})
				r.Route("/tasks", func(r chi.Router) {
					r.Post("/", s.handleCreateTask)
					r.Get("/", s.handleListTasks)
					r.Route("/{task}", func(r chi.Router) {
						r.Get("/", s.handleGetTask)
						r.Patch("/", s.handlePatchTask)
						r.Delete("/", s.handleDeleteTask)
					})
				})
				r.Route("/docs", func(r chi.Router) {
					r.Get("/", s.handleListProjectDocs)
					r.Get("/*", s.handleGetProjectDoc)
				})
				r.Route("/phase", func(r chi.Router) {
					r.Get("/", s.handleGetProjectPhase)
					r.Post("/", s.handleSetProjectPhase)
					r.Post("/advance", s.handleAdvanceProjectPhase)
				})
				r.Route("/steward", func(r chi.Router) {
					r.Get("/state", s.handleGetStewardState)
					// ADR-025 W3 — idempotent ensure-spawn for the
					// project's domain steward. Director-auth only
					// (general steward delegates via attention items
					// per W4 rather than calling this directly).
					r.Post("/ensure", s.handleEnsureProjectSteward)
				})
				// ADR-046 / WS4 — explicit Start: spawn the project's
				// bound domain steward (create binds it; this spawns
				// it). 409 if a steward is already running.
				r.Post("/start", s.handleStartProject)
				// W5b — Deliverables + components (A3 §4 + §5). Templates
				// hydrate these on phase entry; the runtime here ships the
				// CRUD + ratify + composed-overview surface.
				r.Route("/deliverables", func(r chi.Router) {
					r.Get("/", s.handleListDeliverables)
					r.Post("/", s.handleCreateDeliverable)
					r.Route("/{deliverable}", func(r chi.Router) {
						r.Get("/", s.handleGetDeliverable)
						r.Patch("/", s.handlePatchDeliverable)
						r.Post("/ratify", s.handleRatifyDeliverable)
						r.Post("/unratify", s.handleUnratifyDeliverable)
						// ADR-020 W2 — director returns a draft/in-review
						// deliverable to the steward with a structured note
						// + selected annotation IDs. Transitions state to
						// in-review; raises a `revision_requested` attention
						// item that the steward picks up via the normal loop.
						r.Post("/send-back", s.handleSendBackDeliverable)
						r.Route("/components", func(r chi.Router) {
							r.Post("/", s.handleAddDeliverableComponent)
							r.Delete("/{component}", s.handleRemoveDeliverableComponent)
						})
					})
				})
				// W5b lists criteria; W6 ships the mutation surface.
				r.Route("/criteria", func(r chi.Router) {
					r.Get("/", s.handleListProjectCriteria)
					r.Post("/", s.handleCreateCriterion)
					r.Route("/{criterion}", func(r chi.Router) {
						r.Get("/", s.handleGetCriterion)
						r.Patch("/", s.handlePatchCriterion)
						r.Post("/mark-met", s.handleMarkCriterion("mark-met"))
						r.Post("/mark-failed", s.handleMarkCriterion("mark-failed"))
						r.Post("/waive", s.handleMarkCriterion("waive"))
					})
				})
				r.Get("/overview", s.handleGetProjectOverview)
				r.Get("/sweep-summary", s.handleGetProjectSweepSummary)
			})
		})
		// Runs (§6.5): team-scoped; filter by project via ?project= query
		// param. Sits alongside /projects, not nested inside it, because the
		// parent scope is the team (runs cross projects via parent_run_id).
		r.Route("/runs", func(r chi.Router) {
			r.Get("/", s.handleListRuns)
			r.Post("/", s.handleCreateRun)
			r.Route("/{run}", func(r chi.Router) {
				r.Get("/", s.handleGetRun)
				r.Patch("/", s.handleUpdateRun)
				r.Delete("/", s.handleDeleteRun)
				r.Delete("/artifacts/{artifact}", s.handleDetachRunArtifact)
				r.Post("/complete", s.handleCompleteRun)
				r.Post("/metric_uri", s.handleAttachMetricURI)
				r.Put("/metrics", s.handlePutRunMetrics)
				r.Get("/metrics", s.handleGetRunMetrics)
				r.Post("/images", s.handlePostRunImages)
				r.Get("/images", s.handleGetRunImages)
				r.Put("/histograms", s.handlePutRunHistograms)
				r.Get("/histograms", s.handleGetRunHistograms)
				// Run "extras" digests from the trackio sibling tables.
				r.Put("/config", s.handlePutRunConfig)
				r.Get("/config", s.handleGetRunConfig)
				r.Put("/system_metrics", s.handlePutRunSystemMetrics)
				r.Get("/system_metrics", s.handleGetRunSystemMetrics)
				r.Put("/alerts", s.handlePutRunAlerts)
				r.Get("/alerts", s.handleGetRunAlerts)
			})
		})
		// Documents (§6.7) + Reviews (§6.8). Team-scoped; filter by project
		// via ?project=. Sits at team scope (not nested under /projects) so
		// that cross-project review queues can be listed with a single query.
		r.Route("/documents", func(r chi.Router) {
			r.Get("/", s.handleListDocuments)
			r.Post("/", s.handleCreateDocument)
			r.Route("/{doc}", func(r chi.Router) {
				r.Get("/", s.handleGetDocument)
				r.Get("/versions", s.handleListDocumentVersions)
				// W5a — Structured Document Viewer (A4). Sections are
				// stored inline in content_inline as JSON; PATCH and
				// status edit the typed body, not plain markdown.
				r.Route("/sections/{slug}", func(r chi.Router) {
					r.Patch("/", s.handlePatchDocumentSection)
					r.Post("/status", s.handleSetDocumentSectionStatus)
				})
				// ADR-020 W1 — anchored director annotations on a
				// section. List + create live under the document; PATCH /
				// resolve / reopen live at /annotations/{id} below since
				// they don't need the document URL parameter. DELETE is
				// rejected (annotations are append-only-on-content; D3).
				r.Route("/annotations", func(r chi.Router) {
					r.Get("/", s.handleListAnnotations)
					r.Post("/", s.handleCreateAnnotation)
				})
			})
		})
		r.Route("/annotations/{annotation}", func(r chi.Router) {
			r.Patch("/", s.handlePatchAnnotation)
			r.Delete("/", s.handleDeleteAnnotationDisallowed)
			r.Post("/resolve", s.handleResolveAnnotation)
			r.Post("/reopen", s.handleReopenAnnotation)
		})
		r.Route("/reviews", func(r chi.Router) {
			r.Get("/", s.handleListReviews)
			r.Post("/", s.handleCreateReview)
			r.Route("/{review}", func(r chi.Router) {
				r.Get("/", s.handleGetReview)
				r.Post("/decide", s.handleDecideReview)
			})
		})
		// Artifacts (§6.6): content-addressed outputs produced by runs or
		// uploaded by users. Team-scoped with ?project= / ?run= / ?kind=
		// filters; sits alongside /documents for the same cross-project
		// listing reason.
		r.Route("/artifacts", func(r chi.Router) {
			r.Get("/", s.handleListArtifacts)
			r.Post("/", s.handleCreateArtifact)
			r.Get("/{artifact}", s.handleGetArtifact)
		})
		// Plans (§6.2): shallow review-able scaffolds of phases.
		r.Route("/plans", func(r chi.Router) {
			r.Post("/", s.handleCreatePlan)
			r.Get("/", s.handleListPlans)
			r.Route("/{plan}", func(r chi.Router) {
				r.Get("/", s.handleGetPlan)
				r.Patch("/", s.handleUpdatePlan)
				r.Route("/steps", func(r chi.Router) {
					r.Post("/", s.handleCreatePlanStep)
					r.Get("/", s.handleListPlanSteps)
					r.Patch("/{step}", s.handleUpdatePlanStep)
				})
			})
		})
		r.Route("/attention", func(r chi.Router) {
			r.Post("/", s.handleCreateAttention)
			r.Get("/", s.handleListAttention)
			r.Get("/{id}", s.handleGetAttention)
			r.Get("/{id}/context", s.handleAttentionContext)
			r.Post("/{id}/resolve", s.handleResolveAttention)
			r.Post("/{id}/decide", s.handleDecideAttention)
		})
		r.Route("/schedules", func(r chi.Router) {
			r.Post("/", s.handleCreateSchedule)
			r.Get("/", s.handleListSchedules)
			r.Route("/{schedule}", func(r chi.Router) {
				r.Patch("/", s.handlePatchSchedule)
				r.Delete("/", s.handleDeleteSchedule)
				r.Post("/run", s.handleRunSchedule)
			})
		})
		// Team-scope channels (project_id NULL, scope_kind='team'). Events +
		// stream reuse the project-scope handlers — they only consume the
		// channel URL param.
		r.Route("/channels", func(r chi.Router) {
			r.Post("/", s.handleCreateTeamChannel)
			r.Get("/", s.handleListTeamChannels)
			r.Route("/{channel}", func(r chi.Router) {
				r.Get("/", s.handleGetTeamChannel)
				r.Post("/events", s.handlePostEvent)
				r.Get("/events", s.handleListEvents)
				r.Get("/stream", s.handleStreamEvents)
			})
		})
		r.Get("/principals", s.handleListPrincipals)
		r.Get("/audit", s.handleListAudit)
		r.Get("/policy", s.handleGetPolicy)
		r.Put("/policy", s.handlePutPolicy)
		// ADR-030 W21 — parsed `kinds:` block as JSON for the mobile
		// read-only policy viewer (avoids shipping a YAML parser in
		// the Flutter binary). Read-only; the canonical edit path
		// stays `PUT /policy` against the full YAML file.
		r.Get("/policy/kinds", s.handleGetPolicyKinds)
		r.Route("/tokens", func(r chi.Router) {
			r.Get("/", s.handleListTokens)
			r.Post("/", s.handleIssueToken)
			r.Post("/{id}/revoke", s.handleRevokeToken)
		})
	})

	r.Route("/v1/blobs", func(r chi.Router) {
		r.Post("/", s.handleUploadBlob)
		r.Get("/{sha}", s.handleGetBlob)
	})

	r.Get("/v1/search", s.handleSearch)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Hint is the structured recovery envelope carried on a 4xx error
// (ADR-031 D-3). HintText is required; SeeTool / SeeDoc are optional
// but at least one of the three should carry actionable signal — the
// point of a hint is to tell the caller what to do next.
type Hint struct {
	HintText string `json:"hint_text"`
	SeeTool  string `json:"see_tool,omitempty"`
	SeeDoc   string `json:"see_doc,omitempty"`
}

// writeErrHint writes a 4xx error with a structured recovery hint:
//
//	{"error": msg, "hint": {"hint_text": ..., "see_tool": ...}}
//
// It is the hint-bearing sibling of writeErr (ADR-031 W3). A client
// that ignores the hint key still reads `error` exactly as before.
func writeErrHint(w http.ResponseWriter, status int, msg string, hint Hint) {
	writeJSON(w, status, map[string]any{"error": msg, "hint": hint})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"server_version":            ServerVersion,
		"supported_api_versions":    []string{"v1"},
		"schema_versions_supported": []int{1},
	}
	if buildinfo.Commit != "" {
		out["commit"] = buildinfo.Commit
	}
	if buildinfo.BuildTime != "" {
		out["build_time"] = buildinfo.BuildTime
	}
	if buildinfo.Modified {
		out["modified"] = true
	}
	writeJSON(w, http.StatusOK, out)
}
