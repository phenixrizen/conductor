//go:build !unix

package ledger

import "time"

type ownerLock struct{}

func takeOwnerLock(path string, wait time.Duration) (*ownerLock, string, error) {
	return nil, "the owner lock is not supported on this platform", nil
}

func (*ownerLock) release() {}
