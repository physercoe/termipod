// Package metrics defines the vendor-neutral contract for reading scalar
// training-metric series out of local experiment-tracker stores on a
// host. Concrete backends (trackio, wandb, TensorBoard, …) implement
// Reader; the host-runner poller drives them through this interface so
// it has no per-vendor branching.
//
// Blueprint §4 data-ownership law: the hub stores digest rows only — bulk
// time-series stays on the host. Downsampling happens here before the
// digest is shipped, not on the hub.
package metrics

import "context"

// Point is one sample in a scalar metric series. Step is the training
// step and Value is the scalar logged against it. Non-numeric log
// entries (strings, arrays, histograms, images) are skipped by each
// backend — only sparkline-renderable scalars reach this type.
type Point struct {
	Step  int64
	Value float64
}

// Series is an ordered slice of Points for a single named metric. By
// contract series are sorted ascending by Step and deduplicated so that
// a given Step appears at most once (last-write wins).
type Series []Point

// Reader is the vendor-neutral surface for a local tracker-store backend.
// One Reader is bound to one tracker flavour at construction time (so a
// host may run several in parallel) and identifies itself via a URI
// scheme so the poll loop can route run URIs to the right backend
// without inspecting them.
type Reader interface {
	// Scheme returns the URI scheme this backend handles, without the
	// "://" separator (e.g. "trackio", "wandb", "tb"). The poll loop
	// filters runs by matching runs.trackio_run_uri against
	// "<scheme>://".
	Scheme() string

	// Read loads every scalar metric series for the run identified by
	// uri. The map is keyed by metric name. An empty map (not an error)
	// means the worker hasn't logged yet; the poller treats that as
	// "try again next tick" rather than a failure.
	Read(ctx context.Context, uri string) (map[string]Series, error)
}
