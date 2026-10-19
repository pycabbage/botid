package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/store"
)

func (a *App) Issue(ctx context.Context) error {
	p := paths.New(a.Home)

	c, err := config.Load(p.ConfigFile)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%s does not exist; run botid init", p.ConfigFile)
	}
	if err != nil {
		return err
	}
	if c.InstallationID == 0 {
		return fmt.Errorf("%s has no installationId; run botid init again", p.ConfigFile)
	}

	key, err := c.Key()
	if err != nil {
		return err
	}
	cred, err := a.Client(c.ClientID, key).Issue(ctx, c.InstallationID)
	if err != nil {
		return err
	}
	hosts, err := store.HostsYAML(cred.Slug, cred.Token)
	if err != nil {
		return fmt.Errorf("cannot generate hosts.yml: %w", err)
	}

	if err := store.EnsureDir(p.DataDir); err != nil {
		return fmt.Errorf("cannot create %s: %w", p.DataDir, err)
	}
	if err := store.EnsureDir(p.GHConfigDir); err != nil {
		return fmt.Errorf("cannot create %s: %w", p.GHConfigDir, err)
	}
	if err := store.WriteFileAtomic(p.HostsFile, hosts, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", p.HostsFile, err)
	}
	return nil
}
