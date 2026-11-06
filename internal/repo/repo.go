package repo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

// Info describes the repository that contains a directory.
type Info struct {
	// Path is the path of the main worktree, or of the repository directory for a bare repository.
	// It is empty when the directory is not in a repository.
	Path string
	// Branch is the checked-out branch. It is empty when HEAD is detached or not under refs/heads/.
	Branch string
	// Remotes holds the normalized, lowercase, de-duplicated URLs of all remotes.
	Remotes []string
}

// ExitError is the error of a git process that exited with a non-zero status.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("git exited with status %d: %s", e.Code, strings.TrimSpace(e.Stderr))
}

// Runner runs git in dir and returns its stdout. A non-zero exit status is reported as an *ExitError.
type Runner func(ctx context.Context, dir string, args ...string) ([]byte, error)

// ExecGit runs "git -C dir args..." with LC_ALL=C, so that messages do not depend on the locale,
// and without GIT_CONFIG_GLOBAL, so that the result does not depend on the identity selected earlier.
func ExecGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=")
	}), "LC_ALL=C")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil, &ExitError{Code: exitErr.ExitCode(), Stderr: stderr.String()}
		}
		return nil, fmt.Errorf("cannot run git: %w", err)
	}
	return stdout.Bytes(), nil
}

// Inspect reads the repository facts of cwd with run. A directory outside any repository
// gives an empty Info and no error. Every other failure is an error.
func Inspect(ctx context.Context, run Runner, cwd string) (Info, error) {
	fi, err := os.Stat(cwd)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Info{}, fmt.Errorf("cwd does not exist: %s", cwd)
		}
		return Info{}, fmt.Errorf("cannot read cwd: %w", err)
	}
	if !fi.IsDir() {
		return Info{}, fmt.Errorf("cwd is not a directory: %s", cwd)
	}

	out, err := run(ctx, cwd, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		if exitErr, ok := errors.AsType[*ExitError](err); ok && exitErr.Code == 128 && strings.Contains(exitErr.Stderr, "not a git repository") {
			return Info{}, nil
		}
		return Info{}, fmt.Errorf("git worktree list: %w", err)
	}
	first, _, _ := strings.Cut(string(out), "\x00")
	path, ok := strings.CutPrefix(first, "worktree ")
	if !ok || path == "" {
		return Info{}, fmt.Errorf("git worktree list: unexpected output %q", out)
	}

	branch, err := currentBranch(ctx, run, cwd)
	if err != nil {
		return Info{}, err
	}
	remotes, err := listRemotes(ctx, run, cwd)
	if err != nil {
		return Info{}, err
	}
	return Info{Path: path, Branch: branch, Remotes: remotes}, nil
}

func currentBranch(ctx context.Context, run Runner, cwd string) (string, error) {
	out, err := run(ctx, cwd, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		if exitErr, ok := errors.AsType[*ExitError](err); ok && exitErr.Code == 1 {
			return "", nil
		}
		return "", fmt.Errorf("git symbolic-ref: %w", err)
	}
	branch, _ := strings.CutPrefix(strings.TrimSuffix(string(out), "\n"), "refs/heads/")
	if strings.HasPrefix(branch, "refs/") {
		return "", nil
	}
	return branch, nil
}

var remoteLine = regexp.MustCompile(`^(\S+)\t(.+) \((?:fetch|push)\)(?: \[.*\])?$`)

func listRemotes(ctx context.Context, run Runner, cwd string) ([]string, error) {
	out, err := run(ctx, cwd, "remote", "-v")
	if err != nil {
		return nil, fmt.Errorf("git remote: %w", err)
	}
	var remotes []string
	for line := range strings.SplitSeq(strings.TrimSuffix(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		m := remoteLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("git remote: unexpected line %q", line)
		}
		normalized, err := Normalize(m[2])
		if err != nil {
			return nil, fmt.Errorf("remote %q: %w", m[1], err)
		}
		if !slices.Contains(remotes, normalized) {
			remotes = append(remotes, normalized)
		}
	}
	return remotes, nil
}

// Normalize turns a remote URL into a lowercase "host/path" string without a trailing ".git".
func Normalize(remoteURL string) (string, error) {
	if remoteURL == "" {
		return "", errors.New("remote URL is empty")
	}
	var normalized string
	switch {
	case strings.Contains(remoteURL, "://"):
		u, err := url.Parse(remoteURL)
		if err != nil {
			return "", fmt.Errorf("cannot parse remote URL: %w", err)
		}
		normalized = u.Path
		if host := u.Hostname(); host != "" {
			normalized = host + "/" + strings.TrimPrefix(u.Path, "/")
		}
	case isSCPLike(remoteURL):
		hostPart, p, _ := strings.Cut(remoteURL, ":")
		host := hostPart[strings.LastIndex(hostPart, "@")+1:]
		if host == "" {
			return "", fmt.Errorf("remote URL %q has no host", remoteURL)
		}
		normalized = host + "/" + strings.TrimPrefix(p, "/")
	default:
		normalized = remoteURL
	}
	normalized = strings.TrimSuffix(strings.TrimRight(normalized, "/"), ".git")
	return strings.ToLower(normalized), nil
}

func isSCPLike(s string) bool {
	colon := strings.Index(s, ":")
	slash := strings.Index(s, "/")
	return colon >= 0 && (slash < 0 || colon < slash)
}
