package session

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/proto"
)

// The Explorer's filter reaches folders not opened yet (design round 12):
// the files under the working directory whose name holds the query, found
// by a bounded walk under the same deny list as a read.
const (
	MaxFindMatches = 200
	MaxFindScan    = 20000
	MaxFindQuery   = 200
	findTimeout    = 3 * time.Second
)

// errFindStop ends the walk early.
var errFindStop = errors.New("stop")

// FindPath walks root for files whose name holds query (case-insensitive),
// skipping .git, node_modules and what the deny list refuses, and answers a
// FILE header of kind `find`: Path the root, Matches the paths from it,
// Truncated when the walk stopped at a bound (200 matches, 20,000 entries,
// 3 s, the header's size).
func FindPath(root, query string, deny []string) proto.FileHeader {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" || len(q) > MaxFindQuery {
		return proto.FileHeader{Path: root, Kind: "error", Error: &proto.ErrorInfo{Code: "bad_request", Message: "a find needs its words, at most 200 bytes"}}
	}
	base, err := ResolvePath(root, ".", deny)
	if err != nil {
		return deniedHeader(root, err)
	}
	h := proto.FileHeader{Path: base, Kind: "find", Exists: true}
	deadline := time.Now().Add(findTimeout)
	budget := proto.MaxFileHeader - 4096
	scanned, used := 0, 0
	walkErr := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != base {
				return fs.SkipDir
			}
			return nil
		}
		if p == base {
			return nil
		}
		scanned++
		if scanned > MaxFindScan || time.Now().After(deadline) {
			h.Truncated = true
			return errFindStop
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "node_modules" || insideAny(p, deny) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.Contains(strings.ToLower(name), q) || insideAny(p, deny) {
			return nil
		}
		rel, err := filepath.Rel(base, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if used+len(rel)+8 > budget || len(h.Matches) == MaxFindMatches {
			h.Truncated = true
			return errFindStop
		}
		used += len(rel) + 8
		h.Matches = append(h.Matches, rel)
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errFindStop) {
		h.Truncated = true
	}
	return h
}
