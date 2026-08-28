// Package version is the single source of truth for VERIQO's release
// version, closing audit item P0-12 ("Eliminate Version Drift"): prior
// rounds hardcoded "v7.12.0" independently in cmd/veriqo-readiness's
// flag default, pkg/execution's Engine.Version field, and two shell
// scripts' fallback defaults -- four places that could silently drift
// out of sync with each other and with the actual v7.12.1 release,
// exactly the failure mode the audit found.
//
// Current is embedded from internal/version/VERSION via go:embed (see
// that field's doc comment for why it isn't the repository-root VERSION
// file directly). Every other operative version site in this repository
// must derive from Current rather than hold its own literal.
package version

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var raw string

// Current is the release version, trimmed of the trailing newline the
// VERSION file carries.
//
// The embed directive above cannot reach outside this package's
// directory, so this file embeds internal/version/VERSION rather than
// the repository-root VERSION file directly; version_test.go asserts
// the two are byte-for-byte identical, so an edit to one without the
// other fails the build rather than silently drifting -- the
// CI-enforced check P0-12 asked for, just implemented as a comparison
// instead of a single file.
var Current = strings.TrimSpace(raw)
