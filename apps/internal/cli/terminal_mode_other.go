//go:build !darwin && !netbsd && !freebsd && !openbsd && !linux && !dragonfly && !solaris

package cli

import "os"

func silenceTerminalEcho(_ *os.File) (func() error, error) {
	return func() error { return nil }, nil
}

func screenResizeSignals() (<-chan os.Signal, func()) {
	return nil, func() {}
}
