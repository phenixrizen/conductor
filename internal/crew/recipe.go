package crew

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rocksolidlabs/conductor/internal/pty"
	"github.com/rocksolidlabs/conductor/internal/store"
)

// A project recipe (docs/plans/project-recipes.md, Program item EN1) is
// what makes a fresh tree of a project usable, how it is checked and how
// it is cleaned up. A tree is a directory the engine makes usable: an
// attempt's worktree, or the checkout of its result. It is not an agent's
// recipes (internal/catalog: yolo, trust, session).
//
// A recipe is the crew's (Crew.Recipe, edited in the UI and counted by
// the store in Crew.RecipeRevision) or, with File, conductor.recipe.json
// in the crew's directory as each tree's commit holds it (resolveRecipe),
// versioned with the code. A crew has one or the other. A legacy Setup is
// the recipe's setup phase (effectiveRecipe), so the engine sees one
// shape. This build runs that phase alone: the launch of a recipe that
// names more is refused (runnableRecipe) rather than skipped or half run.

// Recipe is a project recipe. Every step is a Check: an argv array, never a
// shell line, run in the tree's working directory.
type Recipe struct {
	// Schema is 1 in conductor.recipe.json, and left out of a crew's.
	Schema int `json:"schema,omitempty"`
	// File reads conductor.recipe.json at each tree's commit; every other
	// field of the crew's recipe is empty then.
	File bool `json:"file,omitempty"`
	// Prerequisites run once per run in the crew's working directory,
	// before any tree: what the host must have. 60 s each when no timeout
	// is given.
	Prerequisites []Check `json:"prerequisites,omitempty"`
	// Env names the host's variables the steps and sessions need; the
	// values stay the host's, and the server's envPassthrough decides
	// whether a name may leave it.
	Env []string `json:"env,omitempty"`
	// Ports names the ports each tree gets of its own, each
	// CONDUCTOR_PORT_<NAME> in the tree.
	Ports []string `json:"ports,omitempty"`
	// Setup makes a fresh tree usable (600 s each by default), Services
	// run beside it for as long as it lives, Ready says when they answer,
	// Preview is the URL a person opens, Verify runs after the task's
	// checks in the checkout of a result (600 s), and Cleanup runs once a
	// tree ends, however it ends (60 s).
	Setup    []Check `json:"setup,omitempty"`
	Services []Check `json:"services,omitempty"`
	Ready    []Probe `json:"ready,omitempty"`
	Preview  string  `json:"preview,omitempty"`
	Verify   []Check `json:"verify,omitempty"`
	Cleanup  []Check `json:"cleanup,omitempty"`
}

// Probe is a readiness check: a command run until it exits 0, or a URL on
// this machine fetched until it answers under 300, every IntervalSeconds,
// for at most TimeoutSeconds. It has Argv or URL, never both.
type Probe struct {
	ID              string   `json:"id"`
	Argv            []string `json:"argv,omitempty"`
	URL             string   `json:"url,omitempty"`
	TimeoutSeconds  int      `json:"timeoutSeconds,omitempty"`  // 1 to maxProbeTimeout; defaultProbeTimeout when 0
	IntervalSeconds int      `json:"intervalSeconds,omitempty"` // 1 to maxProbeInterval; defaultProbeInterval when 0
}

// The bounds of a project recipe (Recipe.validate).
const (
	// recipeFile is the file form's name, in the crew's directory: never
	// under .conductor/, which dispatch refuses a commit to track.
	recipeFile = "conductor.recipe.json"
	// recipeSchema is the schema conductor.recipe.json says. A larger one
	// needs a newer Conductor.
	recipeSchema = 1
	// maxRecipeSteps bounds prerequisites, setup, ready, verify and
	// cleanup, each.
	maxRecipeSteps       = 8
	maxRecipeServices    = 4
	maxRecipeEnv         = 32
	maxRecipePorts       = 8
	maxProbeTimeout      = 3600 // seconds
	defaultProbeTimeout  = 60
	maxProbeInterval     = 60 // seconds
	defaultProbeInterval = 2
	maxProbeURL          = 2048 // bytes
	// maxPreview bounds the preview: a URL on the loopback needs no more,
	// and the task file carries it uncut.
	maxPreview = 512
	maxPort    = 65535
	// maxRecipeJSON bounds a recipe as JSON (encodeRecipe), the crew's
	// and the file's alike.
	maxRecipeJSON = 64 << 10
	// maxRecipeFile bounds conductor.recipe.json as the commit holds it,
	// which may be indented.
	maxRecipeFile = 256 << 10
)

var (
	// envNamePattern is a variable name a recipe's env may give.
	envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	// portNamePattern is a port's name: CONDUCTOR_PORT_<NAME> is its upper
	// case.
	portNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,15}$`)
	// placeholderPattern finds a placeholder: ${CONDUCTOR_PORT_<NAME>},
	// whatever the name (one that names no port of the recipe is refused),
	// or ${CONDUCTOR_STATE_DIR}. Any other ${...} is not a placeholder and
	// passes as it is (a shell a step names may expand it).
	placeholderPattern = regexp.MustCompile(`\$\{CONDUCTOR_(?:PORT_([A-Za-z0-9_]*)|STATE_DIR)\}`)
	// loopbackURLPattern is a probe's URL or the preview as a recipe gives
	// it: http or https, a host of 127.0.0.1, localhost or [::1], a port
	// of digits or a port placeholder, then a path, a query or a fragment
	// without spaces or control characters. web/app/utils/crews.ts holds
	// the same pattern.
	loopbackURLPattern = regexp.MustCompile(`^https?://(?:127\.0\.0\.1|localhost|\[::1\])(?::([0-9]{1,5}|\$\{CONDUCTOR_PORT_[A-Za-z0-9_]*\}))?(?:[/?#][^\x00-\x20\x7f]*)?$`)
)

// stateDirPlaceholder is the placeholder of a tree's state directory.
const stateDirPlaceholder = "${CONDUCTOR_STATE_DIR}"

// empty reports whether the recipe holds nothing: a crew with an empty
// recipe has none (canonical leaves it out).
func (rec Recipe) empty() bool {
	return rec.Schema == 0 && !rec.File && len(rec.Prerequisites) == 0 && len(rec.Env) == 0 && len(rec.Ports) == 0 &&
		len(rec.Setup) == 0 && len(rec.Services) == 0 && len(rec.Ready) == 0 && rec.Preview == "" && len(rec.Verify) == 0 && len(rec.Cleanup) == 0
}

// clone returns a copy of rec that shares no slice with it.
func (rec Recipe) clone() Recipe {
	rec.Prerequisites = cloneChecks(rec.Prerequisites)
	rec.Env = slices.Clone(rec.Env)
	rec.Ports = slices.Clone(rec.Ports)
	rec.Setup = cloneChecks(rec.Setup)
	rec.Services = cloneChecks(rec.Services)
	rec.Ready = slices.Clone(rec.Ready)
	for i := range rec.Ready {
		rec.Ready[i].Argv = slices.Clone(rec.Ready[i].Argv)
	}
	rec.Verify = cloneChecks(rec.Verify)
	rec.Cleanup = cloneChecks(rec.Cleanup)
	return rec
}

// cloneChecks returns a copy of checks that shares no slice with it.
func cloneChecks(checks []Check) []Check {
	out := slices.Clone(checks)
	for i := range out {
		out[i].Argv = slices.Clone(out[i].Argv)
	}
	return out
}

// hasRecipe reports whether c has a project recipe with something in it.
func (c Crew) hasRecipe() bool { return c.Recipe != nil && !c.Recipe.empty() }

// effectiveRecipe is the crew's project recipe as the engine runs it: its
// Recipe, or a legacy Setup lifted into a recipe with that one phase, so
// the engine sees one shape and a legacy Setup is run as the recipe's
// setup phase; empty when the crew has neither. A copy.
func (c Crew) effectiveRecipe() Recipe {
	switch {
	case c.hasRecipe():
		return c.Recipe.clone()
	case len(c.Setup) > 0:
		return Recipe{Setup: cloneChecks(c.Setup)}
	}
	return Recipe{}
}

// encodeRecipe is rec as JSON, as maxRecipeJSON counts it and the store
// compares two: compact, with <, > and & as they are (as the crew's file
// keeps them), and U+2028 and U+2029 escaped, six bytes each, as
// encoding/json always writes them. web/app/utils/crews.ts counts a
// recipe the same way (serverJsonBytes), which JSON.stringify's text
// alone does not.
func encodeRecipe(rec Recipe) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rec); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// sameRecipe reports whether a and b are one recipe: equal as JSON.
func sameRecipe(a, b Recipe) bool {
	ja, erra := encodeRecipe(a)
	jb, errb := encodeRecipe(b)
	return erra == nil && errb == nil && bytes.Equal(ja, jb)
}

// recipeRevision is the RecipeRevision the store gives c as it saves it,
// whatever c carries: for a new crew (old nil) 1 when its effective recipe
// holds something, else 0; over old, old's own, one more when c's
// effective recipe differs from old's as JSON, a removal included. So the
// counter only goes up, and a number names one recipe of a crew for good,
// as a receipt's crew:<revision> promises.
func recipeRevision(old *Crew, c Crew) int64 {
	if old == nil {
		if c.effectiveRecipe().empty() {
			return 0
		}
		return 1
	}
	if sameRecipe(old.effectiveRecipe(), c.effectiveRecipe()) {
		return old.RecipeRevision
	}
	return old.RecipeRevision + 1
}

// taskChecks maps every check id of tasks to the first task that has it:
// the ids a recipe's steps may not take.
func taskChecks(tasks []Task) map[string]string {
	out := map[string]string{}
	for _, t := range tasks {
		for _, ch := range t.Checks {
			if _, ok := out[ch.ID]; !ok {
				out[ch.ID] = t.ID
			}
		}
	}
	return out
}

// stepRule is what a list of steps is held to beyond a check's bounds.
type stepRule int

const (
	treeStep         stepRule = iota // runs in a tree: its placeholders name the recipe's ports
	prerequisiteStep                 // runs before any tree: no placeholder at all
	serviceStep                      // lives as long as its tree: no timeout
)

// validate checks rec, the crew's project recipe or (file) one read from
// conductor.recipe.json, against the rules of a recipe; where names it in
// the error ("project recipe", or the file at its commit). checks maps
// every task's check id to its task: a step's id is none of them, since a
// step's receipt shares the checks' table and key, and every id is unique
// across the recipe's lists. The first problem found is returned; it
// matches ErrInvalid. web/app/utils/crews.ts recipeProblems mirrors it.
func (rec Recipe) validate(where string, checks map[string]string, file bool) error {
	if file {
		switch {
		case rec.Schema > recipeSchema:
			return invalidf("%s: schema %d needs a newer Conductor (this one reads schema %d)", where, rec.Schema, recipeSchema)
		case rec.Schema != recipeSchema:
			return invalidf("%s: schema must be %d", where, recipeSchema)
		case rec.File:
			return invalidf("%s: file is set in a crew's recipe, not in %s", where, recipeFile)
		}
	} else {
		if rec.Schema != 0 {
			return invalidf("%s: schema is only set in %s", where, recipeFile)
		}
		if rec.File {
			rest := rec
			rest.File = false
			if !rest.empty() {
				return invalidf("%s: with file, the recipe is %s at each tree's commit: set nothing else", where, recipeFile)
			}
			return nil
		}
	}
	if len(rec.Env) > maxRecipeEnv {
		return invalidf("%s: too many env names (at most %d)", where, maxRecipeEnv)
	}
	for i, name := range rec.Env {
		switch {
		case !envNamePattern.MatchString(name):
			return invalidf("%s: env[%d]: %s is not a variable name: letters, digits and _, not starting with a digit, at most 128", where, i, quote(name))
		case strings.HasPrefix(name, "CONDUCTOR_"):
			return invalidf("%s: env[%d]: %s is Conductor's own: a CONDUCTOR_ name never comes from the host", where, i, quote(name))
		case pty.Blocked(name):
			return invalidf("%s: env[%d]: %s is a loader's variable, never passed on", where, i, quote(name))
		case slices.Contains(rec.Env[:i], name):
			return invalidf("%s: env[%d]: %s is named twice", where, i, quote(name))
		}
	}
	if len(rec.Ports) > maxRecipePorts {
		return invalidf("%s: too many ports (at most %d)", where, maxRecipePorts)
	}
	ports := map[string]bool{} // each port's name as its placeholder names it
	for i, name := range rec.Ports {
		switch {
		case !portNamePattern.MatchString(name):
			return invalidf("%s: ports[%d]: %s must match %s", where, i, quote(name), portNamePattern)
		case slices.Contains(rec.Ports[:i], name):
			return invalidf("%s: ports[%d]: %s is named twice", where, i, quote(name))
		}
		ports[strings.ToUpper(name)] = true
	}
	ids := map[string]bool{}
	// id checks a step's id against the recipe's ids so far and the tasks'
	// checks, and takes it.
	id := func(what, stepID string) error {
		if ids[stepID] {
			return invalidf("%s: id %s is used twice in the recipe", what, quote(stepID))
		}
		if task, ok := checks[stepID]; ok {
			return invalidf("%s: id %s is also a check of task %s: a step's id is its own", what, quote(stepID), quote(task))
		}
		ids[stepID] = true
		return nil
	}
	// steps checks one list of steps: its count, then each step's bounds,
	// its id and its placeholders, by the list's rule.
	steps := func(list []Check, name, words string, most int, rule stepRule) error {
		if len(list) > most {
			return invalidf("%s: too many %s (at most %d)", where, words, most)
		}
		for i, ch := range list {
			what := fmt.Sprintf("%s: %s[%d]", where, name, i)
			if rule == serviceStep && ch.TimeoutSeconds != 0 {
				return invalidf("%s: a service has no timeoutSeconds: it runs as long as its tree", what)
			}
			if err := ch.validate(what); err != nil {
				return err
			}
			if err := id(what, ch.ID); err != nil {
				return err
			}
			for j, a := range ch.Argv {
				if rule == prerequisiteStep && placeholderPattern.MatchString(a) {
					return invalidf("%s: argv[%d]: a prerequisite runs before any tree: no placeholders", what, j)
				}
			}
			if err := argvPlaceholders(what, ch.Argv, ports); err != nil {
				return err
			}
		}
		return nil
	}
	if err := steps(rec.Prerequisites, "prerequisites", "prerequisites", maxRecipeSteps, prerequisiteStep); err != nil {
		return err
	}
	if err := steps(rec.Setup, "setup", "setup steps", maxRecipeSteps, treeStep); err != nil {
		return err
	}
	if err := steps(rec.Services, "services", "services", maxRecipeServices, serviceStep); err != nil {
		return err
	}
	if len(rec.Ready) > maxRecipeSteps {
		return invalidf("%s: too many ready probes (at most %d)", where, maxRecipeSteps)
	}
	for i, p := range rec.Ready {
		what := fmt.Sprintf("%s: ready[%d]", where, i)
		if err := p.validate(what, ports); err != nil {
			return err
		}
		if err := id(what, p.ID); err != nil {
			return err
		}
	}
	if rec.Preview != "" {
		if err := loopbackURL(where+": preview", rec.Preview, maxPreview, ports); err != nil {
			return err
		}
	}
	if err := steps(rec.Verify, "verify", "verify steps", maxRecipeSteps, treeStep); err != nil {
		return err
	}
	if err := steps(rec.Cleanup, "cleanup", "cleanup steps", maxRecipeSteps, treeStep); err != nil {
		return err
	}
	b, err := encodeRecipe(rec)
	if err != nil {
		return invalidf("%s: cannot be written as JSON: %v", where, err)
	}
	if len(b) > maxRecipeJSON {
		return invalidf("%s: the recipe is %d bytes as JSON, more than %d", where, len(b), maxRecipeJSON)
	}
	return nil
}

// validate checks a readiness probe: its id, argv or url (not both), and
// its timeout and interval.
func (p Probe) validate(what string, ports map[string]bool) error {
	if !taskIDPattern.MatchString(p.ID) {
		return invalidf("%s: id %s must match %s", what, quote(p.ID), taskIDPattern)
	}
	switch {
	case len(p.Argv) > 0 && p.URL != "":
		return invalidf("%s: a probe has argv or url, not both", what)
	case len(p.Argv) > 0:
		if err := validateArgv(what, p.Argv); err != nil {
			return err
		}
		if err := argvPlaceholders(what, p.Argv, ports); err != nil {
			return err
		}
	case p.URL != "":
		if err := loopbackURL(what+": url", p.URL, maxProbeURL, ports); err != nil {
			return err
		}
	default:
		return invalidf("%s: a probe needs argv or url", what)
	}
	if p.TimeoutSeconds < 0 || p.TimeoutSeconds > maxProbeTimeout {
		return invalidf("%s: timeoutSeconds must be between 1 and %d (%d when left out)", what, maxProbeTimeout, defaultProbeTimeout)
	}
	if p.IntervalSeconds < 0 || p.IntervalSeconds > maxProbeInterval {
		return invalidf("%s: intervalSeconds must be between 1 and %d (%d when left out)", what, maxProbeInterval, defaultProbeInterval)
	}
	return nil
}

// loopbackURL checks a probe's URL or the preview as a recipe gives it:
// at most most bytes, http or https on 127.0.0.1, localhost or [::1]
// (loopbackURLPattern), a port of 1 to 65535 when it is digits, and
// every placeholder naming a port of the recipe.
func loopbackURL(what, u string, most int, ports map[string]bool) error {
	if len(u) > most {
		return invalidf("%s is longer than %d bytes", what, most)
	}
	m := loopbackURLPattern.FindStringSubmatch(u)
	if m == nil {
		return invalidf("%s %s must be http or https on 127.0.0.1, localhost or [::1]", what, quote(u))
	}
	if port := m[1]; port != "" && !strings.HasPrefix(port, "$") {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > maxPort {
			return invalidf("%s %s: the port must be 1 to %d", what, quote(u), maxPort)
		}
	}
	return placeholders(what, u, ports)
}

// argvPlaceholders checks the placeholders in each argv element.
func argvPlaceholders(what string, argv []string, ports map[string]bool) error {
	for j, a := range argv {
		if err := placeholders(fmt.Sprintf("%s: argv[%d]", what, j), a, ports); err != nil {
			return err
		}
	}
	return nil
}

// placeholders refuses a port placeholder in s that names no port of the
// recipe (ports holds their upper-case names): it would be passed on as
// it is, or expand to nothing in a shell.
func placeholders(what, s string, ports map[string]bool) error {
	for _, m := range placeholderPattern.FindAllStringSubmatch(s, -1) {
		if m[0] != stateDirPlaceholder && !ports[m[1]] {
			return invalidf("%s: %s names no port of the recipe", what, quote(m[0]))
		}
	}
	return nil
}

// expand is s with its placeholders put in literally: each
// ${CONDUCTOR_PORT_<NAME>} the number ports gives the port name (a ports
// entry; NAME is its upper case) and ${CONDUCTOR_STATE_DIR} the tree's
// state directory. It is applied to one argv element at a time, a probe's
// URL and the preview, so it never splits an element, and what it puts in
// is never expanded again. A placeholder naming no port is left as it is
// (validation refuses one in a recipe). Nothing applies it yet: the engine
// that runs a recipe's ports and state directory does.
func expand(s string, ports map[string]int, stateDir string) string {
	if !strings.Contains(s, "${CONDUCTOR_") {
		return s
	}
	return placeholderPattern.ReplaceAllStringFunc(s, func(m string) string {
		if m == stateDirPlaceholder {
			return stateDir
		}
		name := m[len("${CONDUCTOR_PORT_") : len(m)-1]
		if p, ok := ports[strings.ToLower(name)]; ok && name == strings.ToUpper(name) {
			return strconv.Itoa(p)
		}
		return m
	})
}

// runnableRecipe refuses the launch of a crew whose project recipe names a
// phase, a form or a placeholder this build does not run yet, so a recipe
// saved ahead of its engine is never accepted and then skipped or half
// run. This build runs the setup phase alone, as it runs a legacy Setup,
// and expands no placeholder (a ${CONDUCTOR_STATE_DIR} in a setup step
// would be passed on as it is). A legacy Setup launches as it always has.
// The error matches ErrInvalid: the API answers 400 invalid_crew.
func (c Crew) runnableRecipe() error {
	if !c.hasRecipe() {
		return nil
	}
	rec := c.Recipe
	switch {
	case rec.File:
		return invalidf("this build does not read a project recipe from %s yet", recipeFile)
	case len(rec.Prerequisites) > 0:
		return invalidf("this build does not run a project recipe's prerequisites yet")
	case len(rec.Env) > 0:
		return invalidf("this build does not pass a project recipe's env names on yet")
	case len(rec.Ports) > 0:
		return invalidf("this build does not give a project recipe's ports yet")
	case len(rec.Services) > 0:
		return invalidf("this build does not run a project recipe's services yet")
	case len(rec.Ready) > 0:
		return invalidf("this build does not run a project recipe's ready probes yet")
	case rec.Preview != "":
		return invalidf("this build does not offer a project recipe's preview yet")
	case len(rec.Verify) > 0:
		return invalidf("this build does not run a project recipe's verify steps yet")
	case len(rec.Cleanup) > 0:
		return invalidf("this build does not run a project recipe's cleanup steps yet")
	}
	for i, ch := range rec.Setup {
		for j, a := range ch.Argv {
			if m := placeholderPattern.FindString(a); m != "" {
				return invalidf("this build does not expand a project recipe's placeholders yet: setup[%d] (%s) names %s in argv[%d]", i, quote(ch.ID), m, j)
			}
		}
	}
	return nil
}

// resolveRecipe reads the file form of a project recipe:
// <prefix>conductor.recipe.json as commit holds it in the repository repo
// is in, through gitcli.Run (the C locale, no hook, no filter, no text
// conversion). One git ls-tree -l reads the entry's mode, type, blob id and
// size, so a link, a directory or a submodule in the file's place is
// refused by name and a file of more than maxRecipeFile bytes is refused
// before its content is read (git show of the blob). The content is
// decoded strictly (store.DecodeStrict: unknown fields refused) and held
// to a recipe's rules (validate), its step ids against tasks' checks. blob
// is the file's identity: its blob id, which a receipt records as
// file:<blob>. A commit without the file, and a file that cannot be a
// recipe, are refused with words naming the file and the commit (matching
// ErrInvalid); a failure of git itself is returned as it is.
func resolveRecipe(ctx context.Context, repo, commit, prefix string, tasks []Task) (rec Recipe, blob string, err error) {
	path := prefix + recipeFile
	if !isObjectID(commit) {
		return Recipe{}, "", fmt.Errorf("%s: %s is not a commit id", path, quote(commit))
	}
	where := fmt.Sprintf("%s at %s", path, short(commit))
	// Literal: "--" ends the options, not pathspec magic.
	out, err := git(ctx, repo, "ls-tree", "-l", "-z", "--full-tree", commit, "--", ":(literal)"+path)
	if err != nil {
		return Recipe{}, "", err
	}
	entry := strings.TrimSuffix(out, "\x00")
	if entry == "" {
		return Recipe{}, "", invalidf("the commit %s does not hold %s", short(commit), path)
	}
	meta, name, _ := strings.Cut(entry, "\t")
	fields := strings.Fields(meta) // <mode> <type> <object> <size>
	if name != path || len(fields) != 4 {
		return Recipe{}, "", fmt.Errorf("%s: git ls-tree answered %s", where, quote(entry))
	}
	mode, kind, id, size := fields[0], fields[1], fields[2], fields[3]
	switch {
	case kind == "tree":
		return Recipe{}, "", invalidf("%s is a directory: the recipe is a file", where)
	case kind == "commit":
		return Recipe{}, "", invalidf("%s is a submodule: the recipe is a file", where)
	case mode == "120000":
		return Recipe{}, "", invalidf("%s is a symbolic link: the recipe is a file", where)
	case kind != "blob" || (mode != "100644" && mode != "100755") || !isObjectID(id):
		return Recipe{}, "", invalidf("%s is not a file (%s %s)", where, mode, kind)
	}
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil {
		return Recipe{}, "", fmt.Errorf("%s: git ls-tree gave the size %s", where, quote(size))
	}
	if n > maxRecipeFile {
		return Recipe{}, "", invalidf("%s is %d bytes, more than %d", where, n, maxRecipeFile)
	}
	content, err := git(ctx, repo, "show", id)
	if err != nil {
		return Recipe{}, "", err
	}
	if int64(len(content)) != n {
		return Recipe{}, "", fmt.Errorf("%s: git show gave %d bytes of a blob of %d", where, len(content), n)
	}
	if err := store.DecodeStrict([]byte(content), &rec); err != nil {
		return Recipe{}, "", invalidf("%s: %v", where, err)
	}
	if err := rec.validate(where, taskChecks(tasks), true); err != nil {
		return Recipe{}, "", err
	}
	return rec, id, nil
}

// isObjectID reports whether s is an object id as git prints one: 40
// (SHA-1) or 64 (SHA-256) lower-case hex digits.
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
