package cmd

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/repo"
	"github.com/pycabbage/botid/internal/store"
	"github.com/pycabbage/botid/internal/systemd"
)

type App struct {
	Home string
	// Cwd is the working directory of the botid process.
	Cwd    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Client func(clientID string, key *rsa.PrivateKey) *ghapp.Client
	// Exec runs the git and gh commands of init.
	Exec func(ctx context.Context, env []string, name string, args ...string) ([]byte, error)
	// Git runs the git commands of env.
	Git     repo.Runner
	Systemd systemd.Runner
	Exe     func() (string, error)
}

// ExecCommand runs the command with env appended to os.Environ() and returns the combined stdout and stderr output.
func ExecCommand(ctx context.Context, env []string, name string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, name, args...)
	c.Env = append(os.Environ(), env...)
	return c.CombinedOutput()
}

// loadConfig loads config.json and migrates a file of botid v0.1.0 first. A missing file gives the same advice for every command.
func (a *App) loadConfig(ctx context.Context, p paths.Paths) (config.Config, error) {
	c, err := config.Load(p.ConfigFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return config.Config{}, fmt.Errorf("%s does not exist; run botid init", p.ConfigFile)
	case errors.Is(err, config.ErrLegacy):
		return a.migrate(ctx, p)
	}
	return c, err
}

func ensureDirs(dirs ...string) error {
	for _, dir := range dirs {
		if err := store.EnsureDir(dir); err != nil {
			return fmt.Errorf("cannot create %s: %w", dir, err)
		}
	}
	return nil
}

func (a *App) runTool(ctx context.Context, env []string, name string, args ...string) error {
	out, err := a.Exec(ctx, env, name, args...)
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s not found; install it and add it to PATH: %w", name, err)
	}
	cmdline := strings.Join(append([]string{name}, args...), " ")
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%s failed: %w\n%s", cmdline, err, msg)
	}
	return fmt.Errorf("%s failed: %w", cmdline, err)
}
