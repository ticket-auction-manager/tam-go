// Package version holds the build version both programs report. build.sh
// and the release workflow stamp it at link time with
// -ldflags "-X ticket-auction-manager/tam-go/internal/version.Version=v1.2.3".
package version

// Version is the program version, "0.0.1" when built without stamping.
var Version = "0.0.1"
