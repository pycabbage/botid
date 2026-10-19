package integration

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/store"
)

const (
	botSlug  = "example-app"
	botToken = "ghs_dummy"
)

type sandbox struct {
	home string
	p    paths.Paths
}

func newSandbox(t *testing.T) sandbox {
	t.Helper()
	home := t.TempDir()
	return sandbox{home: home, p: paths.New(home)}
}

// The child process environment does not inherit os.Environ(); everything except PATH is built here.
// Tokens such as GH_TOKEN, DBUS_SESSION_BUS_ADDRESS and XDG_RUNTIME_DIR are not passed.
func (s sandbox) env() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"HOME=" + s.home,
		"XDG_CONFIG_HOME=" + filepath.Join(s.home, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(s.home, ".local", "share"),
		"XDG_CACHE_HOME=" + filepath.Join(s.home, ".cache"),
		"XDG_STATE_HOME=" + filepath.Join(s.home, ".local", "state"),
		"GH_CONFIG_DIR=" + s.p.GHConfigDir,
		"GIT_CONFIG_GLOBAL=" + s.p.GitConfigFile,
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
	}
}

type result struct {
	stdout string
	stderr string
	err    error
}

func (s sandbox) exec(t *testing.T, stdin string, name string, args ...string) result {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Env = s.env()
	cmd.Dir = s.home
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return result{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func (s sandbox) mustRun(t *testing.T, stdin string, name string, args ...string) string {
	t.Helper()
	r := s.exec(t, stdin, name, args...)
	if r.err != nil {
		t.Fatalf("%s %s: %v\nstdout:\n%s\nstderr:\n%s", name, strings.Join(args, " "), r.err, r.stdout, r.stderr)
	}
	return r.stdout
}

func (s sandbox) writeHosts(t *testing.T) {
	t.Helper()
	if err := store.EnsureDir(s.p.DataDir); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureDir(s.p.GHConfigDir); err != nil {
		t.Fatal(err)
	}
	data, err := store.HostsYAML(botSlug, botToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteFileAtomic(s.p.HostsFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (s sandbox) ensureGitConfigDir(t *testing.T) {
	t.Helper()
	if err := store.EnsureDir(s.p.DataDir); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureDir(s.p.GitConfigDir); err != nil {
		t.Fatal(err)
	}
}

func (s sandbox) setBotIdentity(t *testing.T) {
	t.Helper()
	s.mustRun(t, "", "git", "config", "--global", "user.name", "example-app[bot]")
	s.mustRun(t, "", "git", "config", "--global", "user.email", "123+example-app[bot]@users.noreply.github.com")
}

func requireTools(t *testing.T, names ...string) {
	t.Helper()
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not on PATH", name)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type statSnapshot struct {
	exists  bool
	modTime time.Time
}

func snapshot(t *testing.T, path string) statSnapshot {
	t.Helper()
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return statSnapshot{}
	}
	if err != nil {
		t.Fatal(err)
	}
	return statSnapshot{exists: true, modTime: fi.ModTime()}
}

func (a statSnapshot) equal(b statSnapshot) bool {
	return a.exists == b.exists && a.modTime.Equal(b.modTime)
}

func TestIntegration(t *testing.T) {
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	guarded := []string{
		filepath.Join(realHome, ".config", "gh"),
		filepath.Join(realHome, ".gitconfig"),
	}
	before := make([]statSnapshot, len(guarded))
	for i, path := range guarded {
		before[i] = snapshot(t, path)
	}
	t.Cleanup(func() {
		for i, path := range guarded {
			after := snapshot(t, path)
			if !before[i].equal(after) {
				t.Errorf("existence or modification time of %s changed: before=%+v after=%+v", path, before[i], after)
			}
		}
	})

	t.Run("gh auth token", func(t *testing.T) {
		requireTools(t, "gh")
		s := newSandbox(t)
		s.writeHosts(t)

		got := strings.TrimSpace(s.mustRun(t, "", "gh", "auth", "token"))
		if got != "ghs_dummy" {
			t.Errorf("gh auth token = %q, want %q", got, "ghs_dummy")
		}
	})

	t.Run("gh auth setup-git", func(t *testing.T) {
		requireTools(t, "gh", "git")
		s := newSandbox(t)
		s.writeHosts(t)
		s.ensureGitConfigDir(t)

		s.mustRun(t, "", "gh", "auth", "setup-git")

		var section []string
		inSection := false
		for _, line := range strings.Split(readFile(t, s.p.GitConfigFile), "\n") {
			trimmed := strings.TrimSpace(line)
			switch {
			case trimmed == `[credential "https://github.com"]`:
				inSection = true
			case strings.HasPrefix(trimmed, "["):
				inSection = false
			case inSection:
				section = append(section, trimmed)
			}
		}
		if len(section) == 0 {
			t.Fatalf("[credential \"https://github.com\"] not found:\n%s", readFile(t, s.p.GitConfigFile))
		}
		hasReset := false
		var ghHelper string
		for _, line := range section {
			if line == "helper =" {
				hasReset = true
			}
			if strings.HasPrefix(line, "helper = !") && strings.HasSuffix(line, " auth git-credential") {
				ghHelper = line
			}
		}
		if !hasReset {
			t.Errorf("missing `helper =` line: %q", section)
		}
		if ghHelper == "" {
			t.Errorf("missing `helper = !<gh> auth git-credential` line: %q", section)
		}
		t.Logf("embedded helper: %s", ghHelper)
	})

	t.Run("git user config", func(t *testing.T) {
		requireTools(t, "git")
		s := newSandbox(t)
		s.ensureGitConfigDir(t)

		s.setBotIdentity(t)

		gotName := strings.TrimSpace(s.mustRun(t, "", "git", "config", "--global", "--get", "user.name"))
		if gotName != "example-app[bot]" {
			t.Errorf("user.name = %q, want %q", gotName, "example-app[bot]")
		}
		gotEmail := strings.TrimSpace(s.mustRun(t, "", "git", "config", "--global", "--get", "user.email"))
		if gotEmail != "123+example-app[bot]@users.noreply.github.com" {
			t.Errorf("user.email = %q, want %q", gotEmail, "123+example-app[bot]@users.noreply.github.com")
		}
	})

	t.Run("git does not create missing parent dir", func(t *testing.T) {
		requireTools(t, "git")
		s := newSandbox(t)
		if _, err := os.Stat(s.p.GitConfigDir); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("precondition: GitConfigDir must not exist: stat err = %v", err)
		}

		r := s.exec(t, "", "git", "config", "--global", "user.name", "example-app[bot]")

		var exitErr *exec.ExitError
		if !errors.As(r.err, &exitErr) || exitErr.ExitCode() == 0 {
			t.Errorf("git config --global must exit with a non-zero code: err = %v", r.err)
		}
		if !strings.Contains(r.stderr, "could not lock config file") {
			t.Errorf("stderr does not contain `could not lock config file`: %q", r.stderr)
		}
		if _, err := os.Stat(s.p.GitConfigDir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("GitConfigDir was created: stat err = %v", err)
		}
		if _, err := os.Stat(s.p.GitConfigFile); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("GitConfigFile was created: stat err = %v", err)
		}
	})

	t.Run("git credential fill", func(t *testing.T) {
		requireTools(t, "gh", "git")
		s := newSandbox(t)
		s.writeHosts(t)
		s.ensureGitConfigDir(t)
		s.setBotIdentity(t)
		s.mustRun(t, "", "gh", "auth", "setup-git")

		out := s.mustRun(t, "protocol=https\nhost=github.com\n\n", "git", "credential", "fill")

		for _, want := range []string{"username=example-app[bot]\n", "password=ghs_dummy\n"} {
			if !strings.Contains(out, want) {
				t.Errorf("git credential fill output does not contain %q:\n%s", want, out)
			}
		}
	})
}
