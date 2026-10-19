package cmd

import (
	"fmt"
	"io"

	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/store"
)

func (a *App) Env() error {
	p := paths.New(a.Home)
	out, err := store.DotEnv(p.GHConfigDir, p.GitConfigFile)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(a.Stdout, out); err != nil {
		return fmt.Errorf("cannot write to standard output: %w", err)
	}
	_, _ = fmt.Fprintln(a.Stderr, "Set the environment variables above once in the coding agent's environment. Their values are fixed and never change.")
	return nil
}
