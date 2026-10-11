package session

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/phenixrizen/conductor/internal/proto"
)

// Limits for file reads.
const (
	MaxFileRead      = proto.MaxFileBytes
	MaxDirEntries    = 2000
	MaxInflightFiles = 4
	binarySniffBytes = 8192
	// MaxFileFrameBody is the most of a file a FILE frame carries to a
	// client (FileGet): what a client's queue takes (HighWaterMark) less the
	// largest header (proto.MaxFileHeader) and the frame's own 13 bytes, so
	// that the frame, whatever its header, fits a queue that holds nothing
	// else instead of dropping the client that asked as a slow consumer.
	MaxFileFrameBody = HighWaterMark - proto.MaxFileHeader - 13
)

// fitFrame cuts a body longer than MaxFileFrameBody to its first
// MaxFileFrameBody bytes, marked truncated. The whole file's hash and time
// go with it: they say what a save may replace, and a file read in part is
// not saved.
func fitFrame(h proto.FileHeader, body []byte) (proto.FileHeader, []byte) {
	if len(body) <= MaxFileFrameBody {
		return h, body
	}
	h.Truncated = true
	h.Sha256, h.Mtime = "", ""
	return h, body[:MaxFileFrameBody]
}

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

// fileDeny is the deny list for one file request: FileDenyFunc's when it is
// set, else FileDeny.
func (s *Local) fileDeny() []string {
	if s.opts.FileDenyFunc != nil {
		return s.opts.FileDenyFunc()
	}
	return s.opts.FileDeny
}

// FileGet serves a file or directory read for sub. It enforces the role
// policy, an in-flight cap, and resolves the path inside the session cwd,
// read through the session's folder (Folder). The response is queued to the
// subscriber as a FILE frame.
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
		deny := s.fileDeny()
		switch req.Op {
		case "":
			h, body = s.folder.read(req.Path, req.Stat, deny, sub.Role)
		case proto.FileOpStatus, proto.FileOpShow, proto.FileOpLog, proto.FileOpCommit:
			// Git reads the files itself, by path: the session's path
			// must still name its folder for them too.
			if err := s.folder.check(); err != nil {
				h = deniedHeader(req.Path, err)
				break
			}
			switch req.Op {
			case proto.FileOpStatus:
				h = GitStatusPath(s.info.Cwd, req.Base, deny)
			case proto.FileOpShow:
				h, body = GitShowPath(s.info.Cwd, req.Rev, req.Path, deny, sub.Role)
			case proto.FileOpLog:
				h = GitLogPath(s.info.Cwd, req.Base, s.info.CreatedAt, deny)
			case proto.FileOpCommit:
				h = GitCommitPath(s.info.Cwd, req.Rev, deny)
			}
		case proto.FileOpFind:
			h = s.folder.find(req.Path, deny)
		case proto.FileOpTouched:
			h = s.TouchedPath(deny)
		default:
			h = proto.FileHeader{Path: req.Path, Kind: "error", Error: &proto.ErrorInfo{Code: "bad_request", Message: "unknown file operation"}}
		}
		h.ReqID = req.ReqID
		h, body = fitFrame(h, body)
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
// kind are reported. root is opened as a folder for this one read, a
// controller's; a session reads through the folder it holds, for the role
// that asks (Local.ReadPath).
func ReadPath(root, raw string, statOnly bool, deny []string) (proto.FileHeader, []byte) {
	f, err := OpenFolder(root)
	if err != nil {
		return proto.FileHeader{Path: raw, Kind: "error", Error: &proto.ErrorInfo{Code: "denied", Message: errFolderGone.Error()}}, nil
	}
	defer f.Close()
	return f.read(raw, statOnly, deny, RoleControl)
}

// ReadPath is the package's ReadPath through the session's own folder, for
// a viewer of role: one that is not a controller is not given git's
// settings and credentials (forControllers).
func (s *Local) ReadPath(raw string, statOnly bool, deny []string, role Role) (proto.FileHeader, []byte) {
	return s.folder.read(raw, statOnly, deny, role)
}

// read is ReadPath in the folder for a viewer of role: raw checked
// (resolve, and forControllers unless role is control), then what it
// names opened from the folder one element at a time (openDir, stepDir,
// openFileIn), so that what is read, stat'ed or listed is the folder's file
// the check passed, or nothing.
func (f *Folder) read(raw string, statOnly bool, deny []string, role Role) (proto.FileHeader, []byte) {
	h := proto.FileHeader{Path: raw}
	fail := func(code, msg string) (proto.FileHeader, []byte) {
		h.Kind, h.Exists, h.Size = "error", false, 0
		h.Error = &proto.ErrorInfo{Code: code, Message: msg}
		return h, nil
	}
	// The deny list as looked up once: the check and the use hold the path
	// to the same files, wherever they are moved meanwhile.
	ds := newDenySet(deny)
	target, rel, err := f.resolve(raw, ds)
	if err != nil {
		return fail("denied", err.Error())
	}
	// Whether target is in a repository's .git, looked at once: a view
	// link is not given it, and a save of it would be refused (saveFile),
	// so the editor shows Read only.
	inGit, looked := false, false
	isGit := func() bool {
		if !looked {
			looked, inGit = true, gitMetadata(raw, target)
		}
		return inGit
	}
	// For a view link (forControllers), what the repositories beside the
	// path stood for at its check (gitMeta) is held to each directory and
	// to the file as they are opened, as a save holds it, and git's
	// settings in the home join the deny set, so that metadata moved into
	// the path's place since is refused too.
	var check func(*os.Root) error
	var snap gitMeta
	if role != RoleControl {
		held := newDenySet(userGitFiles())
		in, s := gitMetadataIDs(raw, target)
		inGit, looked = in, true
		if forControllers(raw, target, held, isGit) {
			return fail(proto.ErrCodeFileDenied, errForControllers.Error())
		}
		snap, check = s, refuseGit(s)
		ds = ds.and(held)
	}
	h.Path = target
	afterCheck()
	// failOpen answers a path that changed or turned denied since its check
	// as denied, one that turned into a repository's .git as a view link's
	// refusal, and one whose file went as not found.
	failOpen := func(err error) (proto.FileHeader, []byte) {
		if errors.Is(err, errGitDir) {
			return fail(proto.ErrCodeFileDenied, errForControllers.Error())
		}
		if errors.Is(err, errPathChanged) || errors.Is(err, errNotReadable) || errors.Is(err, errFolderGone) {
			return fail("denied", err.Error())
		}
		return fail("not_found", "no such file or directory")
	}
	var dir *os.Root // a directory to list
	var fi os.FileInfo
	if rel == "." {
		// The folder itself.
		if dir, fi, err = f.openDirChecked(".", ds, check); err != nil {
			return failOpen(err)
		}
		defer dir.Close()
	} else {
		parent, pid, err := f.openDirChecked(filepath.Dir(rel), ds, check)
		if err != nil {
			return failOpen(err)
		}
		defer parent.Close()
		name := filepath.Base(rel)
		if fi, err = parent.Lstat(name); err != nil || fi.Mode()&os.ModeSymlink != 0 {
			// A link here leads nowhere: the check resolves the others.
			return fail("not_found", "no such file or directory")
		}
		if ds.hit(fi, name, pid) {
			return fail("denied", errNotReadable.Error())
		}
		if check != nil && (sameAsAny(fi, snap.files) || snap.makes(pid, name)) {
			return failOpen(errGitDir)
		}
		switch {
		case statOnly:
		case fi.IsDir():
			if dir, fi, err = stepDir(parent, pid, name, ds); err != nil {
				return failOpen(err)
			}
			defer dir.Close()
			if check != nil {
				if err := check(dir); err != nil {
					return failOpen(err)
				}
			}
		case fi.Mode().IsRegular():
			file, got, err := openFileIn(parent, name, fi)
			if err != nil {
				return failOpen(err)
			}
			defer file.Close()
			return readFile(h, file, got, isGit())
		}
	}
	h.Exists = true
	h.Size = fi.Size()
	if fi.IsDir() {
		h.Kind = "dir"
		if statOnly {
			return h, nil
		}
		if h.Entries, h.Truncated, err = listDir(dir); err != nil {
			return fail("read", "cannot list directory")
		}
		return h, nil
	}
	if !fi.Mode().IsRegular() {
		h.Kind = "error"
		h.Error = &proto.ErrorInfo{Code: "unsupported", Message: "not a regular file"}
		return h, nil
	}
	// A regular file, stat'ed only.
	h.Kind = "file"
	h.Mime = mime.TypeByExtension(filepath.Ext(h.Path))
	h.ReadOnly = isGit()
	return h, nil
}

// readFile completes h for file, open and checked (its stat fi): its first
// MaxFileRead bytes, none for a binary file; readOnly when it is in a
// repository's .git, whose save would be refused (saveFile), so the editor
// shows Read only.
func readFile(h proto.FileHeader, file *os.File, fi os.FileInfo, readOnly bool) (proto.FileHeader, []byte) {
	h.Exists = true
	h.Size = fi.Size()
	h.Kind = "file"
	h.Mime = mime.TypeByExtension(filepath.Ext(h.Path))
	h.ReadOnly = readOnly
	body, err := io.ReadAll(io.LimitReader(file, MaxFileRead+1))
	if err != nil {
		h.Kind, h.Exists, h.Size = "error", false, 0
		h.Error = &proto.ErrorInfo{Code: "read", Message: "cannot read file"}
		return h, nil
	}
	if len(body) > MaxFileRead {
		body = body[:MaxFileRead]
		h.Truncated = true
	} else {
		// What a save sends back to tell the file changed since (F6).
		h.Sha256 = sha256Hex(body)
		h.Mtime = fi.ModTime().UTC().Format(time.RFC3339Nano)
	}
	sniff := body[:min(len(body), binarySniffBytes)]
	if bytes.IndexByte(sniff, 0) >= 0 && !strings.HasPrefix(h.Mime, "image/") {
		h.Binary = true
		return h, nil
	}
	return h, body
}

// maxListScan is the entries a listing reads at most (a test lowers it),
// each name counted as it is read and then looked at through its folder
// (readFolder): a listing of a folder with more sorts the first
// maxListScan it reads and says it is truncated.
var maxListScan = 10 * MaxDirEntries

// listDir lists dir, open: directories first, then by name, at most
// MaxDirEntries of the first maxListScan entries read (truncated when
// there are more), each entry looked up through dir, not by a path that
// could lead elsewhere by then.
func listDir(dir *os.Root) (entries []proto.FileEntry, truncated bool, err error) {
	budget := maxListScan
	all, complete, err := readFolder(dir, &budget, func() bool { return false })
	if err != nil {
		return nil, false, err
	}
	truncated = !complete
	sort.Slice(all, func(i, j int) bool {
		if all[i].fi.IsDir() != all[j].fi.IsDir() {
			return all[i].fi.IsDir()
		}
		return all[i].name < all[j].name
	})
	for i, e := range all {
		if i >= MaxDirEntries {
			truncated = true
			break
		}
		entries = append(entries, proto.FileEntry{Name: e.name, Dir: e.fi.IsDir(), Size: e.fi.Size()})
	}
	return entries, truncated, nil
}

// ResolvePath turns raw into an absolute path under root, following symlinks
// and rejecting anything that escapes root, enters .git/objects or is one of
// the deny entries or inside one (the server passes its data directory, its
// config file and its catalog file, and a name entry for each of the two files,
// see insideAny).
func ResolvePath(root, raw string, deny []string) (string, error) {
	real, _, err := resolvePath(root, raw, newDenySet(deny))
	return real, err
}

// resolvePath is ResolvePath with the deny list looked up already (a
// request looks it up once, for its check and its use alike), returning
// root with its links resolved too, as the check took it.
func resolvePath(root, raw string, deny denySet) (real, rootReal string, err error) {
	if raw == "" {
		raw = "."
	}
	if strings.ContainsRune(raw, 0) {
		return "", "", errors.New("path contains NUL")
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", errors.New("home directory unavailable")
		}
		raw = filepath.Join(home, strings.TrimPrefix(raw, "~"))
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	rootReal, err = filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", errFolderGone
	}
	var target string
	if filepath.IsAbs(raw) {
		target = filepath.Clean(raw)
	} else {
		target = filepath.Join(rootAbs, raw)
	}
	// Evaluate the deepest existing prefix so a missing final component still
	// reports not_found instead of leaking whether directories exist elsewhere.
	real, err = evalExisting(target)
	if err != nil {
		return "", "", errors.New("path unavailable")
	}
	if real != rootReal && !strings.HasPrefix(real, rootReal+string(filepath.Separator)) {
		return "", "", errors.New("path is outside the session directory")
	}
	rel, _ := filepath.Rel(rootReal, real)
	parts := strings.Split(rel, string(filepath.Separator))
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == ".git" && parts[i+1] == "objects" {
			return "", "", errNotReadable
		}
	}
	if deny.inside(real) {
		return "", "", errNotReadable
	}
	return real, rootReal, nil
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
// exist, or whose directory does not, is compared by its path: below the
// deepest directory on it that exists, compared as a file, its elements
// match real's ignoring case (missingEntry), so that what a revision keeps
// at a path the list names is refused though the working tree no longer
// has it. A request looks the entries up once (newDenySet) and holds what
// it opens to them by identity too (denySet.hit), so an entry moved since
// its check is still denied.
func insideAny(real string, deny []string) bool {
	return newDenySet(deny).inside(real)
}

// inside is insideAny for the entries d holds.
func (d denySet) inside(real string) bool {
	if d.empty() {
		return false
	}
	// Every ancestor counts, above root too: a session may run inside one.
	for p := real; ; p = filepath.Dir(p) {
		var fi, dir os.FileInfo
		if len(d.files) > 0 || len(d.missing) > 0 {
			fi, _ = os.Stat(p)
		}
		if fi != nil && len(d.missing) > 0 && d.below(real, p, fi) {
			return true
		}
		// The name decides first; the directory is looked at only for a match.
		base := filepath.Base(p)
		if slices.ContainsFunc(d.names, func(n nameEntry) bool { return containsFold(base, n.name) }) {
			dir, _ = os.Stat(filepath.Dir(p))
		}
		if d.hit(fi, base, dir) {
			return true
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

// sameName reports whether a and b are the same name as containsFold
// compares names: ignoring case and the characters HFS+ leaves out.
func sameName(a, b string) bool {
	return strings.EqualFold(foldName(a), foldName(b))
}

// containsFold reports whether s contains sub, comparing rune by rune with
// strings.EqualFold. That is simple Unicode case folding, which is what a
// case-insensitive file system applies and is wider than lower-casing both
// sides: the long s (U+017F) folds to s and the Kelvin sign (U+212A) to k.
// Folding keeps the number of runes, so the window of s that is compared with
// sub always holds as many runes as sub, though not as many bytes. The
// characters HFS+ leaves out of names are left out of both first
// (foldName), so a spelling that file system takes for the name holds it.
func containsFold(s, sub string) bool {
	s, sub = foldName(s), foldName(sub)
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
