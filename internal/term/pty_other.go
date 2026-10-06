//go:build !windows

package term

import "errors"

// A pseudo-console off Windows needs a pty library this module does not
// carry yet; the web terminal says so rather than pretending.
func start(Spec) (pty, error) {
	return nil, errors.New("the web terminal runs on Windows hosts for now")
}
