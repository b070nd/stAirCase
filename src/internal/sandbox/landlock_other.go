//go:build !linux

package sandbox

import "errors"

func landlockWrapper(string, string, []string) ([]string, error) {
	return nil, errors.New("landlock is Linux only")
}
