package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/paths"
)

// migrate converts the config.json of botid v0.1.0 to the current format and returns the new config.
// Concurrent botid processes wait on a lock of the old file, then find the converted file and use it.
func (a *App) migrate(ctx context.Context, p paths.Paths) (config.Config, error) {
	lock, err := os.Open(p.ConfigFile)
	if err != nil {
		return config.Config{}, migrateError(p, err)
	}
	defer func() { _ = lock.Close() }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return config.Config{}, migrateError(p, fmt.Errorf("cannot lock the file: %w", err))
	}

	c, err := config.Load(p.ConfigFile)
	if !errors.Is(err, config.ErrLegacy) {
		return c, err
	}
	c, err = a.convert(ctx, p)
	if err != nil {
		return config.Config{}, migrateError(p, err)
	}
	return c, nil
}

func migrateError(p paths.Paths, err error) error {
	return fmt.Errorf("cannot migrate %s from the format of botid v0.1.0: %w (to discard the old configuration instead, remove the file and run botid init)", p.ConfigFile, err)
}

// convert turns the v0.1.0 App into the identity named after its App slug and moves the v0.1.0 gh and git directories to it.
// The private key stays where config.json points. config.json is written last, so a failed run leaves the old file and the next run repeats the steps that are missing.
func (a *App) convert(ctx context.Context, p paths.Paths) (config.Config, error) {
	ident, err := config.LoadLegacy(p.ConfigFile)
	if err != nil {
		return config.Config{}, err
	}
	if ident.InstallationID == 0 {
		return config.Config{}, errors.New("installationId is not set")
	}
	key, err := ident.Key()
	if err != nil {
		return config.Config{}, err
	}
	cred, err := a.Client(ident.ClientID, key).Issue(ctx, ident.InstallationID)
	if err != nil {
		return config.Config{}, err
	}
	name := cred.Slug
	if err := config.ValidName(name); err != nil {
		return config.Config{}, fmt.Errorf("cannot use the App slug as an identity name: %w", err)
	}

	id := p.Identity(name)
	if err := ensureDirs(p.DataDir, filepath.Dir(id.DataDir), id.DataDir); err != nil {
		return config.Config{}, err
	}
	var moved [][2]string
	for _, m := range [][2]string{
		{filepath.Join(p.DataDir, "gh"), id.GHConfigDir},
		{filepath.Join(p.DataDir, "git"), id.GitConfigDir},
	} {
		did, err := moveDir(m[0], m[1])
		if err != nil {
			return config.Config{}, err
		}
		if did {
			moved = append(moved, m)
		}
	}
	if err := writeHosts(p, id, cred); err != nil {
		return config.Config{}, err
	}

	c := config.Config{Default: name, Identities: map[string]config.Identity{name: ident}}
	if err := config.Save(p.ConfigFile, c); err != nil {
		return config.Config{}, fmt.Errorf("cannot write %s: %w", p.ConfigFile, err)
	}

	_, _ = fmt.Fprintf(a.Stderr, "botid: migrated %s from the format of botid v0.1.0; the identity %q is the default\n", p.ConfigFile, name)
	for _, m := range moved {
		_, _ = fmt.Fprintf(a.Stderr, "botid: moved %s to %s\n", m[0], m[1])
	}
	return c, nil
}

// moveDir renames src to dst. It reports whether src existed. A src that is already gone is a step that an earlier run completed.
func moveDir(src, dst string) (bool, error) {
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if err := os.Rename(src, dst); err != nil {
		return false, err
	}
	return true, nil
}
