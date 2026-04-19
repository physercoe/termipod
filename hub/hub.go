// Package hub is the root package for the Termipod Hub module.
// Its only job is to expose embedded resources (migrations, built-in
// templates) so that internal/ packages can consume them without
// crossing module-root directories with //go:embed.
package hub

import "embed"

//go:embed migrations/*.sql
var MigrationsFS embed.FS

//go:embed all:templates
var TemplatesFS embed.FS
