//go:build !unix

package ledger

type ownerLock struct{}

func takeOwnerLock(path string) (*ownerLock, string, bool, error) {
	return nil, "the owner lock is not supported on this platform", false, nil
}

func (*ownerLock) release() {}
