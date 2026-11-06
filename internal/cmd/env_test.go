package cmd_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/pycabbage/botid/internal/cmd"
	"github.com/pycabbage/botid/internal/config"
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

func (g *fakeGit) calledDirs() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.dirs...)
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

func (h *harness) envSetup(c config.Config, g *fakeGit) string {
	h.t.Helper()
	h.saveConfig(c)
	work := h.path("work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		h.t.Fatal(err)
	}
	h.app.Cwd = work
	h.app.Git = g.run
	return work
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
			dirs := g.calledDirs()
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

	if got := len(g.calledDirs()); got != 3 {
		t.Errorf("git ran %d times, want 3", got)
	}
}

func TestEnvTouchesNothing(t *testing.T) {
	h := newHarness(t, "")
	h.envSetup(envConfig(), newFakeGit("/src/api", "main", "https://github.com/octocorp/api"))
	before := h.read(".config/botid/config.json")

	if err := h.app.Env(context.Background(), "", cmd.FormatJSON); err != nil {
		t.Fatalf("Env: %v", err)
	}

	if got := h.read(".config/botid/config.json"); got != before {
		t.Errorf("config.json changed:\n%s\nwant\n%s", got, before)
	}
	if got := h.entries(".local"); len(got) != 0 {
		t.Errorf("%v was created under .local; env must not write", got)
	}
	if got := h.gh.requests(); len(got) != 0 {
		t.Errorf("requests were sent to GitHub: %v", got)
	}
	if got := h.rec.lines(); len(got) != 0 {
		t.Errorf("external commands were run: %v", got)
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
	h.app.Cwd = work
	h.app.Git = newFakeGit("/src/bar", "main", "https://github.com/foo/bar").run

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
