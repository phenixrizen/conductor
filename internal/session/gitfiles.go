package session

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/gitcli"
	"github.com/phenixrizen/conductor/internal/proto"
)

// The git reads a viewer asks for through the file request (design 4d,
// 4e): the working directory's status and a file at a revision, under the
// same policy and deny list as a read, with the git binary where the
// session runs (the server, or the developer's machine for a hosted one).

// GitTimeout bounds one git read.
const GitTimeout = 20 * time.Second

// GitStatusPath is root's status against base as a FILE header of kind
// `status`: Path the working tree's top (the changes' paths are relative
// to it), the branch, the base's id, the changes with their lines and the
// totals; kind `error` with code `not_repo` outside a repository, `denied`
// for a root in the deny list.
func GitStatusPath(root, base string, deny []string) proto.FileHeader {
	h := proto.FileHeader{Path: root, Kind: "status", Exists: true}
	if _, err := ResolvePath(root, ".", deny); err != nil {
		return deniedHeader(root, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	st, err := gitcli.GetStatus(ctx, root, base)
	if err != nil {
		return gitError(root, err)
	}
	h.Path = st.Top
	h.Branch, h.Base, h.Added, h.Removed, h.Truncated = st.Branch, st.Base, st.Added, st.Removed, st.Truncated
	h.Changes = make([]proto.Change, 0, len(st.Changes))
	// The header has a frame-size bound (proto.MaxFileHeader): long paths stop the list early, said with Truncated.
	budget := proto.MaxFileHeader - 4096
	used := 0
	for _, c := range st.Changes {
		cost := len(c.Path) + 72
		if used+cost > budget {
			h.Truncated = true
			break
		}
		used += cost
		h.Changes = append(h.Changes, proto.Change{Path: c.Path, Status: c.Status, Added: c.Added, Removed: c.Removed, Binary: c.Binary})
	}
	return h
}

// GitShowPath is the file at path (relative to root, or absolute under it)
// at rev, as a FILE header of kind `show` with the content as the body,
// through ResolvePath so the deny list holds for what a revision keeps too.
func GitShowPath(root, rev, path string, deny []string) (proto.FileHeader, []byte) {
	// ResolvePath follows what exists of the path, so a file the working tree no longer has (deleted) resolves too.
	target, err := ResolvePath(root, path, deny)
	if err != nil {
		return deniedHeader(path, err), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), GitTimeout)
	defer cancel()
	top, err := gitcli.Run(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return gitError(path, err), nil
	}
	rel, err := filepath.Rel(strings.TrimSpace(top), target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return deniedHeader(path, errors.New("outside the repository")), nil
	}
	body, truncated, err := gitcli.Show(ctx, root, rev, filepath.ToSlash(rel))
	if err != nil {
		return gitError(path, err), nil
	}
	return proto.FileHeader{Path: target, Kind: "show", Exists: true, Rev: rev, Size: int64(len(body)), Truncated: truncated, Binary: looksBinary(body)}, body
}

func deniedHeader(path string, err error) proto.FileHeader {
	return proto.FileHeader{Path: path, Kind: "error", Error: &proto.ErrorInfo{Code: "denied", Message: err.Error()}}
}

// looksBinary: a NUL among the first bytes, as a read's detection goes.
func looksBinary(b []byte) bool {
	head := b
	if len(head) > 8192 {
		head = head[:8192]
	}
	return bytes.IndexByte(head, 0) >= 0
}

func gitError(path string, err error) proto.FileHeader {
	code := "git"
	if errors.Is(err, gitcli.ErrNotRepo) {
		code = "not_repo"
	}
	return proto.FileHeader{Path: path, Kind: "error", Error: &proto.ErrorInfo{Code: code, Message: err.Error()}}
}
