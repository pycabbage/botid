package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/cmd"
	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/repo"
	"github.com/pycabbage/botid/internal/store"
)

func TestTwoIdentitiesInOneHomeDoNotShareCredentials(t *testing.T) {
	guardRealHome(t)
	requireTools(t, "gh", "git")
	home := t.TempDir()
	alpha := newSandboxFor(home, "alpha")
	beta := newSandboxFor(home, "beta")
	for _, tt := range []struct {
		s           sandbox
		slug, token string
	}{
		{alpha, "alpha-app", "ghs_alpha"},
		{beta, "beta-app", "ghs_beta"},
	} {
		tt.s.writeHostsFor(t, tt.slug, tt.token)
		tt.s.ensureGitConfigDir(t)
		tt.s.setGitUser(t, tt.slug+"[bot]", "1+"+tt.slug+"[bot]@users.noreply.github.com")
		tt.s.mustRun(t, "", "gh", "auth", "setup-git")
	}

	for _, tt := range []struct {
		s           sandbox
		slug, token string
	}{
		{alpha, "alpha-app", "ghs_alpha"},
		{beta, "beta-app", "ghs_beta"},
	} {
		if got := strings.TrimSpace(tt.s.mustRun(t, "", "gh", "auth", "token")); got != tt.token {
			t.Errorf("%s: gh auth token = %q, want %q", tt.slug, got, tt.token)
		}
		if got := strings.TrimSpace(tt.s.mustRun(t, "", "git", "config", "--global", "--get", "user.name")); got != tt.slug+"[bot]" {
			t.Errorf("%s: user.name = %q", tt.slug, got)
		}
		out := tt.s.mustRun(t, "protocol=https\nhost=github.com\n\n", "git", "credential", "fill")
		for _, want := range []string{"username=" + tt.slug + "[bot]\n", "password=" + tt.token + "\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: git credential fill output does not contain %q:\n%s", tt.slug, want, out)
			}
		}
	}
}

// envOf converts the JSON that botid env prints into the environment of a child process.
func envOf(t *testing.T, out string) []string {
	t.Helper()
	var vars map[string]string
	if err := json.Unmarshal([]byte(out), &vars); err != nil {
		t.Fatalf("botid env printed invalid JSON %q: %v", out, err)
	}
	var env []string
	for k, v := range vars {
		env = append(env, k+"="+v)
	}
	slices.Sort(env)
	return env
}

// runWithEnv runs a command with exactly env, plus a PATH and an empty home directory, so that a command
// without GH_CONFIG_DIR or GIT_CONFIG_GLOBAL finds no configuration at all.
func runWithEnv(t *testing.T, emptyHome string, env []string, name string, args ...string) (string, error) {
	t.Helper()
	c := exec.CommandContext(t.Context(), name, args...)
	c.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"HOME=" + emptyHome,
		"XDG_CONFIG_HOME=" + filepath.Join(emptyHome, ".config"),
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
	}, env...)
	c.Dir = emptyHome
	out, err := c.Output()
	return strings.TrimSpace(string(out)), err
}

func TestEnvSelectsTheIdentityForRealRepositoriesAndAppliesItToGhAndGit(t *testing.T) {
	guardRealHome(t)
	requireTools(t, "gh", "git")
	root := isolateGit(t)

	botidHome := t.TempDir()
	p := paths.New(botidHome)
	tokens := map[string]string{"alpha": "ghs_alpha", "beta": "ghs_beta"}
	for name, token := range tokens {
		s := newSandboxFor(botidHome, name)
		s.writeHostsFor(t, name+"-app", token)
		s.ensureGitConfigDir(t)
		git(t, root, "config", "--file", s.id.GitConfigFile, "user.email", name+"@example.com")
	}
	if err := store.EnsureDir(p.ConfigDir); err != nil {
		t.Fatal(err)
	}
	c := config.Config{
		Default: "alpha",
		Identities: map[string]config.Identity{
			"alpha": {ClientID: "Iv23liA", PrivateKeyPath: "/k/a.pem", InstallationID: 1},
			"beta":  {ClientID: "Iv23liB", PrivateKeyPath: "/k/b.pem", InstallationID: 2},
		},
		Rules: []config.Rule{
			{Remote: "github.com/octocorp/*", Identity: "beta"},
			{Remote: "github.com/octocorp/*", Branch: "release/**", Identity: "user"},
			{Path: filepath.Join(root, "private") + "/**", Identity: "user"},
		},
	}
	if err := config.Save(p.ConfigFile, c); err != nil {
		t.Fatal(err)
	}

	plain := newRepo(t, filepath.Join(root, "plain"))
	api := newRepo(t, filepath.Join(root, "api"))
	git(t, api, "remote", "add", "origin", "git@github.com:OctoCorp/api.git")
	apiSub := filepath.Join(api, "pkg", "x")
	if err := os.MkdirAll(apiSub, 0o700); err != nil {
		t.Fatal(err)
	}
	notes := newRepo(t, filepath.Join(root, "private", "notes"))
	git(t, notes, "remote", "add", "origin", "https://github.com/me/notes.git")
	outside := filepath.Join(root, "not-a-repo")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	emptyHome := t.TempDir()

	check := func(t *testing.T, cwd, want string) {
		t.Helper()
		var out bytes.Buffer
		app := &cmd.App{Home: botidHome, Cwd: root, Stdout: &out, Git: repo.ExecGit}
		if err := app.Env(t.Context(), cwd, cmd.FormatJSON); err != nil {
			t.Fatalf("Env(%q): %v", cwd, err)
		}
		env := envOf(t, out.String())

		wantEnv := []string(nil)
		if want != config.User {
			id := p.Identity(want)
			wantEnv = []string{"GH_CONFIG_DIR=" + id.GHConfigDir, "GIT_CONFIG_GLOBAL=" + id.GitConfigFile}
		}
		if !reflect.DeepEqual(env, wantEnv) {
			t.Fatalf("Env(%q) = %v, want the identity %q: %v", cwd, env, want, wantEnv)
		}

		token, tokenErr := runWithEnv(t, emptyHome, env, "gh", "auth", "token")
		email, emailErr := runWithEnv(t, emptyHome, env, "git", "config", "--global", "--get", "user.email")
		if want == config.User {
			if tokenErr == nil {
				t.Errorf("Env(%q): gh auth token = %q, want the user identity to have no bot token", cwd, token)
			}
			if emailErr == nil {
				t.Errorf("Env(%q): git user.email = %q, want the user identity to have no bot e-mail address", cwd, email)
			}
			return
		}
		if tokenErr != nil || token != tokens[want] {
			t.Errorf("Env(%q): gh auth token = %q (%v), want %q", cwd, token, tokenErr, tokens[want])
		}
		if emailErr != nil || email != want+"@example.com" {
			t.Errorf("Env(%q): git user.email = %q (%v), want %q", cwd, email, emailErr, want+"@example.com")
		}
	}

	t.Run("a repository without a rule uses the default identity", func(t *testing.T) {
		check(t, plain, "alpha")
	})
	t.Run("a directory outside any repository uses the default identity", func(t *testing.T) {
		check(t, outside, "alpha")
	})
	t.Run("the remote rule matches through scp-like URL and mixed case", func(t *testing.T) {
		check(t, api, "beta")
	})
	t.Run("a subdirectory follows its repository", func(t *testing.T) {
		check(t, apiSub, "beta")
	})
	t.Run("the path rule selects the user identity", func(t *testing.T) {
		check(t, notes, "user")
	})
	t.Run("a linked worktree on a release branch uses the branch rule while the main worktree keeps the repository rule", func(t *testing.T) {
		linked := filepath.Join(root, "api-release")
		git(t, api, "worktree", "add", "-b", "release/1.0", linked)
		check(t, linked, "user")
		check(t, api, "beta")
	})
	t.Run("a linked worktree on another branch follows the repository rule", func(t *testing.T) {
		linked := filepath.Join(root, "api-feature")
		git(t, api, "worktree", "add", "-b", "feature/x", linked)
		check(t, linked, "beta")
	})
	t.Run("switching the branch in the same directory switches the identity", func(t *testing.T) {
		git(t, api, "switch", "-c", "release/2.0/rc")
		check(t, api, "user")
		git(t, api, "switch", "main")
		check(t, api, "beta")
	})
	t.Run("a detached HEAD ignores the branch rule", func(t *testing.T) {
		git(t, api, "switch", "--detach", "release/2.0/rc")
		check(t, api, "beta")
		git(t, api, "switch", "main")
	})
}
