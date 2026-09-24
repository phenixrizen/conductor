package session

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/phenixrizen/conductor/internal/proto"
)

// Limits for file reads.
const (
	MaxFileRead      = proto.MaxFileBytes
	MaxDirEntries    = 2000
	MaxInflightFiles = 4
	binarySniffBytes = 8192
)

// fileAllowed reports whether role may read files under the current policy.
func (s *Local) fileAllowed(role Role) bool {
	switch s.opts.FileView {
	case "view":
		return true
	case "control":
		return role == RoleControl
	}
	return false
}

// FileGet serves a file or directory read for sub. It enforces the role
// policy, an in-flight cap, and resolves the path inside the session cwd. The
// response is queued to the subscriber as a FILE frame.
func (s *Local) FileGet(sub *Subscription, req proto.FileGet) error {
	if !s.fileAllowed(sub.Role) {
		return ErrFileDenied
	}
	if req.ReqID == "" || len(req.ReqID) > 64 || len(req.Path) > 4096 {
		return errors.New("session: invalid file request")
	}
	if sub.inflight.Add(1) > MaxInflightFiles {
		sub.inflight.Add(-1)
		return ErrTooManyRequests
	}
	go func() {
		defer sub.inflight.Add(-1)
		h, body := ReadPath(s.info.Cwd, req.Path, req.Stat)
		h.ReqID = req.ReqID
		frame, err := proto.EncodeFile(h, body)
		if err != nil {
			frame, _ = proto.EncodeFile(proto.FileHeader{ReqID: req.ReqID, Path: req.Path, Kind: "error",
				Error: &proto.ErrorInfo{Code: "encode", Message: err.Error()}}, nil)
		}
		sub.send(frame)
	}()
	return nil
}

// ReadPath resolves raw against root and returns a header plus body. Paths may
// be absolute, relative to root, or start with "~" (the process home). The
// resolved target must stay under root after symlink evaluation. When statOnly
// is set only existence and kind are reported.
func ReadPath(root, raw string, statOnly bool) (proto.FileHeader, []byte) {
	h := proto.FileHeader{Path: raw}
	target, err := ResolvePath(root, raw)
	if err != nil {
		h.Kind = "error"
		h.Error = &proto.ErrorInfo{Code: "denied", Message: err.Error()}
		return h, nil
	}
	h.Path = target
	fi, err := os.Stat(target)
	if err != nil {
		h.Kind = "error"
		h.Error = &proto.ErrorInfo{Code: "not_found", Message: "no such file or directory"}
		return h, nil
	}
	h.Exists = true
	h.Size = fi.Size()
	if fi.IsDir() {
		h.Kind = "dir"
		if statOnly {
			return h, nil
		}
		entries, err := os.ReadDir(target)
		if err != nil {
			h.Kind = "error"
			h.Error = &proto.ErrorInfo{Code: "read", Message: "cannot list directory"}
			return h, nil
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].IsDir() != entries[j].IsDir() {
				return entries[i].IsDir()
			}
			return entries[i].Name() < entries[j].Name()
		})
		for i, e := range entries {
			if i >= MaxDirEntries {
				h.Truncated = true
				break
			}
			var size int64
			if info, err := e.Info(); err == nil {
				size = info.Size()
			}
			h.Entries = append(h.Entries, proto.FileEntry{Name: e.Name(), Dir: e.IsDir(), Size: size})
		}
		return h, nil
	}
	if !fi.Mode().IsRegular() {
		h.Kind = "error"
		h.Error = &proto.ErrorInfo{Code: "unsupported", Message: "not a regular file"}
		return h, nil
	}
	h.Kind = "file"
	h.Mime = mime.TypeByExtension(filepath.Ext(target))
	if statOnly {
		return h, nil
	}
	f, err := os.Open(target)
	if err != nil {
		h.Kind = "error"
		h.Error = &proto.ErrorInfo{Code: "read", Message: "cannot open file"}
		return h, nil
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, MaxFileRead+1))
	if err != nil {
		h.Kind = "error"
		h.Error = &proto.ErrorInfo{Code: "read", Message: "cannot read file"}
		return h, nil
	}
	if len(body) > MaxFileRead {
		body = body[:MaxFileRead]
		h.Truncated = true
	}
	sniff := body[:min(len(body), binarySniffBytes)]
	if bytes.IndexByte(sniff, 0) >= 0 && !strings.HasPrefix(h.Mime, "image/") {
		h.Binary = true
		return h, nil
	}
	return h, body
}

// ResolvePath turns raw into an absolute path under root, following symlinks
// and rejecting anything that escapes root or enters .git/objects.
func ResolvePath(root, raw string) (string, error) {
	if raw == "" {
		raw = "."
	}
	if strings.ContainsRune(raw, 0) {
		return "", errors.New("path contains NUL")
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("home directory unavailable")
		}
		raw = filepath.Join(home, strings.TrimPrefix(raw, "~"))
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootReal, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("session root unavailable")
	}
	var target string
	if filepath.IsAbs(raw) {
		target = filepath.Clean(raw)
	} else {
		target = filepath.Join(rootAbs, raw)
	}
	// Evaluate the deepest existing prefix so a missing final component still
	// reports not_found instead of leaking whether directories exist elsewhere.
	real, err := evalExisting(target)
	if err != nil {
		return "", errors.New("path unavailable")
	}
	if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
		return "", errors.New("path is outside the session directory")
	}
	rel, _ := filepath.Rel(rootReal, real)
	parts := strings.Split(rel, string(filepath.Separator))
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == ".git" && parts[i+1] == "objects" {
			return "", errors.New("path is not readable")
		}
	}
	return real, nil
}

func evalExisting(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		return real, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	dir, base := filepath.Split(filepath.Clean(p))
	if dir == "" || dir == p {
		return "", err
	}
	parent, err := evalExisting(filepath.Clean(dir))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, base), nil
}
