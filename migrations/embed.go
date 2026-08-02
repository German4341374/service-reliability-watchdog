package migrations

import "embed"

// Files contains ordered, idempotent SQL migrations.
//
//go:embed *.sql
var Files embed.FS
