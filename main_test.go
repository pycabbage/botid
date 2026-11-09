package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/cmd"
	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/repo"
	"github.com/pycabbage/botid/internal/version"
)

type result struct {
	code      int
	stdout    string
	stderr    string
	appCalled bool
	gitDirs   []string
}

// runWith runs botid with a Git that reports every directory as outside a repository.
// The working directory of the process is home.
func runWith(t *testing.T, home, stdin string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	r := result{}
	r.code = run(args, &stdout, &stderr, func() (*cmd.App, error) {
		r.appCalled = true
		return &cmd.App{
			Home:   home,
			Cwd:    home,
			Stdin:  strings.NewReader(stdin),
			Stdout: &stdout,
			Stderr: &stderr,
			Exe:    func() (string, error) { return "/opt/botid/bin/botid", nil },
			Git: func(_ context.Context, dir string, args ...string) ([]byte, error) {
				if len(args) > 0 && args[0] == "config" {
					return []byte("user.name\nbot[bot]\x00user.email\n1+bot[bot]@users.noreply.github.com\x00" +
						"credential.https://github.com.helper\n!/usr/bin/gh auth git-credential\x00"), nil
				}
				r.gitDirs = append(r.gitDirs, dir)
				return nil, &repo.ExitError{Code: 128, Stderr: "fatal: not a git repository (or any of the parent directories): .git\n"}
			},
		}, nil
	})
	r.stdout, r.stderr = stdout.String(), stderr.String()
	return r
}

func writeConfig(t *testing.T, home string, c config.Config) {
	t.Helper()
	dir := filepath.Join(home, ".config", "botid")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(filepath.Join(dir, "config.json"), c); err != nil {
		t.Fatal(err)
	}
}

// markComplete gives the identity what botid init leaves: a hosts.yml and a git config file with every setting.
// runWith's Git answers the questions about a repository only, so it reports that this file has every setting.
func markComplete(t *testing.T, home, name string) {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "botid", "identities", name)
	for file, content := range map[string]string{
		filepath.Join(dir, "gh", "hosts.yml"): "hosts\n",
		filepath.Join(dir, "git", "config"):   "[user]\n",
	} {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func botConfig(defaultIdentity string) config.Config {
	return config.Config{
		Default: defaultIdentity,
		Identities: map[string]config.Identity{
			"bot": {ClientID: "Iv23liTEST", PrivateKeyPath: "/k.pem", InstallationID: 1},
		},
	}
}

func TestHelpPrintsUsageToStdoutWithExitCode0(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		t.Run(strings.Join(append([]string{"botid"}, args...), " "), func(t *testing.T) {
			r := runWith(t, t.TempDir(), "", args...)
			if r.code != 0 {
				t.Errorf("exit code = %d, want 0", r.code)
			}
			if r.stdout != usage {
				t.Errorf("stdout =\n%s\nwant\n%s", r.stdout, usage)
			}
			if r.stderr != "" {
				t.Errorf("stderr = %q, want empty", r.stderr)
			}
			if r.appCalled {
				t.Error("App was created when printing usage")
			}
		})
	}
}

func TestUsageText(t *testing.T) {
	want := "Usage: botid <command>\n" +
		"\n" +
		"Commands:\n" +
		"  init     Set up credentials, git/gh isolation and the systemd timer\n" +
		"  issue    Issue a GitHub App installation token and write it to the botid store\n" +
		"  env      Print the environment variables that point gh and git at the identity selected for a directory, in .env or JSON format\n" +
		"  version  Print the version\n" +
		"  help     Show this usage\n"
	if usage != want {
		t.Errorf("usage =\n%s\nwant\n%s", usage, want)
	}
}

func TestVersionPrintsTheVersionWithExitCode0(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	version.Version = "from-ldflags"

	for _, args := range [][]string{{"version"}, {"--version"}} {
		t.Run(strings.Join(append([]string{"botid"}, args...), " "), func(t *testing.T) {
			home := t.TempDir()
			r := runWith(t, home, "", args...)
			if r.code != 0 {
				t.Errorf("exit code = %d, want 0", r.code)
			}
			if want := "botid version from-ldflags\n"; r.stdout != want {
				t.Errorf("stdout = %q, want %q", r.stdout, want)
			}
			if r.stderr != "" {
				t.Errorf("stderr = %q, want empty", r.stderr)
			}
			if r.appCalled {
				t.Error("App was created when printing the version")
			}
			if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
				t.Errorf("home contents = %v (err %v), want it untouched", entries, err)
			}
		})
	}
}

func TestVersionWithoutALinkedVersionPrintsWhatGoStamped(t *testing.T) {
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	version.Version = ""

	r := runWith(t, t.TempDir(), "", "version")

	if r.code != 0 {
		t.Errorf("exit code = %d, want 0", r.code)
	}
	if want := "botid version " + version.String() + "\n"; r.stdout != want || version.String() == "" {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
}

func TestUnknownCommandPrintsUsageToStderrWithExitCode2(t *testing.T) {
	r := runWith(t, t.TempDir(), "", "bogus")
	if r.code != 2 {
		t.Errorf("exit code = %d, want 2", r.code)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
	if want := "botid: unknown command \"bogus\"\n\n" + usage; r.stderr != want {
		t.Errorf("stderr =\n%s\nwant\n%s", r.stderr, want)
	}
	if r.appCalled {
		t.Error("App was created for an unknown command")
	}
}

func TestExtraArgumentsExitWithCode2(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"init", "extra"}, "botid: unexpected extra arguments for init: [\"extra\"]\n\n" + usage},
		{[]string{"issue", "--force"}, "botid: unexpected extra arguments for issue: [\"--force\"]\n\n" + usage},
		{[]string{"version", "extra"}, "botid: unexpected extra arguments for version: [\"extra\"]\n\n" + usage},
		{[]string{"--version", "-v"}, "botid: unexpected extra arguments for --version: [\"-v\"]\n\n" + usage},
		{[]string{"env", "a", "b"}, "botid: unexpected extra arguments for env: [\"a\" \"b\"]\n\n" + usage},
		{[]string{"env", "--cwd", ".", "a"}, "botid: unexpected extra arguments for env: [\"a\"]\n\n" + usage},
		{[]string{"env", "--format", "yaml"}, "botid: --format must be dotenv or json, not \"yaml\"\n\n" + usage},
		{[]string{"env", "--format="}, "botid: --format must be dotenv or json, not \"\"\n\n" + usage},
		{[]string{"env", "--bogus"}, "botid: flag provided but not defined: -bogus\n\n" + usage},
		{[]string{"env", "--cwd"}, "botid: flag needs an argument: -cwd\n\n" + usage},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			home := t.TempDir()
			r := runWith(t, home, "", tt.args...)
			if r.code != 2 {
				t.Errorf("exit code = %d, want 2", r.code)
			}
			if r.stdout != "" {
				t.Errorf("stdout = %q, want empty", r.stdout)
			}
			if r.stderr != tt.want {
				t.Errorf("stderr =\n%s\nwant\n%s", r.stderr, tt.want)
			}
			if r.appCalled {
				t.Error("App was created despite an argument error")
			}
		})
	}
}

func TestRuntimeErrorsAreOneLineOnStderrWithExitCode1(t *testing.T) {
	t.Run("issue: config.json is missing", func(t *testing.T) {
		home := t.TempDir()
		r := runWith(t, home, "", "issue")
		if r.code != 1 {
			t.Errorf("exit code = %d, want 1", r.code)
		}
		if r.stdout != "" {
			t.Errorf("stdout = %q, want empty", r.stdout)
		}
		want := "botid: " + home + "/.config/botid/config.json does not exist; run botid init\n"
		if r.stderr != want {
			t.Errorf("stderr = %q, want %q", r.stderr, want)
		}
	})

	t.Run("init: Client ID is empty", func(t *testing.T) {
		if _, err := os.Stat("/run/systemd/system"); err != nil {
			t.Skipf("init checks /run/systemd/system before it reads any input: %v", err)
		}
		r := runWith(t, t.TempDir(), "\n", "init")
		if r.code != 1 {
			t.Errorf("exit code = %d, want 1", r.code)
		}
		if want := "botid: Client ID is empty\n"; r.stderr != want {
			t.Errorf("stderr = %q, want %q", r.stderr, want)
		}
	})

	t.Run("cannot create App", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run([]string{"env"}, &stdout, &stderr, func() (*cmd.App, error) {
			return nil, errors.New("home directory is not an absolute path: \"x\"")
		})
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if want := "botid: home directory is not an absolute path: \"x\"\n"; stderr.String() != want {
			t.Errorf("stderr = %q, want %q", stderr.String(), want)
		}
	})
}

func TestEnvPrintsTheDefaultIdentityWithExitCode0(t *testing.T) {
	gh := func(home string) string { return home + "/.local/share/botid/identities/bot/gh" }
	git := func(home string) string { return home + "/.local/share/botid/identities/bot/git/config" }
	dotenv := func(home string) string {
		return "GH_CONFIG_DIR='" + gh(home) + "'\nGIT_CONFIG_GLOBAL='" + git(home) + "'\n"
	}
	jsonEnv := func(home string) string {
		return `{"GH_CONFIG_DIR":"` + gh(home) + `","GIT_CONFIG_GLOBAL":"` + git(home) + `"}` + "\n"
	}
	tests := []struct {
		name string
		args []string
		want func(home string) string
	}{
		{"dotenv is the default", []string{"env"}, dotenv},
		{"--format dotenv", []string{"env", "--format", "dotenv"}, dotenv},
		{"--format json", []string{"env", "--format", "json"}, jsonEnv},
		{"--format=json", []string{"env", "--format=json"}, jsonEnv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeConfig(t, home, botConfig("bot"))
			markComplete(t, home, "bot")

			r := runWith(t, home, "", tt.args...)

			if r.code != 0 {
				t.Errorf("exit code = %d, want 0 (stderr: %q)", r.code, r.stderr)
			}
			if want := tt.want(home); r.stdout != want {
				t.Errorf("stdout = %q, want %q", r.stdout, want)
			}
			if r.stderr != "" {
				t.Errorf("stderr = %q, want empty", r.stderr)
			}
		})
	}
}

func TestEnvForTheUserIdentityPrintsNothing(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, botConfig(config.User))

	r := runWith(t, home, "", "env")
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Errorf("env = (%d, %q, %q), want (0, empty, empty)", r.code, r.stdout, r.stderr)
	}
	r = runWith(t, home, "", "env", "--format", "json")
	if r.code != 0 || r.stdout != "{}\n" || r.stderr != "" {
		t.Errorf("env --format json = (%d, %q, %q), want (0, {}, empty)", r.code, r.stdout, r.stderr)
	}
}

func TestEnvPassesCwdToGit(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, botConfig("bot"))
	markComplete(t, home, "bot")
	if err := os.MkdirAll(filepath.Join(home, "proj", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := runWith(t, home, "", "env", "--cwd", "proj/sub")

	if r.code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %q)", r.code, r.stderr)
	}
	if len(r.gitDirs) == 0 {
		t.Fatal("git was not run")
	}
	for _, dir := range r.gitDirs {
		if want := filepath.Join(home, "proj", "sub"); dir != want {
			t.Errorf("git ran in %q, want %q", dir, want)
		}
	}
}

func TestEnvWithoutConfigExitsWithCode1(t *testing.T) {
	home := t.TempDir()

	r := runWith(t, home, "", "env", "--format", "json")

	if r.code != 1 {
		t.Errorf("exit code = %d, want 1", r.code)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
	want := "botid: " + home + "/.config/botid/config.json does not exist; run botid init\n"
	if r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
}

func TestNewAppWiresProductionDependencies(t *testing.T) {
	t.Setenv("HOME", "/home/alice")

	app, err := newApp()
	if err != nil {
		t.Fatalf("newApp: %v", err)
	}
	if app.Home != "/home/alice" {
		t.Errorf("Home = %q, want %q", app.Home, "/home/alice")
	}
	client := app.Client("Iv23liTEST", nil)
	if client.BaseURL != "https://api.github.com" || client.ClientID != "Iv23liTEST" || client.HTTP != nil || client.Now != nil {
		t.Errorf("Client = %+v, want BaseURL=https://api.github.com / ClientID=Iv23liTEST / HTTP and Now nil", client)
	}
	if app.Exec == nil || app.Git == nil || app.Systemd == nil || app.Exe == nil || app.Stdin == nil || app.Stdout == nil || app.Stderr == nil {
		t.Errorf("App has unset dependencies: %+v", app)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if app.Cwd != wd {
		t.Errorf("Cwd = %q, want the working directory %q", app.Cwd, wd)
	}
}

func TestNewAppRejectsRelativeHome(t *testing.T) {
	t.Setenv("HOME", "relative/home")
	if _, err := newApp(); err == nil {
		t.Fatal("expected an error")
	}
}
