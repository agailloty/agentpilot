// Package version exposes the build version injected by GoReleaser via
// -ldflags "-X github.com/agailloty/agentpilot/server/internal/version.Version=x.y.z".
package version

// Version is overridden at build time; "dev" is the default for local builds.
var Version = "dev"
