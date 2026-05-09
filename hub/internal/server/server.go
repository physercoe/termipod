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
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/termipod/hub/internal/agentfamilies"
	"github.com/termipod/hub/internal/auth"
	"github.com/termipod/hub/internal/buildinfo"
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
	Logger    *slog.Logger
}

type Server struct {
	cfg          Config
	db           *sql.DB
	router       chi.Router
	log          *slog.Logger
	bus          *eventBus
	sched        *Scheduler
	policy       *policyStore
	escalator    *Escalator
	tunnel       *TunnelManager
	agentFamilies *agentfamilies.Registry
}

func New(cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	// Seed bundled templates that don't yet exist on disk. Init() does
	// this once at `hub init`, but a hub upgraded across versions only
	// runs `hub serve` on subsequent boots, so newly-shipped built-ins
	// would otherwise be invisible until someone re-ran init. The call
	// is idempotent and never overwrites an existing file (user edits
	// stay), so it's safe to run on every start.
	if err := writeBuiltinTemplates(cfg.DataRoot); err != nil {
		cfg.Logger.Warn("seed builtin templates", "err", err)
	}
	s := &Server{cfg: cfg, db: db, log: cfg.Logger, bus: newEventBus()}
	// Operation-scope manifest (ADR-016) — load embedded + overlay so
	// dispatchTool's role-gating middleware has a manifest to consult.
	// Failure to parse is a hard error: without the manifest, every
	// agent MCP call would fail-closed, which is worse than refusing
	// to start.
	if err := initRoles(cfg.DataRoot); err != nil {
		return nil, fmt.Errorf("init roles manifest: %w", err)
	}
	s.policy = newPolicyStore(cfg.DataRoot)
	s.agentFamilies = agentfamilies.New(agentFamiliesOverlayDir(cfg.DataRoot))
	// Register as the package default so spawn_mode.go's call to
	// agentfamilies.ByName picks up the overlay too. Tests that need
	// embedded-only behaviour can call SetDefault(New("")) in cleanup.
	agentfamilies.SetDefault(s.agentFamilies)
	s.sched = NewScheduler(s, cfg.Logger)
	s.escalator = NewEscalator(s, cfg.Logger, 0)
	s.tunnel = newTunnelManager()
	s.router = s.buildRouter()
	return s, nil
}

func (s *Server) DB() *sql.DB { return s.db }

func (s *Server) Close() error { return s.db.Close() }

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

	// /v1/insights — scope-parameterized aggregator (ADR-022 D3).
	// Phase 1 W2 wires the project scope only:
	// /v1/insights?project_id=...&since=...&until=...
	// Same auth posture as /v1/hub/stats; the project_id is the
	// authorization unit and projects are team-owned, so a stray
	// cross-team read is bounded to "you have a hub bearer".
	r.Get("/v1/insights", s.handleInsights)

	r.Route("/v1/teams/{team}", func(r chi.Router) {
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
				r.Get("/pane", s.handleGetAgentPane)
				r.Post("/events", s.handlePostAgentEvent)
				r.Get("/events", s.handleListAgentEvents)
				r.Get("/stream", s.handleStreamAgentEvents)
				r.Post("/input", s.handlePostAgentInput)
			})
		})
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
				// /archive is canonical (ADR-009); /close is a deprecated
				// alias kept for one release so an in-flight app build
				// doesn't break during coordinated rollout.
				r.Post("/archive", s.handleArchiveSession)
				r.Post("/close", s.handleArchiveSession)
				r.Post("/fork", s.handleForkSession)
				r.Post("/resume", s.handleResumeSession)
				r.Delete("/", s.handleDeleteSession)
			})
		})
		r.Route("/templates", func(r chi.Router) {
			r.Get("/", s.handleListTemplates)
			r.Get("/{category}/{name}", s.handleGetTemplate)
			r.Put("/{category}/{name}", s.handlePutTemplate)
			r.Delete("/{category}/{name}", s.handleDeleteTemplate)
			r.Patch("/{category}/{name}", s.handleRenameTemplate)
		})
		r.Route("/agent-families", func(r chi.Router) {
			r.Get("/", s.handleListAgentFamilies)
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
				})
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
				r.Post("/complete", s.handleCompleteRun)
				r.Post("/metric_uri", s.handleAttachMetricURI)
				r.Put("/metrics", s.handlePutRunMetrics)
				r.Get("/metrics", s.handleGetRunMetrics)
				r.Post("/images", s.handlePostRunImages)
				r.Get("/images", s.handleGetRunImages)
				r.Put("/histograms", s.handlePutRunHistograms)
				r.Get("/histograms", s.handleGetRunHistograms)
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
