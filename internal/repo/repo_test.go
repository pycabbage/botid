package repo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/OctoCorp/Widget.git", "github.com/octocorp/widget"},
		{"git@github.com:octocorp/widget.git", "github.com/octocorp/widget"},
		{"ssh://git@github.com:22/octocorp/widget", "github.com/octocorp/widget"},
		{"https://user:pw@github.com/o/r/", "github.com/o/r"},
		{"https://github.com:8443/o/r.git", "github.com/o/r"},
		{"git://example.com/o/r.git", "example.com/o/r"},
		{"github.com:o/r", "github.com/o/r"},
		{"git@host:/abs/path.git", "host/abs/path"},
		{"https://github.com", "github.com"},
		{"file:///srv/Repo.git", "/srv/repo"},
		{"/srv/Repo.git", "/srv/repo"},
		{"../relative/repo", "../relative/repo"},
		{"./a:b", "./a:b"},
	}
	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got, err := Normalize(tt.url)
			if err != nil {
				t.Fatalf("Normalize(%q) error = %v", tt.url, err)
			}
			if got != tt.want {
				t.Errorf("Normalize(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestNormalizeErrors(t *testing.T) {
	for _, url := range []string{"", "http://[::1", ":path", "git@:path"} {
		t.Run(url, func(t *testing.T) {
			got, err := Normalize(url)
			if err == nil {
				t.Errorf("Normalize(%q) = %q, want error", url, got)
			}
		})
	}
}

type reply struct {
	out string
	err error
}

type fakeGit struct {
	replies map[string]reply
	calls   []string
}

func (f *fakeGit) run(_ context.Context, dir string, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	f.calls = append(f.calls, dir+": "+key)
	r, ok := f.replies[key]
	if !ok {
		return nil, errors.New("unexpected git call: " + key)
	}
	return []byte(r.out), r.err
}

const (
	worktreeList = "worktree list --porcelain -z"
	symbolicRef  = "symbolic-ref -q HEAD"
	remoteList   = "remote -v"
)

func TestInspect(t *testing.T) {
	dir := t.TempDir()
	f := &fakeGit{replies: map[string]reply{
		worktreeList: {out: "worktree /src/repo\x00HEAD aaa\x00branch refs/heads/main\x00\x00worktree /src/repo-wt\x00HEAD bbb\x00branch refs/heads/feature/x\x00\x00"},
		symbolicRef:  {out: "refs/heads/feature/x\n"},
		remoteList: {out: "origin\thttps://github.com/OctoCorp/Widget.git (fetch)\n" +
			"origin\thttps://github.com/OctoCorp/Widget.git (push)\n" +
			"upstream\tgit@github.com:upstream/widget.git (fetch) [blob:none]\n" +
			"upstream\tgit@github.com:upstream/widget.git (push)\n"},
	}}

	got, err := Inspect(t.Context(), f.run, dir)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	want := Info{
		Path:    "/src/repo",
		Branch:  "feature/x",
		Remotes: []string{"github.com/octocorp/widget", "github.com/upstream/widget"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Inspect() = %+v, want %+v", got, want)
	}
	for _, call := range f.calls {
		if !strings.HasPrefix(call, dir+": ") {
			t.Errorf("git ran in the wrong directory: %q, want prefix %q", call, dir)
		}
	}
}

func TestInspectNoRemotes(t *testing.T) {
	f := &fakeGit{replies: map[string]reply{
		worktreeList: {out: "worktree /src/repo\x00HEAD aaa\x00branch refs/heads/main\x00\x00"},
		symbolicRef:  {out: "refs/heads/main\n"},
		remoteList:   {out: ""},
	}}
	got, err := Inspect(t.Context(), f.run, t.TempDir())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	want := Info{Path: "/src/repo", Branch: "main"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Inspect() = %+v, want %+v", got, want)
	}
}

func TestInspectOutsideRepository(t *testing.T) {
	f := &fakeGit{replies: map[string]reply{
		worktreeList: {err: &ExitError{Code: 128, Stderr: "fatal: not a git repository (or any of the parent directories): .git\n"}},
	}}
	dir := t.TempDir()
	got, err := Inspect(t.Context(), f.run, dir)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if want := (Info{Path: dir}); !reflect.DeepEqual(got, want) {
		t.Errorf("Inspect() = %+v, want %+v", got, want)
	}
	if len(f.calls) != 1 {
		t.Errorf("git ran %d times, want 1: %v", len(f.calls), f.calls)
	}
}

func TestInspectNoBranch(t *testing.T) {
	for name, r := range map[string]reply{
		"detached HEAD":       {err: &ExitError{Code: 1}},
		"ref outside heads":   {out: "refs/remotes/origin/main\n"},
		"ref of another kind": {out: "refs/tags/v1\n"},
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeGit{replies: map[string]reply{
				worktreeList: {out: "worktree /src/repo\x00HEAD aaa\x00detached\x00\x00"},
				symbolicRef:  r,
				remoteList:   {out: ""},
			}}
			got, err := Inspect(t.Context(), f.run, t.TempDir())
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}
			if got.Branch != "" || got.Path != "/src/repo" {
				t.Errorf("Inspect() = %+v, want Path /src/repo and no branch", got)
			}
		})
	}
}

func TestInspectErrors(t *testing.T) {
	worktreeOK := reply{out: "worktree /src/repo\x00HEAD aaa\x00branch refs/heads/main\x00\x00"}
	tests := []struct {
		name    string
		replies map[string]reply
		want    string
	}{
		{
			name: "git failure other than not a repository",
			replies: map[string]reply{
				worktreeList: {err: &ExitError{Code: 128, Stderr: "fatal: detected dubious ownership in repository at '/src/repo'\n"}},
			},
			want: "dubious ownership",
		},
		{
			name: "usage error of an old git is not a missing repository",
			replies: map[string]reply{
				worktreeList: {err: &ExitError{Code: 129, Stderr: "error: unknown switch `z'\n"}},
			},
			want: "unknown switch",
		},
		{
			name: "git cannot run",
			replies: map[string]reply{
				worktreeList: {err: errors.New("cannot run git: not found")},
			},
			want: "cannot run git",
		},
		{
			name: "unexpected worktree output",
			replies: map[string]reply{
				worktreeList: {out: "something else"},
			},
			want: "unexpected output",
		},
		{
			name: "symbolic-ref fails",
			replies: map[string]reply{
				worktreeList: worktreeOK,
				symbolicRef:  {err: &ExitError{Code: 128, Stderr: "fatal: corrupt\n"}},
			},
			want: "corrupt",
		},
		{
			name: "remote fails",
			replies: map[string]reply{
				worktreeList: worktreeOK,
				symbolicRef:  {out: "refs/heads/main\n"},
				remoteList:   {err: &ExitError{Code: 128, Stderr: "fatal: bad config\n"}},
			},
			want: "bad config",
		},
		{
			name: "unparsable remote line",
			replies: map[string]reply{
				worktreeList: worktreeOK,
				symbolicRef:  {out: "refs/heads/main\n"},
				remoteList:   {out: "origin https://github.com/o/r (fetch)\n"},
			},
			want: "unexpected line",
		},
		{
			name: "unparsable remote URL names the remote",
			replies: map[string]reply{
				worktreeList: worktreeOK,
				symbolicRef:  {out: "refs/heads/main\n"},
				remoteList:   {out: "origin\thttp://[::1 (fetch)\n"},
			},
			want: `remote "origin"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeGit{replies: tt.replies}
			got, err := Inspect(t.Context(), f.run, t.TempDir())
			if err == nil {
				t.Fatalf("Inspect() = %+v, want error containing %q", got, tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Inspect() error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestInspectRejectsBadCwd(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		filepath.Join(dir, "missing"): "cwd does not exist",
		file:                          "cwd is not a directory",
	}
	for cwd, want := range tests {
		f := &fakeGit{}
		_, err := Inspect(t.Context(), f.run, cwd)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Inspect(%q) error = %v, want it to contain %q", cwd, err, want)
		}
		if len(f.calls) != 0 {
			t.Errorf("Inspect(%q) ran git: %v", cwd, f.calls)
		}
	}
}
