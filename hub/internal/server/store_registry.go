package server

import (
	"container/list"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// store_registry.go — ADR-045 P2: per-team sharding of the event + digest
// stores.
//
// P1 gave the firehose (events.db) and its derived digest (digest.db) their own
// files and writers, split out of the control plane (hub.db). P2 takes the next
// step: those two stores become PER-TEAM files under
// dataRoot/teams/<team>/{events.db,digest.db}, while hub.db stays global. Each
// team then gets its own SQLite write lock, so cross-team ingest fans out across
// N writers instead of serializing on one (plan §P2, ADR-045 D2). hub.db is the
// global control plane — teams/auth/hosts are cross-team concerns that stay
// shared.
//
// teamStores is the connection registry that backs the per-team accessors. It
// lazily opens a team's four pools (events reader/writer + digest reader/writer)
// on first use, ensuring the schema, and bounds the number of simultaneously
// open teams with an LRU so a hub with many teams can't exhaust file
// descriptors. Teams are coarse (an org/workspace, not an agent), so in practice
// the open set is small and eviction is rare; the cap is a safety bound, not a
// hot-path concern.
//
// CAVEAT (documented, accepted for v1): eviction removes the team from the map
// under the lock and then Close()s its pools outside the lock. A concurrent
// get() for the just-evicted team can momentarily open a second handle set to
// the same files before the first finishes closing — two writer pools to one
// file. SQLite's per-file lock + busy_timeout(5000) keep that correct (the
// second writer waits), and the victim is always the coldest team, so the window
// is pathological. Raising HUB_MAX_OPEN_TEAM_STORES avoids it entirely.

// defaultMaxOpenTeamStores bounds how many teams keep their pools open at once.
// Each open team holds four *sql.DB pools; idle pools hold ~no connections
// (database/sql closes idle conns), so the real cost is per-active-team. 128 is
// far above any realistic concurrent-team count on a single hub; override with
// HUB_MAX_OPEN_TEAM_STORES.
const defaultMaxOpenTeamStores = 128

// teamHandles is one team's four store pools. The reader pools are uncapped
// (WAL readers run concurrently); the writer pools are single-connection so
// writes queue in Go instead of colliding on the per-file write lock — the same
// reader/writer split P1 applies globally, now per team.
type teamHandles struct {
	team    string
	eventsR *sql.DB // events.db reader  (agent_events + _fts)
	eventsW *sql.DB // events.db writer
	digestR *sql.DB // digest.db reader  (agent_event_digests + agent_turns)
	digestW *sql.DB // digest.db writer
}

// close shuts down all four pools. Writers first (they hold the single
// connection); (*sql.DB).Close waits for in-flight queries on a pool to finish,
// so a fold tx in progress completes before its pool closes.
func (h *teamHandles) close() {
	for _, db := range []*sql.DB{h.eventsW, h.digestW, h.eventsR, h.digestR} {
		if db != nil {
			_ = db.Close()
		}
	}
}

// teamStores is the per-(team) store-handle registry. Safe for concurrent use.
type teamStores struct {
	root    string // dataRoot/teams
	maxOpen int

	mu     sync.Mutex
	lru    *list.List               // *teamHandles, front = most-recently used
	byTeam map[string]*list.Element // team id -> lru element
}

// newTeamStores builds a registry rooted at dataRoot/teams. maxOpen <= 0 falls
// back to the env override (HUB_MAX_OPEN_TEAM_STORES) or the default cap.
func newTeamStores(dataRoot string, maxOpen int) *teamStores {
	if maxOpen <= 0 {
		maxOpen = defaultMaxOpenTeamStores
		if v := os.Getenv("HUB_MAX_OPEN_TEAM_STORES"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				maxOpen = n
			}
		}
	}
	return &teamStores{
		root:    filepath.Join(dataRoot, "teams"),
		maxOpen: maxOpen,
		lru:     list.New(),
		byTeam:  map[string]*list.Element{},
	}
}

// get returns the open handle set for a team, opening (and schema-ensuring) it
// on first use. The team id must be a valid slug (teamIDRe) — it becomes a path
// segment, so this also guards against traversal. The first open of a team does
// disk I/O under the registry lock; that serializes concurrent first-opens but
// keeps the invariant that there is at most one handle set per team (no
// double-open of the same writer file). Subsequent gets are an O(1) map lookup +
// LRU bump.
func (r *teamStores) get(team string) (*teamHandles, error) {
	if !teamIDRe.MatchString(team) {
		return nil, fmt.Errorf("invalid team id %q for per-team store routing", team)
	}
	r.mu.Lock()
	if el, ok := r.byTeam[team]; ok {
		r.lru.MoveToFront(el)
		h := el.Value.(*teamHandles)
		r.mu.Unlock()
		return h, nil
	}
	h, err := r.openTeam(team)
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	r.byTeam[team] = r.lru.PushFront(h)
	var victim *teamHandles
	if r.lru.Len() > r.maxOpen {
		if back := r.lru.Back(); back != nil {
			victim = back.Value.(*teamHandles)
			r.lru.Remove(back)
			delete(r.byTeam, victim.team)
		}
	}
	r.mu.Unlock()
	if victim != nil {
		victim.close() // outside the lock — Close waits for in-flight queries
	}
	return h, nil
}

// openTeam opens a team's four pools and ensures the events + digest schema.
// Called with r.mu held. The schema DDL is the same CREATE … IF NOT EXISTS shape
// the P1 split uses, so an already-populated team file is left intact.
func (r *teamStores) openTeam(team string) (*teamHandles, error) {
	dir := filepath.Join(r.root, team)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("team store dir %s: %w", team, err)
	}
	eventsPath := filepath.Join(dir, "events.db")
	digestPath := filepath.Join(dir, "digest.db")
	h := &teamHandles{team: team}
	fail := func(err error) (*teamHandles, error) {
		h.close()
		return nil, err
	}
	var err error
	if h.eventsW, err = openStorePool(eventsPath, true); err != nil {
		return fail(err)
	}
	if err = ensureEventsSchema(h.eventsW); err != nil {
		return fail(err)
	}
	if h.eventsR, err = openStorePool(eventsPath, false); err != nil {
		return fail(err)
	}
	if h.digestW, err = openStorePool(digestPath, true); err != nil {
		return fail(err)
	}
	if err = ensureDigestSchema(h.digestW); err != nil {
		return fail(err)
	}
	if h.digestR, err = openStorePool(digestPath, false); err != nil {
		return fail(err)
	}
	return h, nil
}

// closeAll closes every open team's pools and empties the registry. Called from
// Server.Close at shutdown.
func (r *teamStores) closeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, el := range r.byTeam {
		el.Value.(*teamHandles).close()
	}
	r.byTeam = map[string]*list.Element{}
	r.lru.Init()
}

// openCount reports how many teams currently hold open pools (test/observability).
func (r *teamStores) openCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lru.Len()
}

// ensureEventsSchema runs the events.db DDL (real table + indexes + FTS) on an
// already-open pool. Idempotent (CREATE … IF NOT EXISTS), so it's safe on both a
// fresh file and a populated one.
func ensureEventsSchema(db *sql.DB) error {
	if _, err := db.Exec(eventsStoreTablesDDL); err != nil {
		return fmt.Errorf("events store schema: %w", err)
	}
	if _, err := db.Exec(eventsStoreFTSDDL); err != nil {
		return fmt.Errorf("events store fts schema: %w", err)
	}
	return nil
}

// ensureDigestSchema runs the digest.db DDL on an already-open pool. Idempotent.
func ensureDigestSchema(db *sql.DB) error {
	if _, err := db.Exec(digestStoreDDL); err != nil {
		return fmt.Errorf("digest store schema: %w", err)
	}
	return nil
}
