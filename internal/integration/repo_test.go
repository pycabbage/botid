package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/repo"
)

// isolateGit points git at an empty home directory, so that the tests do not read
// the configuration of the user who runs them. It returns a directory for repositories.
func isolateGit(t *testing.T) string {
	t.Helper()
	requireTools(t, "git")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GIT_CONFIG_GLOBAL", "")
	if err := os.Unsetenv("GIT_CONFIG_GLOBAL"); err != nil {
		t.Fatal(err)
	}
	gitconfig := "[user]\n\tname = test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n"
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(gitconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func newRepo(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init")
	git(t, dir, "commit", "--allow-empty", "-m", "init")
	return dir
}

func inspect(t *testing.T, cwd string) repo.Info {
	t.Helper()
	info, err := repo.Inspect(t.Context(), repo.ExecGit, cwd)
	if err != nil {
		t.Fatalf("Inspect(%q) error = %v", cwd, err)
	}
	return info
}

func TestInspectWithRealGit(t *testing.T) {
	t.Run("main and linked worktree share the path", func(t *testing.T) {
		root := isolateGit(t)
		main := newRepo(t, filepath.Join(root, "repo"))
		linked := filepath.Join(root, "linked")
		git(t, main, "worktree", "add", "-b", "feature/x", linked)

		if got, want := inspect(t, main), (repo.Info{Path: main, Branch: "main"}); !reflect.DeepEqual(got, want) {
			t.Errorf("main worktree = %+v, want %+v", got, want)
		}
		if got, want := inspect(t, linked), (repo.Info{Path: main, Branch: "feature/x"}); !reflect.DeepEqual(got, want) {
			t.Errorf("linked worktree = %+v, want %+v", got, want)
		}
	})

	t.Run("subdirectory", func(t *testing.T) {
		root := isolateGit(t)
		main := newRepo(t, filepath.Join(root, "repo"))
		sub := filepath.Join(main, "a", "b")
		if err := os.MkdirAll(sub, 0o700); err != nil {
			t.Fatal(err)
		}

		if got, want := inspect(t, sub), (repo.Info{Path: main, Branch: "main"}); !reflect.DeepEqual(got, want) {
			t.Errorf("Inspect() = %+v, want %+v", got, want)
		}
	})

	t.Run("git switch changes the branch", func(t *testing.T) {
		root := isolateGit(t)
		main := newRepo(t, filepath.Join(root, "repo"))

		git(t, main, "switch", "-c", "release/1.0")
		if got := inspect(t, main).Branch; got != "release/1.0" {
			t.Errorf("Branch = %q, want %q", got, "release/1.0")
		}
		git(t, main, "switch", "main")
		if got := inspect(t, main).Branch; got != "main" {
			t.Errorf("Branch = %q, want %q", got, "main")
		}
	})

	t.Run("branch named like a tag", func(t *testing.T) {
		root := isolateGit(t)
		main := newRepo(t, filepath.Join(root, "repo"))
		git(t, main, "tag", "foo")
		git(t, main, "switch", "-c", "foo")

		if got := inspect(t, main).Branch; got != "foo" {
			t.Errorf("Branch = %q, want %q", got, "foo")
		}
	})

	t.Run("detached HEAD", func(t *testing.T) {
		root := isolateGit(t)
		main := newRepo(t, filepath.Join(root, "repo"))
		git(t, main, "checkout", "--detach")

		if got, want := inspect(t, main), (repo.Info{Path: main}); !reflect.DeepEqual(got, want) {
			t.Errorf("Inspect() = %+v, want %+v", got, want)
		}
	})

	t.Run("unborn branch", func(t *testing.T) {
		root := isolateGit(t)
		dir := filepath.Join(root, "repo")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "init", "-b", "trunk")

		if got, want := inspect(t, dir), (repo.Info{Path: dir, Branch: "trunk"}); !reflect.DeepEqual(got, want) {
			t.Errorf("Inspect() = %+v, want %+v", got, want)
		}
	})

	t.Run("bare repository", func(t *testing.T) {
		root := isolateGit(t)
		dir := filepath.Join(root, "bare.git")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "init", "--bare")

		if got, want := inspect(t, dir), (repo.Info{Path: dir, Branch: "main"}); !reflect.DeepEqual(got, want) {
			t.Errorf("Inspect() = %+v, want %+v", got, want)
		}
	})

	t.Run("directory outside a repository", func(t *testing.T) {
		root := isolateGit(t)
		t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))

		if got, want := inspect(t, root), (repo.Info{Path: root}); !reflect.DeepEqual(got, want) {
			t.Errorf("Inspect() = %+v, want %+v", got, want)
		}
	})

	t.Run("two remotes and a push URL", func(t *testing.T) {
		root := isolateGit(t)
		main := newRepo(t, filepath.Join(root, "repo"))
		git(t, main, "remote", "add", "origin", "https://github.com/OctoCorp/Widget.git")
		git(t, main, "remote", "add", "upstream", "git@github.com:upstream/widget.git")
		git(t, main, "remote", "set-url", "--push", "origin", "ssh://git@github.com/fork/widget")

		want := []string{"github.com/octocorp/widget", "github.com/fork/widget", "github.com/upstream/widget"}
		if got := inspect(t, main).Remotes; !reflect.DeepEqual(got, want) {
			t.Errorf("Remotes = %q, want %q", got, want)
		}
	})

	t.Run("insteadOf is applied", func(t *testing.T) {
		root := isolateGit(t)
		git(t, root, "config", "--global", "url.https://github.com/.insteadOf", "gh:")
		main := newRepo(t, filepath.Join(root, "repo"))
		git(t, main, "remote", "add", "origin", "gh:octocorp/widget")

		want := []string{"github.com/octocorp/widget"}
		if got := inspect(t, main).Remotes; !reflect.DeepEqual(got, want) {
			t.Errorf("Remotes = %q, want %q", got, want)
		}
	})

	t.Run("GIT_CONFIG_GLOBAL of the caller is ignored", func(t *testing.T) {
		root := isolateGit(t)
		main := newRepo(t, filepath.Join(root, "repo"))
		git(t, main, "remote", "add", "origin", "https://github.com/octocorp/widget")
		other := filepath.Join(root, "other-gitconfig")
		if err := os.WriteFile(other, []byte("[url \"https://example.com/\"]\n\tinsteadOf = https://github.com/\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GIT_CONFIG_GLOBAL", other)

		want := []string{"github.com/octocorp/widget"}
		if got := inspect(t, main).Remotes; !reflect.DeepEqual(got, want) {
			t.Errorf("Remotes = %q, want %q", got, want)
		}
	})

	t.Run("path with a newline", func(t *testing.T) {
		root := isolateGit(t)
		main := newRepo(t, filepath.Join(root, "re\npo"))

		if got := inspect(t, main).Path; got != main {
			t.Errorf("Path = %q, want %q", got, main)
		}
	})

	t.Run("repository reached through a symlink", func(t *testing.T) {
		root := isolateGit(t)
		real := newRepo(t, filepath.Join(root, "real", "repo"))
		if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
			t.Fatal(err)
		}

		if got := inspect(t, filepath.Join(root, "link", "repo")).Path; got != real {
			t.Errorf("Path = %q, want %q", got, real)
		}
	})
}
