package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/cmd"
)

type result struct {
	code      int
	stdout    string
	stderr    string
	appCalled bool
}

func runWith(t *testing.T, home, stdin string, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	r := result{}
	r.code = run(args, &stdout, &stderr, func() (*cmd.App, error) {
		r.appCalled = true
		return &cmd.App{
			Home:   home,
			Stdin:  strings.NewReader(stdin),
			Stdout: &stdout,
			Stderr: &stderr,
			Exe:    func() (string, error) { return "/opt/botid/bin/botid", nil },
		}, nil
	})
	r.stdout, r.stderr = stdout.String(), stderr.String()
	return r
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
		"  init   Set up credentials, git/gh isolation and the systemd timer\n" +
		"  issue  Issue a GitHub App installation token and write it to the botid store\n" +
		"  env    Print the environment variables that point gh and git at the botid store, in .env format\n" +
		"  help   Show this usage\n"
	if usage != want {
		t.Errorf("usage =\n%s\nwant\n%s", usage, want)
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
		{[]string{"env", "a", "b"}, "botid: unexpected extra arguments for env: [\"a\" \"b\"]\n\n" + usage},
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

func TestEnvRunsWithExitCode0(t *testing.T) {
	home := t.TempDir()
	r := runWith(t, home, "", "env")
	if r.code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr: %q)", r.code, r.stderr)
	}
	want := "GH_CONFIG_DIR='" + home + "/.local/share/botid/gh'\n" +
		"GIT_CONFIG_GLOBAL='" + home + "/.local/share/botid/git/config'\n"
	if r.stdout != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
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
	if app.Exec == nil || app.Systemd == nil || app.Exe == nil || app.Stdin == nil || app.Stdout == nil || app.Stderr == nil {
		t.Errorf("App has unset dependencies: %+v", app)
	}
}

func TestNewAppRejectsRelativeHome(t *testing.T) {
	t.Setenv("HOME", "relative/home")
	if _, err := newApp(); err == nil {
		t.Fatal("expected an error")
	}
}
