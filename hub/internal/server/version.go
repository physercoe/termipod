package server

import "github.com/termipod/hub/internal/buildinfo"

// Re-export the shared buildinfo so existing handleInfo / external readers
// stay on `server.Commit` / `server.BuildTime` / `server.Modified`.
// Single source of truth lives in internal/buildinfo so host-runner can
// read the same fields without depending on the server package.
var (
	Commit    = buildinfo.Commit
	BuildTime = buildinfo.BuildTime
	Modified  = buildinfo.Modified
)
