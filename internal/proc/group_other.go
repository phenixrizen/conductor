//go:build unix && !linux && !darwin

package proc

import "errors"

// groupMembers cannot list a process group's processes here.
func groupMembers(int, int) ([]int, error) {
	return nil, errors.New("proc: no listing of a process group on this platform")
}

func stillMembers(int, []int) []int { return nil }
