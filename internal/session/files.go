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
	"unicode/utf8"

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
		var h proto.FileHeader
		var body []byte
		switch req.Op {
		case "":
			h, body = ReadPath(s.info.Cwd, req.Path, req.Stat, s.opts.FileDeny)
		case proto.FileOpStatus:
			h = GitStatusPath(s.info.Cwd, req.Base, s.opts.FileDeny)
		case proto.FileOpShow:
			h, body = GitShowPath(s.info.Cwd, req.Rev, req.Path, s.opts.FileDeny)
		case proto.FileOpLog:
			h = GitLogPath(s.info.Cwd, req.Base, s.info.CreatedAt, s.opts.FileDeny)
		case proto.FileOpCommit:
			h = GitCommitPath(s.info.Cwd, req.Rev, s.opts.FileDeny)
		default:
			h = proto.FileHeader{Path: req.Path, Kind: "error", Error: &proto.ErrorInfo{Code: "bad_request", Message: "unknown file operation"}}
		}
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
// resolved target must stay under root after symlink evaluation and must not
// be, or be inside, an entry of deny. When statOnly is set only existence and
// kind are reported.
func ReadPath(root, raw string, statOnly bool, deny []string) (proto.FileHeader, []byte) {
	h := proto.FileHeader{Path: raw}
	target, err := ResolvePath(root, raw, deny)
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
// and rejecting anything that escapes root, enters .git/objects or is one of
// the deny entries or inside one (the server passes its data directory, its
// config file and its catalog file, and a name entry for each of the two files,
// see insideAny).
func ResolvePath(root, raw string, deny []string) (string, error) {
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
	if insideAny(real, deny) {
		return "", errors.New("path is not readable")
	}
	return real, nil
}

// insideAny reports whether real, an absolute path with its symlinks resolved,
// is one of deny or inside one. An entry is a directory, which denies
// everything in it, or a single file, which only real itself can match.
// Entries are compared as files (os.SameFile), not by name, so neither a
// symlink to one, a hard link to a denied file, nor another spelling on a
// case-insensitive file system gets around the rule. An entry whose last
// element is "*name*" is a name entry: it denies, in the directory it names,
// every file or directory whose name contains name, folded the way a
// case-insensitive file system folds it (see containsFold).
// "/etc/conductor/*conductor.json*" denies conductor.json.bak, Conductor.JSON~,
// .conductor.json.swp and #conductor.json# there, and the directory is compared
// as a file too. A name entry matches by name alone, so a hard link under an
// unrelated name is not caught, and a "*" in any other place of an entry is not
// a wildcard: a literal "*" in a path is not supported. An entry that does not
// exist, or whose directory does not, denies nothing and is skipped.
func insideAny(real string, deny []string) bool {
	type nameEntry struct {
		dir  os.FileInfo
		name string
	}
	var denied []os.FileInfo
	var names []nameEntry
	for _, d := range deny {
		if name, ok := nameOf(filepath.Base(d)); ok {
			if fi, err := os.Stat(filepath.Dir(d)); err == nil {
				names = append(names, nameEntry{fi, name})
			}
			continue
		}
		if fi, err := os.Stat(d); err == nil {
			denied = append(denied, fi)
		}
	}
	if len(denied) == 0 && len(names) == 0 {
		return false
	}
	// Every ancestor counts, above root too: a session may run inside one.
	for p := real; ; p = filepath.Dir(p) {
		if len(denied) > 0 {
			if fi, err := os.Stat(p); err == nil {
				for _, d := range denied {
					if os.SameFile(fi, d) {
						return true
					}
				}
			}
		}
		// The name decides first; the directory is looked at only for a match.
		if base := filepath.Base(p); len(names) > 0 {
			var dir os.FileInfo
			for _, n := range names {
				if !containsFold(base, n.name) {
					continue
				}
				if dir == nil {
					fi, err := os.Stat(filepath.Dir(p))
					if err != nil {
						break
					}
					dir = fi
				}
				if os.SameFile(dir, n.dir) {
					return true
				}
			}
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

// nameOf returns the name of a "*name*" element: at least one character
// between two stars.
func nameOf(elem string) (string, bool) {
	if len(elem) < 3 || elem[0] != '*' || elem[len(elem)-1] != '*' {
		return "", false
	}
	return elem[1 : len(elem)-1], true
}

// containsFold reports whether s contains sub, comparing rune by rune with
// strings.EqualFold. That is simple Unicode case folding, which is what a
// case-insensitive file system applies and is wider than lower-casing both
// sides: the long s (U+017F) folds to s and the Kelvin sign (U+212A) to k.
// Folding keeps the number of runes, so the window of s that is compared with
// sub always holds as many runes as sub, though not as many bytes.
func containsFold(s, sub string) bool {
	n := utf8.RuneCountInString(sub)
	for i := 0; ; {
		j, c := i, 0
		for c < n && j < len(s) {
			_, w := utf8.DecodeRuneInString(s[j:])
			j += w
			c++
		}
		if c < n {
			return false
		}
		if strings.EqualFold(s[i:j], sub) {
			return true
		}
		if i == len(s) {
			return false
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
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
