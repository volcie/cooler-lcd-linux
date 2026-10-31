package app

import "errors"

// Exit codes, so systemd can tell a transient condition (device unplugged,
// restart and retry) from a permanent one (bad config, do not retry).
const (
	exitOK    = 0
	exitError = 1 // transient: retry is reasonable
	exitUsage = 2 // bad config or flags: retrying cannot help
)

// errConfig marks errors that repeated restarts will never fix.
var errConfig = errors.New("configuration error")

// isPermanent reports whether err should stop systemd from restarting.
func isPermanent(err error) bool {
	return errors.Is(err, errConfig) || errors.Is(err, errNotInstalled)
}

// errNotInstalled means the service unit is missing, which a restart cannot fix.
var errNotInstalled = errors.New("service unit not installed")
