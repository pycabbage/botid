package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/repo"
	"github.com/pycabbage/botid/internal/store"
)

const (
	FormatDotEnv = "dotenv"
	FormatJSON   = "json"
)

// Env prints the variables of the identity selected for cwd. An empty cwd means the working directory of the process.
func (a *App) Env(ctx context.Context, cwd, format string) error {
	if format != FormatDotEnv && format != FormatJSON {
		return fmt.Errorf("unknown format %q; use %s or %s", format, FormatDotEnv, FormatJSON)
	}
	p := paths.New(a.Home)
	c, err := a.loadConfig(ctx, p)
	if err != nil {
		return err
	}
	info, err := repo.Inspect(ctx, a.Git, a.resolveCwd(cwd))
	if err != nil {
		return err
	}

	name := c.Select(info, a.Home)
	if name != config.User {
		if err := a.ensureIdentity(ctx, p, c, name); err != nil {
			return err
		}
	}
	out, err := envOutput(p, name, format)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(a.Stdout, out); err != nil {
		return fmt.Errorf("cannot write to standard output: %w", err)
	}
	return nil
}

// resolveCwd expands "~" and "~/x" and joins a relative path to the working directory of the process.
func (a *App) resolveCwd(cwd string) string {
	if rest, ok := strings.CutPrefix(cwd, "~/"); ok {
		return filepath.Join(a.Home, rest)
	}
	switch {
	case cwd == "":
		return a.Cwd
	case cwd == "~":
		return a.Home
	case filepath.IsAbs(cwd):
		return filepath.Clean(cwd)
	}
	return filepath.Join(a.Cwd, cwd)
}

func envOutput(p paths.Paths, name, format string) (string, error) {
	if name == config.User {
		if format == FormatJSON {
			return "{}\n", nil
		}
		return "", nil
	}
	id := p.Identity(name)
	if format == FormatJSON {
		return store.JSONEnv(id.GHConfigDir, id.GitConfigFile)
	}
	return store.DotEnv(id.GHConfigDir, id.GitConfigFile)
}
