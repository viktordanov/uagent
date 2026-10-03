//go:build !darwin && !linux

package procstart

import "errors"

func read(int) (boot, start string, err error) {
	return "", "", errors.ErrUnsupported
}
