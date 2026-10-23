//go:build unix && !linux && !darwin

package proc

func watchPID(int, func()) (func(), bool) { return nil, false }
