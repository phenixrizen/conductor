package api

import (
	"cmp"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/phenixrizen/conductor/internal/crew"
)

// The working-directory pickers (the crew editor and the Launch dialog)
// complete what is typed from GET /api/paths and show whether a crew with
// worktrees could launch there from GET /api/git/check. Both say what exists
// under the allowed roots and nothing else: a prefix is resolved as a
// session's working directory is (resolveCwd), so a symbolic link out of the
// roots leads nowhere.

const (
	// maxPathEntries bounds one listing: a page for a picker, not a file system.
	maxPathEntries = 50
	// maxPathQuery bounds the prefix and cwd query values, as a crew's cwd is bounded.
	maxPathQuery = 4096
	// gitCheckTimeout bounds the git calls of one request, which run one at a time.
	gitCheckTimeout = 5 * time.Second
	// pathReadChunk is how many directory entries one read takes.
	pathReadChunk = 256
)

// maxPathScan bounds the directory entries one listing reads, before they
// are sorted and cut to the limit: a directory of a million entries costs
// no more than one of maxPathScan. A test lowers it.
var maxPathScan = 2000

// gitState is crew.GitState, which a test replaces to count the calls.
var gitState = crew.GitState

// pathGit marks a listed directory: in a git working tree, and one whose
// HEAD is a commit, which a crew with worktrees needs.
type pathGit struct {
	Repo    bool `json:"repo"`
	Commits bool `json:"commits"`
}

type pathEntry struct {
	Name string  `json:"name"`
	Path string  `json:"path"`
	Git  pathGit `json:"git"`
}

type pathsReply struct {
	Dir       string      `json:"dir"`
	Entries   []pathEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

type gitCheckReply struct {
	InRepo    bool   `json:"inRepo"`
	Toplevel  string `json:"toplevel,omitempty"`
	HasCommit bool   `json:"hasCommit"`
	Message   string `json:"message"`
}

// errNoAllowedDir says that no leading part of a prefix is a directory under
// the allowed roots.
var errNoAllowedDir = errors.New("no directory in the prefix is under the allowed roots")

// splitPrefix finds the longest leading part of prefix that resolveCwd
// accepts (an existing directory under an allowed root, symbolic links
// resolved) and the path element typed after it, "" when prefix names such
// a directory itself. A lone "." after the last separator is the start of a
// hidden name, which filepath.Clean would drop. An empty prefix is the
// server's default working directory. A prefix no part of which qualifies is
// errNoAllowedDir.
func (s *Server) splitPrefix(prefix string) (dir, seg string, err error) {
	raw := cmp.Or(prefix, s.cfg.DefaultCwd)
	if strings.HasSuffix(raw, string(filepath.Separator)+".") {
		dir, seg, err := s.splitPrefix(strings.TrimSuffix(raw, "."))
		if err == nil && seg == "" {
			seg = "."
		}
		return dir, seg, err
	}
	cur := filepath.Clean(raw)
	for {
		if real, err := s.resolveCwd(cur); err == nil {
			return real, seg, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", "", errNoAllowedDir
		}
		seg = filepath.Base(cur)
		cur = parent
	}
}

// candidate is a directory entry that may be listed: its name matches what
// is typed, and it is a directory or a symbolic link.
type candidate struct {
	name string
	link bool
}

// readCandidates reads at most maxPathScan entries of dir, pathReadChunk at a
// time, and keeps the directories and symbolic links whose names start with
// seg, hidden ones only when seg starts with a dot. capped reports that the
// directory has entries it did not read.
func readCandidates(dir, seg string) (out []candidate, capped bool, err error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	for read := 0; read < maxPathScan; {
		des, err := f.ReadDir(min(pathReadChunk, maxPathScan-read))
		read += len(des)
		for _, e := range des {
			name := e.Name()
			if !strings.HasPrefix(name, seg) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(seg, ".")) {
				continue
			}
			switch {
			case e.IsDir():
				out = append(out, candidate{name: name})
			case e.Type()&os.ModeSymlink != 0:
				out = append(out, candidate{name: name, link: true})
			}
		}
		if errors.Is(err, io.EOF) {
			return out, false, nil
		}
		if err != nil {
			return nil, false, err
		}
	}
	return out, true, nil
}

// listPaths lists the child directories of the directory prefix names (see
// splitPrefix) whose names start with the element typed after it: at most
// limit of them in name order, from at most maxPathScan entries read, hidden
// ones only when that element starts with a dot, a symbolic link only when it
// leads under an allowed root. A child with a .git of its own is asked git
// for its marks; the others carry their parent's, asked once and only when
// needed, so one listing runs GitState at most limit times, one after another.
func (s *Server) listPaths(ctx context.Context, prefix string, limit int) (pathsReply, error) {
	dir, seg, err := s.splitPrefix(prefix)
	if err != nil {
		return pathsReply{}, err
	}
	cands, capped, err := readCandidates(dir, seg)
	if err != nil {
		return pathsReply{}, err
	}
	slices.SortFunc(cands, func(a, b candidate) int { return strings.Compare(a.name, b.name) })
	out := pathsReply{Dir: dir, Entries: []pathEntry{}, Truncated: capped}
	var parent *pathGit
	for _, c := range cands {
		path := filepath.Join(dir, c.name)
		if c.link {
			if _, err := s.resolveCwd(path); err != nil {
				continue
			}
		}
		if len(out.Entries) == limit {
			out.Truncated = true
			break
		}
		var mark pathGit
		if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
			if mark, err = gitMark(ctx, path); err != nil {
				return pathsReply{}, err
			}
		} else {
			if parent == nil {
				m, err := gitMark(ctx, dir)
				if err != nil {
					return pathsReply{}, err
				}
				parent = &m
			}
			mark = *parent
		}
		out.Entries = append(out.Entries, pathEntry{Name: c.name, Path: path, Git: mark})
	}
	return out, nil
}

// gitMark is the git state of dir as the picker shows it. Without git on the
// server nothing is marked.
func gitMark(ctx context.Context, dir string) (pathGit, error) {
	st, err := gitState(ctx, dir)
	if errors.Is(err, crew.ErrNoGit) {
		return pathGit{}, nil
	}
	if err != nil {
		return pathGit{}, err
	}
	return pathGit{Repo: st.InRepo, Commits: st.HasCommit}, nil
}

// pathQuery reads a bounded path from the query, or answers the request.
func pathQuery(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	v := r.URL.Query().Get(key)
	if len(v) > maxPathQuery || strings.ContainsRune(v, 0) {
		writeError(w, http.StatusBadRequest, "invalid_request", key+" must be at most 4096 bytes without NUL")
		return "", false
	}
	return v, true
}

// handleListPaths answers GET /api/paths?prefix=<path>&limit=<n>. What a
// client asks about is logged at debug only.
func (s *Server) handleListPaths(w http.ResponseWriter, r *http.Request) {
	prefix, ok := pathQuery(w, r, "prefix")
	if !ok {
		return
	}
	limit := maxPathEntries
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit must be a positive integer")
			return
		}
		limit = min(n, maxPathEntries)
	}
	ctx, cancel := context.WithTimeout(r.Context(), gitCheckTimeout)
	defer cancel()
	reply, err := s.listPaths(ctx, prefix, limit)
	switch {
	case errors.Is(err, errNoAllowedDir):
		writeError(w, http.StatusBadRequest, "invalid_cwd", err.Error())
		return
	case err != nil:
		s.log.Debug("list paths failed", "prefix", prefix, "err", err)
		writeError(w, http.StatusInternalServerError, "list_failed", "could not list the directory")
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

// handleGitCheck answers GET /api/git/check?cwd=<path>: whether a crew with
// worktrees could launch in cwd (the default working directory when empty),
// by the launch's own rules. A preview: the launch's 409 is the authority.
func (s *Server) handleGitCheck(w http.ResponseWriter, r *http.Request) {
	cwdInput, ok := pathQuery(w, r, "cwd")
	if !ok {
		return
	}
	cwd, err := s.resolveCwd(cmp.Or(cwdInput, s.cfg.DefaultCwd))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_cwd", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gitCheckTimeout)
	defer cancel()
	st, err := gitState(ctx, cwd)
	switch {
	case errors.Is(err, crew.ErrNoGit):
		writeJSON(w, http.StatusOK, gitCheckReply{Message: crew.ErrNoGit.Error()})
		return
	case err != nil:
		s.log.Debug("git check failed", "cwd", cwd, "err", err)
		writeError(w, http.StatusInternalServerError, "git_failed", "could not run git")
		return
	}
	writeJSON(w, http.StatusOK, gitCheckReply{InRepo: st.InRepo, Toplevel: st.Toplevel, HasCommit: st.HasCommit, Message: st.Message})
}
