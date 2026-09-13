// Package designtools invokes pinned upstream commands against bounded staged
// artifacts. These reports describe native tool checks, never design acceptance.
package designtools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/phenixrizen/conductor/internal/domain"
)

const SpecKitVersion = "1.0.6"
const SpecKitCommit = "96c9bd657bfd5de0d651a6165084932b7304ac99"
const ADRKitVersion = "0.13.0"
const ADRKitCommit = "3e40675ed6f513d9712b1dccaa68034d649d1eb9"
const MaxFiles = 256
const MaxBytes = 4 << 20
const MaxOutput = 1 << 20

var ErrInvalid = errors.New("invalid design tool input")
var ErrUnavailable = errors.New("design tool unavailable")

type Request struct {
	Command   string   `json:"command"`
	Feature   string   `json:"feature,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	Directory string   `json:"directory,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Title     string   `json:"title,omitempty"`
}
type File struct {
	Mode   uint32 `json:"mode"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
}
type Report struct {
	Command        string `json:"command"`
	Tool           string `json:"tool"`
	Version        string `json:"version"`
	UpstreamCommit string `json:"upstreamCommit"`
	State          string `json:"state"`
	Scope          string `json:"scope"`
	InputDigest    string `json:"inputDigest"`
	Files          []File `json:"files"`
	Created        []File `json:"created"`
	ExitCode       int    `json:"exitCode"`
	Output         string `json:"output"`
	OutputDigest   string `json:"outputDigest"`
	Truncated      bool   `json:"truncated"`
}
type Runtime struct{ Specify, ADR, CoreRoot, Node string }

func DefaultRuntime() Runtime {
	return Runtime{Node: "/usr/local/bin/node", Specify: "/opt/conductor-design/venv/bin/specify", ADR: "/opt/conductor-design/node_modules/.bin/adr", CoreRoot: "/opt/conductor-design/scaffold"}
}
func sum(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func validPath(p string) bool {
	return domain.ValidateContextPath(p) == nil && !strings.Contains(p, "\n") && !strings.Contains(p, "\r") && p != ".git" && !strings.HasPrefix(p, ".git/")
}
func Validate(in Request) error {
	switch in.Command {
	case "spec-init":
		if in.Feature != "" || in.Kind != "" || in.Directory != "" || len(in.Paths) > 0 || in.Title != "" {
			return ErrInvalid
		}
	case "spec-template":
		if !validPath(in.Feature) || !strings.HasPrefix(in.Feature, "specs/") || strings.Count(in.Feature, "/") != 1 || in.Kind != "spec" && in.Kind != "plan" && in.Kind != "tasks" && in.Kind != "checklist" || in.Directory != "" || len(in.Paths) > 0 || in.Title != "" {
			return ErrInvalid
		}
	case "spec-check":
		if !validPath(in.Feature) || !strings.HasPrefix(in.Feature, "specs/") || strings.Count(in.Feature, "/") != 1 || in.Kind != "" || in.Directory != "" || len(in.Paths) > 0 || in.Title != "" {
			return ErrInvalid
		}
	case "adr-lint", "adr-graph", "adr-check", "adr-explain", "adr-new":
		if !validPath(in.Directory) || in.Feature != "" || in.Kind != "" || len(in.Paths) > 128 {
			return ErrInvalid
		}
		if in.Command == "adr-check" && len(in.Paths) == 0 || in.Command == "adr-explain" && len(in.Paths) != 1 || in.Command != "adr-check" && in.Command != "adr-explain" && len(in.Paths) != 0 {
			return ErrInvalid
		}
		if in.Command == "adr-new" {
			if strings.TrimSpace(in.Title) == "" || strings.HasPrefix(in.Title, "-") || len(in.Title) > 256 || !domain.IsContextText([]byte(in.Title)) || strings.ContainsAny(in.Title, "\n\r\t") {
				return ErrInvalid
			}
		} else if in.Title != "" {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for _, p := range in.Paths {
			if !validPath(p) || seen[p] {
				return ErrInvalid
			}
			seen[p] = true
		}
	default:
		return ErrInvalid
	}
	return nil
}

type snapshot struct {
	files map[string][]byte
	modes map[string]fs.FileMode
	bytes int
}

func newSnapshot() *snapshot {
	return &snapshot{files: map[string][]byte{}, modes: map[string]fs.FileMode{}}
}

// Inspect each component without following symbolic links. Staging refuses both
// escaping and in-tree links, FIFOs/devices, binary text and oversized corpora.
func safeFile(root, path string) (string, error) {
	if !validPath(path) {
		return "", ErrInvalid
	}
	current := root
	for _, part := range strings.Split(path, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrInvalid
		}
	}
	return current, nil
}
func (s *snapshot) add(root, path string, missingOK bool) error {
	full, err := safeFile(root, path)
	if errors.Is(err, fs.ErrNotExist) && missingOK {
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > domain.MaxContextArtifactBytes {
		return ErrUnavailable
	}
	file, err := os.OpenFile(full, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return ErrUnavailable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() > domain.MaxContextArtifactBytes {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(file, domain.MaxContextArtifactBytes+1))
	if err != nil || len(data) > domain.MaxContextArtifactBytes || !domain.IsContextText(data) {
		return ErrUnavailable
	}
	if _, ok := s.files[path]; ok {
		return nil
	}
	if len(s.files) >= MaxFiles || s.bytes+len(data) > MaxBytes {
		return ErrUnavailable
	}
	s.files[path] = data
	s.modes[path] = info.Mode().Perm()
	s.bytes += len(data)
	return nil
}
func (s *snapshot) tree(root, path string, missingOK, markdownOnly bool) error {
	full, err := safeFile(root, path)
	if errors.Is(err, fs.ErrNotExist) && missingOK {
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	info, err := os.Lstat(full)
	if err != nil || !info.IsDir() {
		return ErrUnavailable
	}
	visited := 0
	return filepath.WalkDir(full, func(name string, d fs.DirEntry, err error) error {
		visited++
		if visited > MaxFiles*4 {
			return ErrUnavailable
		}
		if err != nil {
			return ErrUnavailable
		}
		if d.Type()&os.ModeSymlink != 0 || !d.IsDir() && !d.Type().IsRegular() {
			return ErrUnavailable
		}
		if d.IsDir() {
			return nil
		}
		if markdownOnly && filepath.Ext(name) != ".md" {
			return nil
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return ErrUnavailable
		}
		return s.add(root, filepath.ToSlash(relative), false)
	})
}
func (s *snapshot) manifest() []File {
	result := []File{}
	for path, data := range s.files {
		result = append(result, File{Path: path, Digest: sum(data), Mode: uint32(0644 | (s.modes[path] & 0111))})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}
func (s *snapshot) write(root string) error {
	for _, f := range s.manifest() {
		path := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, s.files[f.Path], 0600); err != nil {
			return err
		}
	}
	return nil
}

// boundedOutput drains both pipes while retaining only the configured cap. An
// output limit cannot become a passing result just because the process exits 0.
type boundedOutput struct {
	mu        sync.Mutex
	b         bytes.Buffer
	truncated bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(data)
	remaining := MaxOutput - b.b.Len()
	if n > remaining {
		data = data[:remaining]
		b.truncated = true
	}
	_, _ = b.b.Write(data)
	return n, nil
}
func (rt Runtime) run(ctx context.Context, dir string, argv, extra []string) (string, int, bool) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + filepath.Dir(rt.Specify) + ":" + filepath.Dir(rt.ADR) + ":" + filepath.Dir(rt.Node) + ":/usr/local/bin:/usr/bin:/bin", "HOME=" + dir, "LANG=C.UTF-8", "NO_COLOR=1", "PYTHONSAFEPATH=1", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}, extra...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = time.Second
	var output boundedOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	code := 0
	if err != nil {
		code = 2
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
	}
	return output.b.String(), code, output.truncated
}

func (rt Runtime) Execute(ctx context.Context, root string, in Request) (Report, error) {
	var report Report
	if Validate(in) != nil || !filepath.IsAbs(root) || !filepath.IsAbs(rt.Specify) || !filepath.IsAbs(rt.ADR) || !filepath.IsAbs(rt.CoreRoot) || !filepath.IsAbs(rt.Node) {
		return report, ErrInvalid
	}
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return report, ErrInvalid
	}
	stage, err := os.MkdirTemp("", "conductor-design-")
	if err != nil {
		return report, ErrUnavailable
	}
	defer os.RemoveAll(stage)
	report = Report{Command: in.Command, Tool: "adrkit", Version: ADRKitVersion, UpstreamCommit: ADRKitCommit, State: "unavailable", Files: []File{}, Created: []File{}, ExitCode: 2, Scope: "ADR schema and decision-to-path relationships; recorded status is not Conductor approval"}
	binary, expected := rt.ADR, ADRKitVersion
	if strings.HasPrefix(in.Command, "spec-") {
		binary = rt.Specify
		expected = "specify " + SpecKitVersion
		report.Tool = "spec-kit"
		report.Version = SpecKitVersion
		report.UpstreamCommit = SpecKitCommit
		report.Scope = "Core templates and artifact prerequisites; no semantic architecture or implementation verification"
	}
	version, code, truncated := rt.run(ctx, stage, []string{binary, "--version"}, nil)
	if code != 0 || truncated || strings.TrimSpace(version) != expected {
		return report, ErrUnavailable
	}
	source := newSnapshot()
	var argv, extra []string
	var generated *snapshot
	if report.Tool == "adrkit" {
		if err = source.tree(root, in.Directory, in.Command == "adr-new", true); err != nil {
			return report, err
		}
		for _, path := range in.Paths {
			if err = source.add(root, path, true); err != nil {
				return report, err
			}
		}
		if err = source.write(stage); err != nil {
			return report, ErrUnavailable
		}
		if err = os.MkdirAll(filepath.Join(stage, in.Directory), 0700); err != nil {
			return report, ErrUnavailable
		}
		command := strings.TrimPrefix(in.Command, "adr-")
		argv = []string{rt.ADR, command, "--dir", in.Directory}
		if command == "graph" {
			argv = append(argv, "--format", "json")
		} else {
			argv = append(argv, "--json")
		}
		if command == "new" {
			argv = append(argv, "--status", "proposed", in.Title)
		} else {
			argv = append(argv, in.Paths...)
		}
	} else if in.Command == "spec-check" {
		if err = source.tree(root, in.Feature, true, false); err != nil {
			return report, err
		}
		if err = source.write(stage); err != nil {
			return report, ErrUnavailable
		}
		if err = os.MkdirAll(filepath.Join(stage, ".specify"), 0700); err != nil {
			return report, ErrUnavailable
		}
		argv = []string{"/bin/bash", filepath.Join(rt.CoreRoot, ".specify/scripts/bash/check-prerequisites.sh"), "--json", "--require-spec", "--require-tasks", "--include-tasks"}
		extra = []string{"SPECIFY_INIT_DIR=" + stage, "SPECIFY_FEATURE_DIRECTORY=" + in.Feature}
	} else if in.Command == "spec-init" {
		// Initialization occurs in a fresh temporary tree, so upstream setup cannot
		// overwrite a project's constitution, agent configuration or custom scripts.
		argv = []string{rt.Specify, "init", filepath.Join(stage, "generated"), "--integration", "generic", "--integration-options=--commands-dir .conductor/spec-kit", "--ignore-agent-tools", "--non-interactive"}
	} else {
		template := newSnapshot()
		if err = template.add(rt.CoreRoot, ".specify/templates/"+in.Kind+"-template.md", false); err != nil {
			return report, err
		}
		generated = newSnapshot()
		for _, data := range template.files {
			generated.files[in.Feature+"/"+in.Kind+".md"] = data
		}
	}
	report.Files = source.manifest()
	identity, _ := json.Marshal(struct {
		Request         Request
		Files           []File
		SpecKit, ADRKit string
	}{in, report.Files, SpecKitCommit, ADRKitCommit})
	report.InputDigest = sum(identity)
	if argv != nil {
		report.Output, report.ExitCode, report.Truncated = rt.run(ctx, stage, argv, extra)
	} else {
		report.ExitCode = 0
		report.Output = "Pinned upstream core template prepared."
	}
	report.Output = strings.ReplaceAll(report.Output, stage, "<staged-source>")
	report.OutputDigest = sum([]byte(report.Output))
	report.State = "failed"
	if report.ExitCode == 0 && !report.Truncated {
		report.State = "passed"
	}
	// An empty lint corpus is absence of decision evidence, even though the
	// upstream CLI exits successfully after checking zero records.
	if in.Command == "adr-lint" && report.State == "passed" {
		var native struct {
			Checked int `json:"checked"`
		}
		if json.Unmarshal([]byte(report.Output), &native) != nil || native.Checked == 0 {
			report.State = "unavailable"
			report.ExitCode = 2
		}
	}
	if report.State != "passed" {
		return report, nil
	}
	if in.Command == "spec-init" || in.Command == "spec-template" || in.Command == "adr-new" {
		// Upstream staging success is not proof that the requested artifact was
		// produced in the authorized repository. Keep failures in transfer explicit.
		report.State = "unavailable"
		report.ExitCode = 2
	}
	if in.Command == "spec-init" {
		generated = newSnapshot()
		base := filepath.Join(stage, "generated")
		for _, path := range []string{".specify", ".conductor/spec-kit"} {
			if err = generated.tree(base, path, false, false); err != nil {
				return report, err
			}
		}
	} else if in.Command == "adr-new" {
		after := newSnapshot()
		if err = after.tree(stage, in.Directory, false, true); err != nil {
			return report, err
		}
		generated = newSnapshot()
		for path, data := range after.files {
			if _, existed := source.files[path]; !existed {
				generated.files[path] = data
			}
		}
		if len(generated.files) != 1 {
			return report, ErrUnavailable
		}
	}
	if generated != nil {
		report.Created, err = publishGenerated(root, generated)
		if err != nil {
			report.State = "blocked"
			report.ExitCode = 2
			return report, err
		}
		report.State = "produced"
		report.ExitCode = 0
	}
	return report, nil
}

// Only exclusive new-file creation is permitted. Existing artifacts, including
// constitutions and accepted historical ADRs, are never rewritten by these tools.
func publishGenerated(root string, s *snapshot) ([]File, error) {
	for _, f := range s.manifest() {
		if !validPath(f.Path) {
			return nil, ErrInvalid
		}
		parts := strings.Split(f.Path, "/")
		for n := 1; n <= len(parts); n++ {
			name := filepath.Join(root, filepath.Join(parts[:n]...))
			info, err := os.Lstat(name)
			if errors.Is(err, fs.ErrNotExist) {
				break
			}
			if err != nil || info.Mode()&os.ModeSymlink != 0 || n == len(parts) || !info.IsDir() {
				return nil, ErrInvalid
			}
		}
	}
	written := []string{}
	success := false
	defer func() {
		if !success {
			for _, name := range written {
				_ = os.Remove(name)
			}
		}
	}()
	for _, f := range s.manifest() {
		name := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return nil, ErrUnavailable
		}
		file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, fs.FileMode(f.Mode))
		if err != nil {
			return nil, ErrUnavailable
		}
		written = append(written, name)
		_, err = file.Write(s.files[f.Path])
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return nil, ErrUnavailable
		}
	}
	success = true
	return s.manifest(), nil
}
