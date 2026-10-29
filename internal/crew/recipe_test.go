package crew

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rocksolidlabs/conductor/internal/crew/ledger"
)

// fullRecipe is a project recipe with every phase, valid beside
// managedCrew's tasks (whose one check is "unit").
func fullRecipe() *Recipe {
	return &Recipe{
		Prerequisites: []Check{{ID: "node", Argv: []string{"node", "--version"}}},
		Env:           []string{"NPM_TOKEN"},
		Ports:         []string{"web", "db"},
		Setup:         []Check{{ID: "deps", Argv: []string{"npm", "ci"}, TimeoutSeconds: 900}},
		Services: []Check{
			{ID: "dbserver", Argv: []string{"postgres", "-p", "${CONDUCTOR_PORT_DB}", "-D", "${CONDUCTOR_STATE_DIR}/db"}},
			{ID: "webserver", Argv: []string{"npm", "run", "dev", "--", "--port", "${CONDUCTOR_PORT_WEB}"}},
		},
		Ready: []Probe{
			{ID: "dbready", Argv: []string{"pg_isready", "-p", "${CONDUCTOR_PORT_DB}"}},
			{ID: "webready", URL: "http://127.0.0.1:${CONDUCTOR_PORT_WEB}/health", TimeoutSeconds: 120, IntervalSeconds: 1},
		},
		Preview: "http://localhost:${CONDUCTOR_PORT_WEB}/",
		Verify:  []Check{{ID: "e2e", Argv: []string{"npm", "run", "e2e"}}},
		Cleanup: []Check{{ID: "dropdb", Argv: []string{"rm", "-rf", "${CONDUCTOR_STATE_DIR}/db"}}},
	}
}

// recipeCrew is managedCrew with fullRecipe in place of its legacy setup.
func recipeCrew() Crew {
	c := managedCrew()
	c.Setup = nil
	c.Recipe = fullRecipe()
	return c
}

// steps returns n steps whose ids start with prefix.
func steps(prefix string, n int) []Check {
	var out []Check
	for i := range n {
		out = append(out, Check{ID: fmt.Sprintf("%s%d", prefix, i), Argv: []string{"true"}})
	}
	return out
}

func TestAProjectRecipeWithEveryPhaseIsValid(t *testing.T) {
	if err := recipeCrew().ValidateSave(); err != nil {
		t.Fatal(err)
	}
	// Every list at its bound.
	c := recipeCrew()
	rec := c.Recipe
	rec.Prerequisites, rec.Setup, rec.Verify, rec.Cleanup = steps("p", maxRecipeSteps), steps("s", maxRecipeSteps), steps("v", maxRecipeSteps), steps("c", maxRecipeSteps)
	rec.Services = steps("svc", maxRecipeServices)
	rec.Ready = nil
	for i := range maxRecipeSteps {
		rec.Ready = append(rec.Ready, Probe{ID: fmt.Sprintf("r%d", i), URL: "https://[::1]:8443/", TimeoutSeconds: maxProbeTimeout, IntervalSeconds: maxProbeInterval})
	}
	rec.Env = nil
	for i := range maxRecipeEnv {
		rec.Env = append(rec.Env, fmt.Sprintf("VAR_%d", i))
	}
	rec.Ports = []string{"a", "b", "c", "d", "e", "f", "web", strings.Repeat("p", 16)}
	rec.Preview = "http://127.0.0.1:${CONDUCTOR_PORT_" + strings.ToUpper(strings.Repeat("p", 16)) + "}/" + strings.Repeat("x", maxPreview-len("http://127.0.0.1:${CONDUCTOR_PORT_PPPPPPPPPPPPPPPP}/"))
	if len(rec.Preview) != maxPreview {
		t.Fatalf("the preview is %d bytes", len(rec.Preview))
	}
	if err := c.ValidateSave(); err != nil {
		t.Fatal(err)
	}
	// The file form alone, and a recipe with the setup phase alone.
	c.Recipe = &Recipe{File: true}
	if err := c.ValidateSave(); err != nil {
		t.Fatal(err)
	}
	c.Recipe = &Recipe{Setup: []Check{{ID: "deps", Argv: []string{"npm", "ci"}}}}
	if err := c.ValidateSave(); err != nil {
		t.Fatal(err)
	}
}

// The validation table: every rule of a crew's project recipe, each case
// refused with words that name it (web/app/utils/crews.test.ts holds the
// same cases for recipeProblems).
func TestAProjectRecipeIsHeldToItsRules(t *testing.T) {
	big := strings.Repeat("x", maxArg)
	cases := []struct {
		name  string
		edit  func(c *Crew)
		words string
	}{
		{"a recipe without tasks", func(c *Crew) {
			c.Tasks = nil
			c.Members[0].Start, c.Members[1].Start = Start{When: StartImmediately}, Start{When: StartImmediately}
		}, "a project recipe is only set with tasks"},
		{"setup beside the recipe", func(c *Crew) { c.Setup = []Check{{ID: "old", Argv: []string{"true"}}} }, "setup is the project recipe's setup phase: put it there"},
		{"schema in a crew's recipe", func(c *Crew) { c.Recipe.Schema = 1 }, "schema is only set in conductor.recipe.json"},
		{"file beside a phase", func(c *Crew) { c.Recipe.File = true }, "with file, the recipe is conductor.recipe.json at each tree's commit: set nothing else"},
		{"too many prerequisites", func(c *Crew) { c.Recipe.Prerequisites = steps("p", maxRecipeSteps+1) }, "too many prerequisites (at most 8)"},
		{"too many setup steps", func(c *Crew) { c.Recipe.Setup = steps("s", maxRecipeSteps+1) }, "too many setup steps (at most 8)"},
		{"too many services", func(c *Crew) { c.Recipe.Services = steps("svc", maxRecipeServices+1) }, "too many services (at most 4)"},
		{"too many ready probes", func(c *Crew) {
			for i := range maxRecipeSteps - 1 {
				c.Recipe.Ready = append(c.Recipe.Ready, Probe{ID: fmt.Sprintf("r%d", i), Argv: []string{"true"}})
			}
		}, "too many ready probes (at most 8)"},
		{"too many verify steps", func(c *Crew) { c.Recipe.Verify = steps("v", maxRecipeSteps+1) }, "too many verify steps (at most 8)"},
		{"too many cleanup steps", func(c *Crew) { c.Recipe.Cleanup = steps("c", maxRecipeSteps+1) }, "too many cleanup steps (at most 8)"},
		{"too many env names", func(c *Crew) {
			for i := range maxRecipeEnv {
				c.Recipe.Env = append(c.Recipe.Env, fmt.Sprintf("VAR_%d", i))
			}
		}, "too many env names (at most 32)"},
		{"too many ports", func(c *Crew) { c.Recipe.Ports = append(c.Recipe.Ports, "a", "b", "c", "d", "e", "f", "g") }, "too many ports (at most 8)"},
		{"a step with a bad id", func(c *Crew) { c.Recipe.Setup[0].ID = "Deps" }, "project recipe: setup[0]: id \"Deps\" must match"},
		{"a step with no program", func(c *Crew) { c.Recipe.Cleanup[0].Argv = nil }, "cleanup[0]: argv must name a program"},
		{"a step's argv past its bound", func(c *Crew) { c.Recipe.Verify[0].Argv = []string{"x", big, big} }, "verify[0]: argv must be at most 8192 bytes in all"},
		{"a step's timeout past the bound", func(c *Crew) { c.Recipe.Prerequisites[0].TimeoutSeconds = maxCheckTimeout + 1 }, "prerequisites[0]: timeoutSeconds must be between 1 and 3600"},
		{"a service with a timeout", func(c *Crew) { c.Recipe.Services[0].TimeoutSeconds = 60 }, "services[0]: a service has no timeoutSeconds: it runs as long as its tree"},
		{"an id in two lists", func(c *Crew) { c.Recipe.Cleanup[0].ID = "deps" }, "cleanup[0]: id \"deps\" is used twice in the recipe"},
		{"a probe's id taken by a step", func(c *Crew) { c.Recipe.Ready[0].ID = "webserver" }, "ready[0]: id \"webserver\" is used twice in the recipe"},
		{"an id of a task's check", func(c *Crew) { c.Recipe.Setup[0].ID = "unit" }, "setup[0]: id \"unit\" is also a check of task \"build\""},
		{"a bad env name", func(c *Crew) { c.Recipe.Env = []string{"NPM-TOKEN"} }, "env[0]: \"NPM-TOKEN\" is not a variable name"},
		{"an env name starting with a digit", func(c *Crew) { c.Recipe.Env = []string{"1PASSWORD"} }, "env[0]: \"1PASSWORD\" is not a variable name"},
		{"a CONDUCTOR_ env name", func(c *Crew) { c.Recipe.Env = []string{"CONDUCTOR_NOTIFY_TOKEN"} }, "env[0]: \"CONDUCTOR_NOTIFY_TOKEN\" is Conductor's own"},
		{"a loader", func(c *Crew) { c.Recipe.Env = []string{"PATH", "LD_PRELOAD"} }, "env[1]: \"LD_PRELOAD\" is a loader's variable"},
		{"an env name twice", func(c *Crew) { c.Recipe.Env = []string{"NPM_TOKEN", "NPM_TOKEN"} }, "env[1]: \"NPM_TOKEN\" is named twice"},
		{"a port name in capitals", func(c *Crew) { c.Recipe.Ports = []string{"Web"} }, "ports[0]: \"Web\" must match"},
		{"a port name past 16", func(c *Crew) { c.Recipe.Ports = []string{strings.Repeat("p", 17)} }, "ports[0]: \"ppppppppppppppppp\" must match"},
		{"a port name twice", func(c *Crew) { c.Recipe.Ports = []string{"web", "db", "web"} }, "ports[2]: \"web\" is named twice"},
		{"a placeholder naming no port", func(c *Crew) { c.Recipe.Setup[0].Argv = []string{"npm", "ci", "--port=${CONDUCTOR_PORT_API}"} }, "setup[0]: argv[2]: \"${CONDUCTOR_PORT_API}\" names no port of the recipe"},
		{"a port placeholder in lower case", func(c *Crew) { c.Recipe.Services[1].Argv[5] = "${CONDUCTOR_PORT_web}" }, "services[1]: argv[5]: \"${CONDUCTOR_PORT_web}\" names no port of the recipe"},
		{"a placeholder naming no port in a probe's URL", func(c *Crew) { c.Recipe.Ready[1].URL = "http://127.0.0.1:${CONDUCTOR_PORT_API}/" }, "ready[1]: url: \"${CONDUCTOR_PORT_API}\" names no port of the recipe"},
		{"a placeholder naming no port in the preview", func(c *Crew) { c.Recipe.Preview = "http://localhost:${CONDUCTOR_PORT_UI}/" }, "preview: \"${CONDUCTOR_PORT_UI}\" names no port of the recipe"},
		{"a port placeholder in a prerequisite", func(c *Crew) {
			c.Recipe.Prerequisites[0].Argv = []string{"nc", "-z", "127.0.0.1", "${CONDUCTOR_PORT_DB}"}
		}, "prerequisites[0]: argv[3]: a prerequisite runs before any tree: no placeholders"},
		{"the state directory in a prerequisite", func(c *Crew) { c.Recipe.Prerequisites[0].Argv = []string{"mkdir", "${CONDUCTOR_STATE_DIR}/cache"} }, "prerequisites[0]: argv[1]: a prerequisite runs before any tree: no placeholders"},
		{"a probe with argv and url", func(c *Crew) { c.Recipe.Ready[0].URL = "http://127.0.0.1/" }, "ready[0]: a probe has argv or url, not both"},
		{"a probe with neither", func(c *Crew) { c.Recipe.Ready[0].Argv = nil }, "ready[0]: a probe needs argv or url"},
		{"a probe with a bad id", func(c *Crew) { c.Recipe.Ready[0].ID = "db.ready" }, "ready[0]: id \"db.ready\" must match"},
		{"a probe's argv with no program", func(c *Crew) { c.Recipe.Ready[0].Argv = []string{""} }, "ready[0]: argv must name a program"},
		{"a probe's URL off the loopback", func(c *Crew) { c.Recipe.Ready[1].URL = "http://example.com/health" }, "ready[1]: url \"http://example.com/health\" must be http or https on 127.0.0.1, localhost or [::1]"},
		{"a probe's URL on a host that starts as the loopback", func(c *Crew) { c.Recipe.Ready[1].URL = "http://127.0.0.1.example.com/" }, "must be http or https on 127.0.0.1, localhost or [::1]"},
		{"a probe's URL with a user", func(c *Crew) { c.Recipe.Ready[1].URL = "http://localhost@example.com/" }, "must be http or https on 127.0.0.1, localhost or [::1]"},
		{"a probe's URL of another scheme", func(c *Crew) { c.Recipe.Ready[1].URL = "ftp://127.0.0.1/" }, "must be http or https on 127.0.0.1, localhost or [::1]"},
		{"a probe's URL with a space", func(c *Crew) { c.Recipe.Ready[1].URL = "http://127.0.0.1/a b" }, "must be http or https on 127.0.0.1, localhost or [::1]"},
		{"a probe's URL past 2048", func(c *Crew) { c.Recipe.Ready[1].URL = "http://127.0.0.1/" + strings.Repeat("x", maxProbeURL) }, "ready[1]: url is longer than 2048 bytes"},
		{"a probe's port of 0", func(c *Crew) { c.Recipe.Ready[1].URL = "http://127.0.0.1:0/" }, "the port must be 1 to 65535"},
		{"a probe's port past 65535", func(c *Crew) { c.Recipe.Ready[1].URL = "http://[::1]:65536/" }, "the port must be 1 to 65535"},
		{"a probe's timeout past the bound", func(c *Crew) { c.Recipe.Ready[1].TimeoutSeconds = maxProbeTimeout + 1 }, "ready[1]: timeoutSeconds must be between 1 and 3600 (60 when left out)"},
		{"a probe's negative timeout", func(c *Crew) { c.Recipe.Ready[1].TimeoutSeconds = -1 }, "ready[1]: timeoutSeconds must be between 1 and 3600"},
		{"a probe's interval past the bound", func(c *Crew) { c.Recipe.Ready[1].IntervalSeconds = maxProbeInterval + 1 }, "ready[1]: intervalSeconds must be between 1 and 60 (2 when left out)"},
		{"a preview off the loopback", func(c *Crew) { c.Recipe.Preview = "https://preview.example.com/" }, "preview \"https://preview.example.com/\" must be http or https on 127.0.0.1, localhost or [::1]"},
		{"a preview past 512", func(c *Crew) { c.Recipe.Preview = "http://localhost/" + strings.Repeat("x", maxPreview) }, "preview is longer than 512 bytes"},
		{"a recipe past 64 KiB as JSON", func(c *Crew) {
			c.Recipe.Setup = nil
			for i := range maxRecipeSteps {
				c.Recipe.Setup = append(c.Recipe.Setup, Check{ID: fmt.Sprintf("s%d", i), Argv: []string{big, big}})
			}
		}, "bytes as JSON, more than 65536"},
	}
	for _, tc := range cases {
		c := recipeCrew()
		tc.edit(&c)
		err := c.Validate()
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.words) {
			t.Errorf("%s: %v (want %q)", tc.name, err, tc.words)
		}
	}
}

// A legacy setup keeps main's rule: its ids unique within setup, and one
// may be a task's check id too, so no stored crew becomes invalid.
func TestALegacySetupKeepsItsRules(t *testing.T) {
	c := managedCrew()
	c.Setup = []Check{{ID: "unit", Argv: []string{"npm", "ci"}}}
	if err := c.ValidateSave(); err != nil {
		t.Fatalf("a legacy setup sharing a check's id: %v", err)
	}
	c.Setup = append(c.Setup, Check{ID: "unit", Argv: []string{"true"}})
	if err := c.Validate(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "setup[1]: id \"unit\" is used twice") {
		t.Fatalf("a setup id twice: %v", err)
	}
	// An empty recipe is none: beside a setup, and without tasks.
	c = managedCrew()
	c.Recipe = &Recipe{}
	if err := c.ValidateSave(); err != nil {
		t.Fatalf("an empty recipe beside a setup: %v", err)
	}
	c.Tasks, c.Setup = nil, nil
	c.Members[0].Start, c.Members[1].Start = Start{When: StartImmediately}, Start{When: StartImmediately}
	if err := c.ValidateSave(); err != nil {
		t.Fatalf("an empty recipe without tasks: %v", err)
	}
}

// effectiveRecipe is the crew's recipe, or its legacy setup lifted into the
// setup phase, or empty; a copy either way.
func TestTheEffectiveRecipeLiftsALegacySetup(t *testing.T) {
	c := managedCrew()
	got := c.effectiveRecipe()
	if !slices.EqualFunc(got.Setup, c.Setup, func(a, b Check) bool { return a.ID == b.ID && slices.Equal(a.Argv, b.Argv) }) || len(got.Setup) != 1 {
		t.Fatalf("the lifted setup: %+v", got)
	}
	rest := got
	rest.Setup = nil
	if !rest.empty() {
		t.Fatalf("a lifted setup has more than its setup phase: %+v", got)
	}
	got.Setup[0].Argv[0] = "changed"
	if c.Setup[0].Argv[0] != "npm" {
		t.Fatal("the lifted setup shares the crew's argv")
	}
	r := recipeCrew()
	if eff := r.effectiveRecipe(); !sameRecipe(eff, *r.Recipe) {
		t.Fatalf("the recipe: %+v", eff)
	}
	if eff := testCrew(immediate("lead", "x")).effectiveRecipe(); !eff.empty() {
		t.Fatalf("a crew without either: %+v", eff)
	}
	// The run reads the setup phase from it: a legacy setup and a recipe's
	// setup alike.
	e, _ := newEngine(t)
	for _, c := range []Crew{managedCrew(), func() Crew { c := managedCrew(); c.Recipe, c.Setup = &Recipe{Setup: c.Setup}, nil; return c }()} {
		r := e.add(c, "", "")
		e.launched(r)
		if len(r.setup) != 1 || r.setup[0].ID != "deps" {
			t.Fatalf("the run's setup: %+v", r.setup)
		}
	}
}

// expand puts each placeholder in literally, inside one argv element, a
// probe's URL or the preview: never splitting the element, never expanding
// what it put in, leaving a placeholder that names no port as it is.
func TestExpandPutsThePlaceholdersInLiterally(t *testing.T) {
	ports := map[string]int{"web": 41234, "db_2": 5433}
	state := "/tmp/a dir/with $1 and ${CONDUCTOR_PORT_WEB}/.conductor/state"
	argv := []string{"server", "--port=${CONDUCTOR_PORT_WEB}", "--data", "${CONDUCTOR_STATE_DIR}/db", "${CONDUCTOR_PORT_DB_2}:${CONDUCTOR_PORT_WEB}"}
	var got []string
	for _, a := range argv {
		got = append(got, expand(a, ports, state))
	}
	want := []string{"server", "--port=41234", "--data", state + "/db", "5433:41234"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv: %q", got)
	}
	if u := expand("http://127.0.0.1:${CONDUCTOR_PORT_WEB}/health?db=${CONDUCTOR_PORT_DB_2}", ports, state); u != "http://127.0.0.1:41234/health?db=5433" {
		t.Fatalf("url: %s", u)
	}
	if p := expand("http://localhost:${CONDUCTOR_PORT_WEB}/", ports, state); p != "http://localhost:41234/" {
		t.Fatalf("preview: %s", p)
	}
	for _, kept := range []string{"${CONDUCTOR_PORT_API}", "${CONDUCTOR_PORT_web}", "${CONDUCTOR_TASK}", "$CONDUCTOR_PORT_WEB", "${CONDUCTOR_STATE_DIRX}", "no placeholder"} {
		if got := expand(kept, ports, state); got != kept {
			t.Errorf("%s expanded to %s", kept, got)
		}
	}
}

// The launch of a recipe that names a phase, the file form or a
// placeholder this build does not run yet is refused before anything is
// made; a recipe with its setup phase alone, and a legacy setup whatever
// its argv holds, launch as main launches a setup.
func TestALaunchOfARecipeThisBuildDoesNotRunIsRefused(t *testing.T) {
	setup := []Check{{ID: "deps", Argv: []string{"npm", "ci"}}}
	cases := []struct {
		name  string
		rec   Recipe
		words string
	}{
		{"the file form", Recipe{File: true}, "this build does not read a project recipe from conductor.recipe.json yet"},
		{"prerequisites", Recipe{Setup: setup, Prerequisites: []Check{{ID: "node", Argv: []string{"node", "--version"}}}}, "this build does not run a project recipe's prerequisites yet"},
		{"env", Recipe{Setup: setup, Env: []string{"NPM_TOKEN"}}, "this build does not pass a project recipe's env names on yet"},
		{"ports", Recipe{Setup: setup, Ports: []string{"web"}}, "this build does not give a project recipe's ports yet"},
		{"services", Recipe{Services: []Check{{ID: "web", Argv: []string{"npm", "start"}}}}, "this build does not run a project recipe's services yet"},
		{"ready", Recipe{Ready: []Probe{{ID: "up", Argv: []string{"true"}}}}, "this build does not run a project recipe's ready probes yet"},
		{"preview", Recipe{Preview: "http://127.0.0.1:8080/"}, "this build does not offer a project recipe's preview yet"},
		{"verify", Recipe{Verify: []Check{{ID: "e2e", Argv: []string{"npm", "test"}}}}, "this build does not run a project recipe's verify steps yet"},
		{"cleanup", Recipe{Cleanup: []Check{{ID: "rm", Argv: []string{"rm", "-rf", "tmp"}}}}, "this build does not run a project recipe's cleanup steps yet"},
		{"the state directory in a setup step", Recipe{Setup: []Check{{ID: "cache", Argv: []string{"mkdir", "-p", "${CONDUCTOR_STATE_DIR}/cache"}}}},
			"this build does not expand a project recipe's placeholders yet: setup[0] (\"cache\") names ${CONDUCTOR_STATE_DIR} in argv[2]"},
	}
	e, _ := newEngine(t)
	for _, tc := range cases {
		c := managedCrew()
		c.Setup, c.Recipe = nil, &tc.rec
		if err := c.ValidateSave(); err != nil {
			t.Fatalf("%s: the recipe is not valid: %v", tc.name, err)
		}
		_, err := e.Launch(t.Context(), c)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.words) {
			t.Errorf("%s: %v (want %q)", tc.name, err, tc.words)
		}
	}
	if runs := e.List(); len(runs) != 0 {
		t.Fatalf("a refused launch kept a run: %+v", runs)
	}
	// Its setup phase alone runs as a legacy setup does; a legacy setup is
	// not a recipe saved ahead of its engine: its argv is passed on as main
	// passes it.
	c := managedCrew()
	c.Setup, c.Recipe = nil, &Recipe{Setup: setup}
	if err := c.runnableRecipe(); err != nil {
		t.Fatalf("a recipe with its setup alone: %v", err)
	}
	c = managedCrew()
	c.Setup = []Check{{ID: "cache", Argv: []string{"mkdir", "-p", "${CONDUCTOR_STATE_DIR}/cache"}}}
	if err := c.runnableRecipe(); err != nil {
		t.Fatalf("a legacy setup: %v", err)
	}
	if err := managedCrew().runnableRecipe(); err != nil {
		t.Fatalf("a legacy crew: %v", err)
	}
}

// A crew's recipe with its setup phase alone runs as a legacy setup runs:
// in every new attempt worktree, and in the checks' checkout.
func TestARecipesSetupRunsAsALegacySetupDoes(t *testing.T) {
	dir := newRepo(t)
	e, _, l := managedEngine(t)
	build := work("build", "lead")
	build.Checks = []Check{{ID: "dep", Argv: []string{"sh", "-c", "test -f dep.txt"}}}
	c := repoCrew(dir, build)
	c.Recipe = &Recipe{Setup: []Check{{ID: "deps", Argv: []string{"sh", "-c", "echo dep > dep.txt"}}}}
	run := launchManaged(t, e, c)
	a := waitAttempt(t, e, run.ID, "build", ledger.AttemptRunning)
	if _, err := os.Stat(filepath.Join(a.Attempt.Worktree, "dep.txt")); err != nil {
		t.Fatalf("the recipe's setup did not run in the worktree: %v", err)
	}
	if n := l.count(run.ID, "attempt.setup"); n != 1 {
		t.Fatalf("attempt.setup events: %d", n)
	}
	writeFile(t, filepath.Join(a.Attempt.Worktree, "built.txt"), "yes\n")
	commitAll(t, a.Attempt.Worktree, "built")
	submit(t, e, a.Attempt.SessionID, Result{Summary: "built"})
	waitTask(t, e, run.ID, "build", ledger.TaskSucceeded, 1)
	if n := l.count(run.ID, "check.setup"); n != 1 {
		t.Fatalf("check.setup events: %d", n)
	}
	// The run's row keeps the crew as launched, recipe and all.
	var launched Crew
	l.mu.Lock()
	err := json.Unmarshal([]byte(l.runs[run.ID].Crew), &launched)
	l.mu.Unlock()
	if err != nil || launched.Recipe == nil || len(launched.Recipe.Setup) != 1 || len(launched.Setup) != 0 {
		t.Fatalf("the crew as launched: %+v %v", launched, err)
	}
	stopRun(t, e, run.ID)
}

// recipeRepo is a repository whose crew directory is sub/, with a commit
// holding sub/conductor.recipe.json as content, and that commit's id.
func recipeRepo(t *testing.T, content string) (dir, commit string) {
	t.Helper()
	dir = newRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "sub", recipeFile), content)
	commitAll(t, dir, "the recipe")
	return dir, runGit(t, dir, "rev-parse", "HEAD")
}

// The file form is read at a commit, its blob id its identity, decoded
// strictly and held to a recipe's rules.
func TestTheFileFormIsReadAtACommit(t *testing.T) {
	dir, commit := recipeRepo(t, `{
  "schema": 1,
  "setup": [{"id": "deps", "argv": ["npm", "ci"]}],
  "ports": ["web"],
  "services": [{"id": "web", "argv": ["npm", "run", "dev", "--", "--port", "${CONDUCTOR_PORT_WEB}"]}],
  "ready": [{"id": "up", "url": "http://127.0.0.1:${CONDUCTOR_PORT_WEB}/"}],
  "preview": "http://localhost:${CONDUCTOR_PORT_WEB}/"
}
`)
	ctx := t.Context()
	sub := filepath.Join(dir, "sub")
	rec, blob, err := resolveRecipe(ctx, sub, commit, "sub/", managedCrew().Tasks)
	if err != nil {
		t.Fatal(err)
	}
	if want := runGit(t, dir, "rev-parse", commit+":sub/"+recipeFile); blob != want {
		t.Fatalf("the blob is %s, not %s", blob, want)
	}
	if rec.Schema != 1 || len(rec.Setup) != 1 || rec.Setup[0].Argv[1] != "ci" || rec.Services[0].ID != "web" || rec.Ready[0].URL == "" || rec.Preview != "http://localhost:${CONDUCTOR_PORT_WEB}/" {
		t.Fatalf("the recipe: %+v", rec)
	}
	// Read at the commit, not from the working tree: a change not
	// committed is not the recipe, and a later commit's is its own.
	writeFile(t, filepath.Join(sub, recipeFile), `{"schema": 1, "setup": [{"id": "other", "argv": ["true"]}]}`)
	if again, blob2, err := resolveRecipe(ctx, sub, commit, "sub/", nil); err != nil || blob2 != blob || again.Setup[0].ID != "deps" {
		t.Fatalf("read again: %+v %s %v", again, blob2, err)
	}
	commitAll(t, dir, "another recipe")
	next := runGit(t, dir, "rev-parse", "HEAD")
	if later, blob3, err := resolveRecipe(ctx, sub, next, "sub/", nil); err != nil || blob3 == blob || later.Setup[0].ID != "other" {
		t.Fatalf("the later commit's: %+v %s %v", later, blob3, err)
	}
	// From the top of the repository too: the path is the prefix's.
	if _, _, err := resolveRecipe(ctx, dir, commit, "sub/", nil); err != nil {
		t.Fatalf("from the top: %v", err)
	}
	if _, _, err := resolveRecipe(ctx, dir, commit, "", nil); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "does not hold conductor.recipe.json") {
		t.Fatalf("a commit without the file: %v", err)
	}
	if _, _, err := resolveRecipe(ctx, dir, "HEAD", "sub/", nil); err == nil || !strings.Contains(err.Error(), "is not a commit id") {
		t.Fatalf("a revision that is not an id: %v", err)
	}
}

func TestTheFileFormRefusesWhatIsNotARecipe(t *testing.T) {
	big := `{"schema": 1, "setup": [{"id": "deps", "argv": ["true"]}]}` + strings.Repeat(" ", maxRecipeFile)
	cases := []struct {
		name, content, words string
	}{
		{"schema 2", `{"schema": 2, "setup": [{"id": "deps", "argv": ["true"]}]}`, "schema 2 needs a newer Conductor (this one reads schema 1)"},
		{"no schema", `{"setup": [{"id": "deps", "argv": ["true"]}]}`, "schema must be 1"},
		{"file in the file", `{"schema": 1, "file": true}`, "file is set in a crew's recipe, not in conductor.recipe.json"},
		{"an unknown field", `{"schema": 1, "setups": []}`, `unknown field "setups"`},
		{"more than one value", `{"schema": 1} {"schema": 1}`, "more than one JSON value"},
		{"not JSON", `schema: 1`, "invalid character"},
		{"a file past 256 KiB", big, fmt.Sprintf("is %d bytes, more than %d", len(big), maxRecipeFile)},
		{"a step id of a task's check", `{"schema": 1, "setup": [{"id": "unit", "argv": ["true"]}]}`, `setup[0]: id "unit" is also a check of task "build"`},
		{"a placeholder naming no port", `{"schema": 1, "setup": [{"id": "deps", "argv": ["x", "${CONDUCTOR_PORT_WEB}"]}]}`, `names no port of the recipe`},
	}
	for _, tc := range cases {
		dir, commit := recipeRepo(t, tc.content)
		_, _, err := resolveRecipe(t.Context(), dir, commit, "sub/", managedCrew().Tasks)
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.words) || !strings.Contains(err.Error(), "sub/conductor.recipe.json at "+short(commit)) {
			t.Errorf("%s: %v (want %q)", tc.name, err, tc.words)
		}
	}
	// A link, a directory and a submodule in the file's place are named,
	// never decoded.
	dir := newRepo(t)
	writeFile(t, filepath.Join(dir, "elsewhere.json"), `{"schema": 1}`)
	if err := os.Symlink("elsewhere.json", filepath.Join(dir, recipeFile)); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "a link")
	link := runGit(t, dir, "rev-parse", "HEAD")
	if _, _, err := resolveRecipe(t.Context(), dir, link, "", nil); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "is a symbolic link") {
		t.Errorf("a link: %v", err)
	}
	runGit(t, dir, "rm", "-q", recipeFile)
	if err := os.MkdirAll(filepath.Join(dir, recipeFile), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, recipeFile, "x.json"), "{}")
	commitAll(t, dir, "a directory")
	tree := runGit(t, dir, "rev-parse", "HEAD")
	if _, _, err := resolveRecipe(t.Context(), dir, tree, "", nil); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("a directory: %v", err)
	}
	runGit(t, dir, "rm", "-rq", recipeFile)
	runGit(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+link+","+recipeFile)
	runGit(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "a submodule")
	gitlink := runGit(t, dir, "rev-parse", "HEAD")
	if _, _, err := resolveRecipe(t.Context(), dir, gitlink, "", nil); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "is a submodule") {
		t.Errorf("a submodule: %v", err)
	}
}
