package session

import (
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/phenixrizen/conductor/internal/proto"
)

// A plan the agent writes outside the session's folder (#289): Claude Code
// saves its plans under its config directory (~/.claude/plans/<slug>.md, or
// under CLAUDE_CONFIG_DIR), where the folder does not reach. Options.Plans
// names that directory, from the agent's adapter and the environment it was
// started with (agents.PlansDir), never from a page. It is opened as a
// second folder (Folder: an os.Root, held to the same checks as the working
// directory, the deny list included) the first time the agent reports a
// file in it, and the files the agent reports there are the session's
// plans: its hooks' file events, the plan its approval names (the hook's
// planFilePath, carried by the report that sets the question:
// SetAttentionPlan), and the path its dialog prints (carried by the
// dialog's own change), once that is seen to be a file there. A request's
// path is routed to the plans
// folder when it leads into the directory (folderFor), and the folder
// allows the file it resolves to only when it is one of the plans
// (Folder.allow, asked of the resolved path, which the folder then opens
// element by element with no link followed: so a link made to lead
// elsewhere between the two cannot reach what was not allowed). Any other
// path in the directory, another session's plan among them, or anywhere
// else outside the working directory, is refused as before. The rest of
// the config directory (the agent's credentials, every project's history)
// is never opened. Only an admin or the holder of the session's agent token
// reports file events and plans, so a viewer cannot name a plan into the
// session.

// homeDir is the process's home, as a path's leading ~ expands: what the
// environment names (os.UserHomeDir: HOME, Windows's USERPROFILE), else
// the account database's (os/user), which a service started with no HOME
// still has; "" when neither knows.
func homeDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

// planPath is raw as a request or a report names a plan: absolute (a
// leading ~ the process's home, as ResolvePath takes it), cleaned; "" for a
// relative path or one that cannot be a plan's.
func planPath(raw string) string {
	if raw == "~" || strings.HasPrefix(raw, "~/") || runtime.GOOS == "windows" && strings.HasPrefix(raw, `~\`) {
		home := homeDir()
		if home == "" {
			return ""
		}
		raw = filepath.Join(home, raw[1:])
	}
	if raw == "" || !filepath.IsAbs(raw) || strings.ContainsRune(raw, 0) {
		return ""
	}
	return filepath.Clean(raw)
}

// underDir reports whether abs, cleaned, is inside dir, cleaned (not dir
// itself), the names compared as the system compares them.
func underDir(abs, dir string) bool {
	if dir == "" || abs == "" {
		return false
	}
	prefix := dir + string(filepath.Separator)
	if runtime.GOOS == "windows" {
		return len(abs) > len(prefix) && strings.EqualFold(abs[:len(prefix)], prefix)
	}
	return strings.HasPrefix(abs, prefix)
}

// planKey is the key a plan is kept under: its path with its links
// resolved as far as it exists (evalExisting, as a request's path is
// resolved), so that a path spelt through a link (/var for /private/var)
// names the same plan.
func planKey(abs string) string {
	if real, err := evalExisting(abs); err == nil {
		return real
	}
	return abs
}

// openPlansLocked opens the plans directory as a folder, once, allowing
// the session's plans alone (planAllowed), and keeps its path with its
// links resolved: false when it cannot be opened (not there yet, or not a
// directory). s.mu held.
func (s *Local) openPlansLocked() bool {
	if s.plans != nil {
		return true
	}
	real, err := filepath.EvalSymlinks(s.opts.Plans)
	if err != nil {
		return false
	}
	f, err := OpenFolder(s.opts.Plans)
	if err != nil {
		return false
	}
	f.allow = s.planAllowed
	s.plans, s.plansReal = f, real
	return true
}

// planAllowed is the plans folder's allow: whether real, the path a request
// resolved to, is one of the session's plans.
func (s *Local) planAllowed(real string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.planFiles[real]
}

// plansRealLocked is the plans directory with its links resolved: as kept
// once the folder is open, else as it is now ("" when it is not there).
// s.mu held.
func (s *Local) plansRealLocked() string {
	if s.plansReal != "" {
		return s.plansReal
	}
	if real, err := filepath.EvalSymlinks(s.opts.Plans); err == nil {
		return real
	}
	return ""
}

// inPlansLocked reports whether abs (planPath's) names a file in the plans
// directory: under it as named, or under it with every link resolved (the
// agent may name a linked directory by its target, or spell /var as
// /private/var). s.mu held.
func (s *Local) inPlansLocked(abs string) bool {
	if s.opts.Plans == "" || abs == "" {
		return false
	}
	return underDir(abs, s.opts.Plans) || underDir(planKey(abs), s.plansRealLocked())
}

// learnPlanLocked keeps abs (planPath's) as one of the session's plans when
// it is a regular file in the plans directory now, by its path with its
// links resolved (a link out of the directory is not kept, nor a denied
// file, nor nothing there), and reports whether it is. s.mu held.
func (s *Local) learnPlanLocked(abs string) bool {
	if !s.inPlansLocked(abs) || !s.openPlansLocked() {
		return false
	}
	real, _, err := resolvePath(s.opts.Plans, abs, newDenySet(s.fileDeny()))
	if err != nil || !underDir(real, s.plansReal) {
		return false
	}
	if fi, err := os.Lstat(real); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	if s.planFiles == nil {
		s.planFiles = map[string]bool{}
	}
	s.planFiles[real] = true
	return true
}

// notePlanLocked learns from a file entry record keeps (its path as the
// agent named it): one under the plans directory is a plan of the
// session's, forgotten when the agent deletes it. s.mu held.
func (s *Local) notePlanLocked(e ActivityEntry) {
	if s.opts.Plans == "" || e.Type != ActivityFile {
		return
	}
	abs := planPath(touchedAbs(s.info.Cwd, e.Path))
	if !s.inPlansLocked(abs) {
		return
	}
	if e.Op == FileOpDelete {
		delete(s.planFiles, planKey(abs))
		return
	}
	s.learnPlanLocked(abs)
}

// planOnShownLocked sets plan (planPath's, learnt) on the question
// showing, when it carries another or none, and tells every client;
// reports whether it did. A change that names the plan of the approval
// already showing (setAttentionLooked: the hook's report after the dialog
// was read, the dialog's footer read on a later look). The caller holds
// s.mu and, when it did, calls notifyChange once unlocked.
func (s *Local) planOnShownLocked(plan string) bool {
	cur := &s.info.Attention
	if cur.State != AttentionNeedsInput || cur.ID == "" || cur.Plan == plan {
		return false
	}
	cur.Plan = plan
	s.hub.Broadcast(proto.MustControl(attentionMessage(s.attentionLocked())))
	return true
}

// folderFor is the folder a request's path raw is read from, saved to or
// opened in: the plans folder for a path that leads into the plans
// directory (as the agent reported it, or spelt through a link), which
// allows the session's plans alone; else the session's folder, which
// refuses what is outside it.
func (s *Local) folderFor(raw string) *Folder {
	abs := planPath(raw)
	if abs == "" {
		return s.folder
	}
	key := planKey(abs)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.plans == nil || !underDir(key, s.plansReal) {
		return s.folder
	}
	return s.plans
}

// PlansPath answers the file request `op: plans`: a FILE header of kind
// `plans`, Path the plans directory, Exists once it is open (the agent
// reported a plan), Entries the session's plans that are files there now,
// by their paths from the directory, in order, each with its size; the
// deny list applied and a view link held to a controller's files as a read
// is. Nothing for a session whose agent keeps no plans directory.
func (s *Local) PlansPath(deny []string, role Role) proto.FileHeader {
	s.mu.Lock()
	dir, f, root := s.opts.Plans, s.plans, s.plansReal
	keys := make([]string, 0, len(s.planFiles))
	for k := range s.planFiles {
		keys = append(keys, k)
	}
	s.mu.Unlock()
	h := proto.FileHeader{Path: dir, Kind: "plans", Exists: f != nil}
	if f == nil {
		return h
	}
	sort.Strings(keys)
	for _, k := range keys {
		st, _ := f.read(k, true, deny, role)
		rel, err := filepath.Rel(root, k)
		if st.Kind != "file" || err != nil {
			continue
		}
		h.Entries = append(h.Entries, proto.FileEntry{Name: filepath.ToSlash(rel), Size: st.Size})
	}
	return h
}

// planFooter is the plan's path on the last row of Claude Code's plan
// approval ("ctrl+g to edit in <editor> · <path>"), with the rows a narrow
// screen wrapped it over joined; "" when the screen shows no such row. A
// path Claude Code shortened to fit (an ellipsis in it) names no file, and
// NotePlan leaves it.
var planFooterRe = regexp.MustCompile(`ctrl\+g to edit in [^·\n]*·[ \t]*(\S+)[ \t]*$`)

func planFooter(text string) string {
	rows := strings.Split(text, "\n")
	for i := len(rows) - 1; i >= 0 && i >= len(rows)-6; i-- {
		m := planFooterRe.FindStringSubmatch(rows[i])
		if m == nil {
			continue
		}
		p := m[1]
		for j := i + 1; j < len(rows) && j <= i+3; j++ {
			r := strings.TrimSpace(rows[j])
			if r == "" {
				break
			}
			p += r
		}
		return p
	}
	return ""
}
