package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/pycabbage/botid/internal/cmd"
	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/repo"
)

type fakeGit struct {
	mu   sync.Mutex
	dirs []string
	out  map[string]string
	errs map[string]error
}

func (g *fakeGit) run(_ context.Context, dir string, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	g.mu.Lock()
	defer g.mu.Unlock()
	g.dirs = append(g.dirs, dir)
	if err := g.errs[key]; err != nil {
		return nil, err
	}
	return []byte(g.out[key]), nil
}

func newFakeGit(path, branch string, remoteURLs ...string) *fakeGit {
	g := &fakeGit{out: map[string]string{}, errs: map[string]error{}}
	g.out["worktree list --porcelain -z"] = "worktree " + path + "\x00HEAD 0123456789abcdef\x00branch refs/heads/" + branch + "\x00\x00"
	if branch == "" {
		g.errs["symbolic-ref -q HEAD"] = &repo.ExitError{Code: 1}
	} else {
		g.out["symbolic-ref -q HEAD"] = "refs/heads/" + branch + "\n"
	}
	var remotes strings.Builder
	for i, u := range remoteURLs {
		fmt.Fprintf(&remotes, "r%d\t%s (fetch)\nr%d\t%s (push)\n", i, u, i, u)
	}
	g.out["remote -v"] = remotes.String()
	return g
}

func newFakeGitOutsideRepo() *fakeGit {
	g := &fakeGit{out: map[string]string{}, errs: map[string]error{}}
	g.errs["worktree list --porcelain -z"] = &repo.ExitError{Code: 128, Stderr: "fatal: not a git repository (or any of the parent directories): .git\n"}
	return g
}

func envConfig() config.Config {
	return config.Config{
		Default: "acme-bot",
		Identities: map[string]config.Identity{
			"acme-bot":     {ClientID: "Iv23liA", PrivateKeyPath: "/k/a.pem", InstallationID: 11},
			"octocorp-bot": {ClientID: "Iv23liB", PrivateKeyPath: "/k/b.pem", InstallationID: 22},
		},
		Rules: []config.Rule{
			{Remote: "github.com/octocorp/*", Identity: "octocorp-bot"},
			{Remote: "github.com/octocorp/*", Branch: "release/**", Identity: "user"},
		},
	}
}

// envSetup saves c, makes every App identity of it complete, and makes g the git of the app.
func (h *harness) envSetup(c config.Config, g *fakeGit) string {
	h.t.Helper()
	h.saveConfig(c)
	h.markReady(g, c.AppNames()...)
	work := h.path("work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		h.t.Fatal(err)
	}
	h.app.Cwd = work
	h.app.Git = g.run
	return work
}

// repositoryDirs returns the directories of the calls of g that ask about a repository,
// not about the git config file of an identity, which runs in the git directory of the identity.
func (g *fakeGit) repositoryDirs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var dirs []string
	for _, dir := range g.dirs {
		if !strings.Contains(dir, "/identities/") {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func (g *fakeGit) repositoryCalls() int {
	return len(g.repositoryDirs())
}

func (h *harness) dotEnvOf(name string) string {
	return "GH_CONFIG_DIR='" + h.home + "/.local/share/botid/identities/" + name + "/gh'\n" +
		"GIT_CONFIG_GLOBAL='" + h.home + "/.local/share/botid/identities/" + name + "/git/config'\n"
}

func (h *harness) jsonEnvOf(name string) string {
	return `{"GH_CONFIG_DIR":"` + h.home + `/.local/share/botid/identities/` + name + `/gh",` +
		`"GIT_CONFIG_GLOBAL":"` + h.home + `/.local/share/botid/identities/` + name + `/git/config"}` + "\n"
}

func TestEnvPrintsTheSelectedIdentity(t *testing.T) {
	tests := []struct {
		name       string
		git        *fakeGit
		wantDotEnv func(h *harness) string
		wantJSON   func(h *harness) string
	}{
		{
			name:       "default identity when no rule matches",
			git:        newFakeGit("/src/bar", "main", "https://github.com/foo/bar.git"),
			wantDotEnv: func(h *harness) string { return h.dotEnvOf("acme-bot") },
			wantJSON:   func(h *harness) string { return h.jsonEnvOf("acme-bot") },
		},
		{
			name:       "remote rule",
			git:        newFakeGit("/src/api", "main", "git@github.com:OctoCorp/api.git"),
			wantDotEnv: func(h *harness) string { return h.dotEnvOf("octocorp-bot") },
			wantJSON:   func(h *harness) string { return h.jsonEnvOf("octocorp-bot") },
		},
		{
			name:       "branch rule selects the user identity",
			git:        newFakeGit("/src/api", "release/1.0", "https://github.com/octocorp/api"),
			wantDotEnv: func(*harness) string { return "" },
			wantJSON:   func(*harness) string { return "{}\n" },
		},
		{
			name:       "detached HEAD falls back to the remote rule",
			git:        newFakeGit("/src/api", "", "https://github.com/octocorp/api"),
			wantDotEnv: func(h *harness) string { return h.dotEnvOf("octocorp-bot") },
			wantJSON:   func(h *harness) string { return h.jsonEnvOf("octocorp-bot") },
		},
		{
			name:       "outside a repository",
			git:        newFakeGitOutsideRepo(),
			wantDotEnv: func(h *harness) string { return h.dotEnvOf("acme-bot") },
			wantJSON:   func(h *harness) string { return h.jsonEnvOf("acme-bot") },
		},
	}
	for _, tt := range tests {
		for _, format := range []string{cmd.FormatDotEnv, cmd.FormatJSON} {
			t.Run(tt.name+" ("+format+")", func(t *testing.T) {
				h := newHarness(t, "")
				h.envSetup(envConfig(), tt.git)

				if err := h.app.Env(context.Background(), "", format); err != nil {
					t.Fatalf("Env: %v", err)
				}

				want := tt.wantDotEnv(h)
				if format == cmd.FormatJSON {
					want = tt.wantJSON(h)
				}
				if got := h.stdout.String(); got != want {
					t.Errorf("stdout = %q, want %q", got, want)
				}
				if got := h.stderr.String(); got != "" {
					t.Errorf("stderr = %q, want empty", got)
				}
			})
		}
	}
}

func TestEnvAppliesAPathRuleToADirectoryOutsideAnyRepository(t *testing.T) {
	c := envConfig()
	c.Rules = append(c.Rules, config.Rule{Path: "~/work/plain", Identity: "octocorp-bot"})
	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{"the directory the rule names", "work/plain", "octocorp-bot"},
		{"another directory", "work/other", "acme-bot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "")
			h.envSetup(c, newFakeGitOutsideRepo())
			if err := os.MkdirAll(h.path(tt.cwd), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := h.app.Env(context.Background(), h.path(tt.cwd), cmd.FormatDotEnv); err != nil {
				t.Fatalf("Env: %v", err)
			}

			if got, want := h.stdout.String(), h.dotEnvOf(tt.want); got != want {
				t.Errorf("stdout = %q, want %q", got, want)
			}
		})
	}
}

func TestEnvPrintsTheUserIdentityAsNothing(t *testing.T) {
	c := envConfig()
	c.Default = config.User

	t.Run("dotenv", func(t *testing.T) {
		h := newHarness(t, "")
		h.envSetup(c, newFakeGit("/src/bar", "main", "https://github.com/foo/bar"))
		if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
			t.Fatalf("Env: %v", err)
		}
		if got := h.stdout.String(); got != "" {
			t.Errorf("stdout = %q, want empty", got)
		}
	})
	t.Run("json", func(t *testing.T) {
		h := newHarness(t, "")
		h.envSetup(c, newFakeGit("/src/bar", "main", "https://github.com/foo/bar"))
		if err := h.app.Env(context.Background(), "", cmd.FormatJSON); err != nil {
			t.Fatalf("Env: %v", err)
		}
		if got := h.stdout.String(); got != "{}\n" {
			t.Errorf("stdout = %q, want {}", got)
		}
	})
}

func TestEnvResolvesCwd(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
		want func(h *harness) string
	}{
		{"empty is the working directory of the process", "", func(h *harness) string { return h.path("work") }},
		{"dot", ".", func(h *harness) string { return h.path("work") }},
		{"relative is joined to the working directory", "sub", func(h *harness) string { return h.path("work", "sub") }},
		{"relative with dot-dot", "../other", func(h *harness) string { return h.path("other") }},
		{"absolute", "ABS", func(h *harness) string { return h.path("abs") }},
		{"tilde is the home directory", "~", func(h *harness) string { return h.home }},
		{"tilde slash is below the home directory", "~/proj/x", func(h *harness) string { return h.path("proj", "x") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "")
			g := newFakeGit("/src/bar", "main", "https://github.com/foo/bar")
			h.envSetup(envConfig(), g)
			for _, d := range []string{"work/sub", "other", "abs", "proj/x"} {
				if err := os.MkdirAll(h.path(d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			cwd := tt.cwd
			if cwd == "ABS" {
				cwd = h.path("abs") + "/"
			}

			if err := h.app.Env(context.Background(), cwd, cmd.FormatDotEnv); err != nil {
				t.Fatalf("Env: %v", err)
			}

			want := tt.want(h)
			dirs := g.repositoryDirs()
			if len(dirs) == 0 {
				t.Fatal("git was not run")
			}
			for _, d := range dirs {
				if d != want {
					t.Errorf("git ran in %q, want %q", d, want)
				}
			}
		})
	}
}

func TestEnvAsksGitThreeQuestionsAboutTheDirectory(t *testing.T) {
	h := newHarness(t, "")
	g := newFakeGit("/src/bar", "main", "https://github.com/foo/bar")
	h.envSetup(envConfig(), g)

	if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
		t.Fatalf("Env: %v", err)
	}

	if got := g.repositoryCalls(); got != 3 {
		t.Errorf("git asked about the repository %d times, want 3", got)
	}
}

func TestEnvTouchesNothingWhenTheIdentityIsComplete(t *testing.T) {
	h := newHarness(t, "")
	g := newFakeGit("/src/api", "main", "https://github.com/octocorp/api")
	h.envSetup(envConfig(), g)
	before := h.snapshot()

	if err := h.app.Env(context.Background(), "", cmd.FormatJSON); err != nil {
		t.Fatalf("Env: %v", err)
	}

	if after := h.snapshot(); !reflect.DeepEqual(after, before) {
		t.Errorf("files changed:\n%v\nwant\n%v", after, before)
	}
	if got := h.stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
	if got := h.gh.requests(); len(got) != 0 {
		t.Errorf("requests were sent to GitHub: %v", got)
	}
	if got := h.rec.lines(); len(got) != 0 {
		t.Errorf("external commands were run: %v", got)
	}
}

// incompleteEnv is a config.json written by hand: one App identity, no files of its own yet.
func (h *harness) incompleteEnv() string {
	h.t.Helper()
	h.saveConfig(config.Config{
		Default:    "my-app",
		Identities: map[string]config.Identity{"my-app": h.appIdentity("my-app", 42)},
	})
	return h.useGitOutsideRepo()
}

func (h *harness) useGitOutsideRepo() string {
	h.t.Helper()
	g := newFakeGitOutsideRepo()
	for k, v := range h.git.out {
		g.out[k] = v
	}
	h.git = g
	h.app.Git = g.run
	work := h.path("work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		h.t.Fatal(err)
	}
	h.app.Cwd = work
	return work
}

func TestEnvCompletesAnIdentityThatConfigJSONNamesButHasNoFiles(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.incompleteEnv()

	if err := h.app.Env(context.Background(), "", cmd.FormatJSON); err != nil {
		t.Fatalf("Env: %v", err)
	}

	if got := h.stdout.String(); got != h.jsonEnvOf("my-app") {
		t.Errorf("stdout = %q, want %q", got, h.jsonEnvOf("my-app"))
	}
	if got := h.read(hostsPath); got != wantHosts {
		t.Errorf("hosts.yml = %q, want %q", got, wantHosts)
	}
	wantCalls := []string{"git config --global user.name my-app[bot]", "git config --global user.email " + botEmail, "gh auth setup-git"}
	if got := h.rec.lines(); !equalLines(got, wantCalls) {
		t.Errorf("external commands = %v, want %v", got, wantCalls)
	}
	for _, c := range h.rec.calls {
		wantEnv := []string{"GH_CONFIG_DIR=" + h.identityDataPath("my-app", "gh"), "GIT_CONFIG_GLOBAL=" + h.gitConfigPath("my-app")}
		if !equalLines(c.Env, wantEnv) {
			t.Errorf("env passed to %s = %v, want %v", c.line(), c.Env, wantEnv)
		}
	}
	wantReqs := []string{"POST /app/installations/42/access_tokens", "GET /app", "GET /users/my-app%5Bbot%5D"}
	if got := h.gh.requests(); !equalLines(got, wantReqs) {
		t.Errorf("GitHub requests = %v, want %v", got, wantReqs)
	}
	wantNotice := `botid: identity "my-app" was incomplete; wrote ` + h.identityDataPath("my-app", "gh", "hosts.yml") +
		" and added user.name, user.email, the gh credential helper to " + h.gitConfigPath("my-app") + "\n"
	if got := h.stderr.String(); got != wantNotice {
		t.Errorf("stderr = %q, want %q", got, wantNotice)
	}
	h.assertNoOutputLeaksToken()
}

func TestEnvDoesNothingMoreOnceTheIdentityIsComplete(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.incompleteEnv()
	if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
		t.Fatalf("first Env: %v", err)
	}
	h.stdout.Reset()
	h.stderr.Reset()
	callsBefore, reqsBefore, filesBefore := len(h.rec.lines()), len(h.gh.requests()), h.snapshot()

	if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
		t.Fatalf("second Env: %v", err)
	}

	if got := h.stdout.String(); got != h.dotEnvOf("my-app") {
		t.Errorf("stdout = %q, want %q", got, h.dotEnvOf("my-app"))
	}
	if got := h.stderr.String(); got != "" {
		t.Errorf("stderr = %q, want empty", got)
	}
	if got := len(h.rec.lines()); got != callsBefore {
		t.Errorf("the second run ran %d more commands", got-callsBefore)
	}
	if got := len(h.gh.requests()); got != reqsBefore {
		t.Errorf("the second run sent %d more requests", got-reqsBefore)
	}
	if after := h.snapshot(); !reflect.DeepEqual(after, filesBefore) {
		t.Errorf("the second run changed files:\n%v\nwant\n%v", after, filesBefore)
	}
}

func TestEnvAddsOnlyWhatTheGitConfigFileLacks(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.incompleteEnv()
	h.writeFile(h.identityDataPath("my-app", "gh", "hosts.yml"), "kept token\n")
	h.writeGitConfigFile(h.git, "my-app", gitListing("My Own Name", "", true))

	if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
		t.Fatalf("Env: %v", err)
	}

	if got, want := h.rec.lines(), []string{"git config --global user.email " + botEmail}; !equalLines(got, want) {
		t.Errorf("external commands = %v, want %v", got, want)
	}
}

func TestEnvCompletesOnlyTheSelectedIdentity(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"}, ghapp.Installation{ID: 43, Login: "acme"})
	h.saveConfig(config.Config{
		Default: "alpha",
		Identities: map[string]config.Identity{
			"alpha": h.appIdentity("alpha", 42),
			"beta":  h.appIdentity("beta", 43),
		},
		Rules: []config.Rule{{Path: "~/work", Identity: "beta"}},
	})
	h.useGitOutsideRepo()

	if err := h.app.Env(context.Background(), h.path("work"), cmd.FormatDotEnv); err != nil {
		t.Fatalf("Env: %v", err)
	}

	if got := h.entries(".local/share/botid/identities"); !equalLines(got, []string{"beta"}) {
		t.Errorf("identity directories = %v, want only the selected [beta]", got)
	}
	for _, r := range h.gh.requests() {
		if strings.Contains(r, "/installations/42/") {
			t.Errorf("request %q was sent for an identity that was not selected", r)
		}
	}
}

func TestEnvDoesNotTouchTheUserIdentity(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.saveConfig(config.Config{
		Default:    config.User,
		Identities: map[string]config.Identity{"my-app": h.appIdentity("my-app", 42)},
	})
	h.useGitOutsideRepo()
	before := h.snapshot()

	if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
		t.Fatalf("Env: %v", err)
	}

	if got := h.stdout.String(); got != "" {
		t.Errorf("stdout = %q, want empty", got)
	}
	if got := h.gh.requests(); len(got) != 0 {
		t.Errorf("requests were sent: %v", got)
	}
	if after := h.snapshot(); !reflect.DeepEqual(after, before) {
		t.Errorf("files changed:\n%v\nwant\n%v", after, before)
	}
}

func TestEnvFailsWhenTheIdentityCannotBeCompleted(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness)
		want  string
	}{
		{
			"GitHub is not reachable",
			func(h *harness) { h.gh.fail["POST /app/installations/42/access_tokens"] = 401 },
			"authentication failed (401)",
		},
		{
			"the private key file is missing",
			func(h *harness) {
				h.saveConfig(config.Config{
					Default:    "my-app",
					Identities: map[string]config.Identity{"my-app": {ClientID: testClientID, PrivateKeyPath: "/nonexistent/key.pem", InstallationID: 42}},
				})
			},
			"cannot read private key",
		},
		{
			"installationId is not set",
			func(h *harness) {
				h.saveConfig(config.Config{
					Default:    "my-app",
					Identities: map[string]config.Identity{"my-app": h.appIdentity("my-app", 0)},
				})
			},
			"has no installationId; run botid init again",
		},
		{
			"git config fails",
			func(h *harness) {
				h.rec.errs["git config --global user.name my-app[bot]"] = errors.New("exit status 128")
			},
			"git config --global user.name my-app[bot] failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
			h.incompleteEnv()
			tt.setup(h)

			err := h.app.Env(context.Background(), "", cmd.FormatDotEnv)

			if err == nil || !strings.HasPrefix(err.Error(), `identity "my-app": `) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to name the identity and contain %q", err, tt.want)
			}
			if h.stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty so that the plugin's command does not run", h.stdout.String())
			}
		})
	}
}

func TestEnvCompletesOnceWhenCommandsStartTogether(t *testing.T) {
	const commands = 8
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.incompleteEnv()

	var wg sync.WaitGroup
	errs := make([]error, commands)
	stdouts := make([]*bytes.Buffer, commands)
	stderrs := make([]*bytes.Buffer, commands)
	for i := range commands {
		app := *h.app
		stdouts[i], stderrs[i] = &bytes.Buffer{}, &bytes.Buffer{}
		app.Stdout, app.Stderr = stdouts[i], stderrs[i]
		wg.Go(func() { errs[i] = app.Env(context.Background(), "", cmd.FormatDotEnv) })
	}
	wg.Wait()

	notices := 0
	for i := range commands {
		if errs[i] != nil {
			t.Errorf("command %d: %v", i, errs[i])
		}
		if got := stdouts[i].String(); got != h.dotEnvOf("my-app") {
			t.Errorf("command %d: stdout = %q, want %q", i, got, h.dotEnvOf("my-app"))
		}
		if strings.Contains(stderrs[i].String(), "was incomplete") {
			notices++
		}
	}
	if notices != 1 {
		t.Errorf("%d commands completed the identity, want 1", notices)
	}
	if got := len(h.rec.lines()); got != 3 {
		t.Errorf("%d git and gh commands ran, want the 3 of one setup: %v", got, h.rec.lines())
	}
	wantReqs := []string{"POST /app/installations/42/access_tokens", "GET /app", "GET /users/my-app%5Bbot%5D"}
	if got := h.gh.requests(); !equalLines(got, wantReqs) {
		t.Errorf("GitHub requests = %v, want one setup's %v", got, wantReqs)
	}
}

func TestEnvFailsWithoutWritingToStdout(t *testing.T) {
	legacy := `{"clientId":"Iv23liABC","privateKeyPath":"/k.pem","installationId":42}`
	tests := []struct {
		name   string
		setup  func(h *harness)
		cwd    string
		format string
		want   string
	}{
		{
			name:   "no config",
			setup:  func(h *harness) { h.app.Git = newFakeGit("/src/bar", "main").run },
			format: cmd.FormatDotEnv,
			want:   "/.config/botid/config.json does not exist; run botid init",
		},
		{
			name: "config of botid v0.1.0 that cannot be migrated",
			setup: func(h *harness) {
				h.write(".config/botid/config.json", legacy)
				h.app.Git = newFakeGit("/src/bar", "main").run
			},
			format: cmd.FormatJSON,
			want:   "from the format of botid v0.1.0: cannot read private key",
		},
		{
			name: "broken config",
			setup: func(h *harness) {
				h.write(".config/botid/config.json", `{"default":"nobody","identities":{}}`)
				h.app.Git = newFakeGit("/src/bar", "main").run
			},
			format: cmd.FormatDotEnv,
			want:   `default refers to the unknown identity "nobody"`,
		},
		{
			name:   "unknown format",
			setup:  func(h *harness) { h.envSetup(envConfig(), newFakeGit("/src/bar", "main")) },
			format: "yaml",
			want:   `unknown format "yaml"; use dotenv or json`,
		},
		{
			name:   "cwd does not exist",
			setup:  func(h *harness) { h.envSetup(envConfig(), newFakeGit("/src/bar", "main")) },
			cwd:    "/no/such/dir",
			format: cmd.FormatDotEnv,
			want:   "cwd does not exist: /no/such/dir",
		},
		{
			name: "git fails for another reason than a missing repository",
			setup: func(h *harness) {
				g := newFakeGitOutsideRepo()
				g.errs["worktree list --porcelain -z"] = &repo.ExitError{Code: 128, Stderr: "fatal: detected dubious ownership in repository\n"}
				h.envSetup(envConfig(), g)
			},
			format: cmd.FormatDotEnv,
			want:   "dubious ownership",
		},
		{
			name: "unparsable remote",
			setup: func(h *harness) {
				g := newFakeGit("/src/api", "main")
				g.out["remote -v"] = "origin https://example.com/x (fetch)\n"
				h.envSetup(envConfig(), g)
			},
			format: cmd.FormatDotEnv,
			want:   "unexpected line",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "")
			tt.setup(h)

			err := h.app.Env(context.Background(), tt.cwd, tt.format)

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if h.stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", h.stdout.String())
			}
		})
	}
}

func TestEnvRejectsHomeWithSingleQuote(t *testing.T) {
	h := newHarness(t, "")
	h.app.Home = h.path("o'brien")
	if err := os.MkdirAll(h.app.Home+"/.config/botid", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(h.app.Home+"/.config/botid/config.json", envConfig()); err != nil {
		t.Fatal(err)
	}
	work := h.path("work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	g := newFakeGit("/src/bar", "main", "https://github.com/foo/bar")
	h.markReady(g, envConfig().AppNames()...)
	h.app.Cwd = work
	h.app.Git = g.run

	t.Run("dotenv", func(t *testing.T) {
		err := h.app.Env(context.Background(), "", cmd.FormatDotEnv)
		if err == nil || !strings.Contains(err.Error(), "must not contain a single quote or newline") {
			t.Fatalf("error = %v, want an error about a path containing a single quote", err)
		}
		if h.stdout.Len() != 0 {
			t.Errorf("output was written to stdout on error: %q", h.stdout.String())
		}
	})
	t.Run("json escapes it", func(t *testing.T) {
		if err := h.app.Env(context.Background(), "", cmd.FormatJSON); err != nil {
			t.Fatalf("Env: %v", err)
		}
		if got := h.stdout.String(); !strings.Contains(got, "o'brien/.local/share/botid/identities/acme-bot/gh") {
			t.Errorf("stdout = %q", got)
		}
	})
}
