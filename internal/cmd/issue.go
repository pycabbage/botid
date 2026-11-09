package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
	"github.com/pycabbage/botid/internal/paths"
	"github.com/pycabbage/botid/internal/store"
)

// Issue replaces the hosts.yml of every App identity and adds the git settings that its git config file lacks.
// A failure for one identity does not stop the others.
func (a *App) Issue(ctx context.Context) error {
	p := paths.New(a.Home)
	c, err := a.loadConfig(ctx, p)
	if err != nil {
		return err
	}

	var errs []error
	for _, name := range c.AppNames() {
		if err := a.issueIdentity(ctx, p, name, c.Identities[name]); err != nil {
			errs = append(errs, fmt.Errorf("identity %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (a *App) issueIdentity(ctx context.Context, p paths.Paths, name string, ident config.Identity) error {
	client, cred, err := a.issueCredential(ctx, ident)
	if err != nil {
		return err
	}
	id := p.Identity(name)
	var added []string
	err = withLock(p, func() error {
		var err error
		added, err = a.completeIdentity(ctx, p, id, client, cred)
		return err
	})
	if err != nil {
		return err
	}
	if len(added) > 0 {
		a.notef("identity %q: added %s to %s", name, strings.Join(added, ", "), id.GitConfigFile)
	}
	return nil
}

// writeHosts replaces the hosts.yml of one identity with the credential.
func writeHosts(p paths.Paths, id paths.Identity, cred ghapp.Credential) error {
	hosts, err := store.HostsYAML(cred.Slug, cred.Token)
	if err != nil {
		return fmt.Errorf("cannot generate hosts.yml: %w", err)
	}
	if err := ensureDirs(p.DataDir, filepath.Dir(id.DataDir), id.DataDir, id.GHConfigDir); err != nil {
		return err
	}
	if err := store.WriteFileAtomic(id.HostsFile, hosts, 0o600); err != nil {
		return fmt.Errorf("cannot write %s: %w", id.HostsFile, err)
	}
	return nil
}
