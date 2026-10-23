//go:build unix && !linux && !darwin

package proc

import "errors"

func watchPID(int, func()) (func(), error) {
	return nil, errors.New("proc: no exit watch on this platform")
}
