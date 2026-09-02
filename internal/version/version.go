// Package version exposes the build-time version string for the
// cluster-rebootstrap CLI.
//
// Version is a dev sentinel by default. Release builds overwrite it via
// linker flags, e.g.:
//
//	go build -ldflags "-X github.com/bayleafwalker/cluster-rebootstrap/internal/version.Version=v0.1.0"
//
// A binary reporting the dev sentinel was not produced by the pinned
// release pipeline and must not be treated as recovery tooling for G3.
package version

// Version is the semantic version of this build. It defaults to a dev
// sentinel that is never produced by the release pipeline, so any binary
// still reporting it is provably not a pinned release artifact.
var Version = "dev"
