//go:build windows

package watch

import "errors"

func processCommandLine(pid int) (string, error) {
	_ = pid
	return "", errors.New("process command line lookup not supported on windows")
}
