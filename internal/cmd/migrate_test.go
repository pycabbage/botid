package cmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/pycabbage/botid/internal/cmd"
	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
)

const (
	legacyKeyPath   = ".config/botid/private-key.pem"
	legacyGH        = ".local/share/botid/gh"
	legacyGit       = ".local/share/botid/git"
	legacyHosts     = "legacy hosts"
	legacyGitConfig = "legacy git config"
)

func legacyBody(keyPath string, installationID int64) string {
	return fmt.Sprintf(`{"clientId":%q,"privateKeyPath":%q,"installationId":%d}`+"\n", testClientID, keyPath, installationID)
}

// writeMigratableConfig writes the files that botid v0.1.0 leaves behind after botid init.
func (h *harness) writeMigratableConfig() {
	h.t.Helper()
	h.write(legacyKeyPath, pkcs1PEM(h.t))
	h.write(configPath, legacyBody(h.path(legacyKeyPath), 42))
	h.write(legacyGH+"/hosts.yml", legacyHosts)
	h.write(legacyGit+"/config", legacyGitConfig)
}

func (h *harness) migratedConfig() config.Config {
	return config.Config{
		Default: "my-app",
		Identities: map[string]config.Identity{
			"my-app": {ClientID: testClientID, PrivateKeyPath: h.path(legacyKeyPath), InstallationID: 42},
		},
	}
}

func (h *harness) migratedLine() string {
	return "botid: migrated " + h.path(configPath) + " from the format of botid v0.1.0; the identity \"my-app\" is the default\n"
}

func (h *harness) movedLine(src, dst string) string {
	return "botid: moved " + h.path(src) + " to " + h.path(dst) + "\n"
}

func (h *harness) migrationNotice() string {
	return h.migratedLine() + h.movedLine(legacyGH, identityData+"/gh") + h.movedLine(legacyGit, identityData+"/git")
}

func (h *harness) useWorkDir(g *fakeGit) {
	h.t.Helper()
	work := h.path("work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		h.t.Fatal(err)
	}
	h.app.Cwd = work
	h.app.Git = g.run
}

func (h *harness) assertGone(rel string) {
	h.t.Helper()
	if _, err := os.Lstat(h.path(rel)); !errors.Is(err, fs.ErrNotExist) {
		h.t.Errorf("%s still exists (err = %v)", rel, err)
	}
}

func TestEnvMigratesAConfigOfBotidV010(t *testing.T) {
	h := newHarness(t, "", alice)
	h.writeMigratableConfig()
	h.useWorkDir(newFakeGit("/src/bar", "main"))

	if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
		t.Fatalf("Env: %v", err)
	}

	if got := h.stdout.String(); got != h.dotEnvOf("my-app") {
		t.Errorf("stdout = %q, want %q", got, h.dotEnvOf("my-app"))
	}
	if got := h.loadSavedConfig(); !reflect.DeepEqual(got, h.migratedConfig()) {
		t.Errorf("config.json = %+v, want %+v", got, h.migratedConfig())
	}
	if m := h.mode(configPath); m != 0o600 {
		t.Errorf("config.json mode = %o, want 600", m)
	}
	if got := h.read(legacyKeyPath); got != pkcs1PEM(t) {
		t.Errorf("the private key was changed: %q", got)
	}
	if got := h.read(identityData + "/git/config"); got != legacyGitConfig {
		t.Errorf("the moved git config = %q, want %q", got, legacyGitConfig)
	}
	h.assertGone(legacyGH)
	h.assertGone(legacyGit)
	if got := h.entries(".local/share/botid"); !equalLines(got, []string{"identities"}) {
		t.Errorf(".local/share/botid contents = %v, want [identities]", got)
	}
	if got := h.read(identityData + "/gh/hosts.yml"); got != wantHosts {
		t.Errorf("hosts.yml = %q, want the one issued during the migration: %q", got, wantHosts)
	}
	if m := h.mode(identityData + "/gh/hosts.yml"); m != 0o600 {
		t.Errorf("hosts.yml mode = %o, want 600", m)
	}
	if got := h.stderr.String(); got != h.migrationNotice() {
		t.Errorf("stderr = %q, want %q", got, h.migrationNotice())
	}
	wantRequests := []string{"POST /app/installations/42/access_tokens", "GET /app"}
	if got := h.gh.requests(); !equalLines(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
	h.assertNoOutputLeaksToken()

	h.stdout.Reset()
	h.stderr.Reset()
	if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
		t.Fatalf("second Env: %v", err)
	}
	if got := h.stdout.String(); got != h.dotEnvOf("my-app") {
		t.Errorf("second stdout = %q, want %q", got, h.dotEnvOf("my-app"))
	}
	if got := h.stderr.String(); got != "" {
		t.Errorf("second stderr = %q, want it empty", got)
	}
	if got := h.gh.requests(); !equalLines(got, wantRequests) {
		t.Errorf("the second run sent requests: %v", got)
	}
}

func TestMigrationKeepsAnInlinePrivateKey(t *testing.T) {
	h := newHarness(t, "", alice)
	h.write(configPath, fmt.Sprintf(`{"clientId":%q,"privateKey":%q,"installationId":42}`+"\n", testClientID, pkcs1PEM(t)))
	h.useWorkDir(newFakeGit("/src/bar", "main"))

	if err := h.app.Env(context.Background(), "", cmd.FormatJSON); err != nil {
		t.Fatalf("Env: %v", err)
	}

	want := config.Config{
		Default: "my-app",
		Identities: map[string]config.Identity{
			"my-app": {ClientID: testClientID, PrivateKey: pkcs1PEM(t), InstallationID: 42},
		},
	}
	if got := h.loadSavedConfig(); !reflect.DeepEqual(got, want) {
		t.Errorf("config.json = %+v, want %+v", got, want)
	}
	if got := h.stdout.String(); got != h.jsonEnvOf("my-app") {
		t.Errorf("stdout = %q, want %q", got, h.jsonEnvOf("my-app"))
	}
}

func TestMigrationWithoutTheOldGhAndGitDirectories(t *testing.T) {
	h := newHarness(t, "", alice)
	h.write(legacyKeyPath, pkcs1PEM(t))
	h.write(configPath, legacyBody(h.path(legacyKeyPath), 42))
	h.useWorkDir(newFakeGit("/src/bar", "main"))

	if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
		t.Fatalf("Env: %v", err)
	}

	if got := h.read(identityData + "/gh/hosts.yml"); got != wantHosts {
		t.Errorf("hosts.yml = %q, want %q", got, wantHosts)
	}
	h.assertGone(identityData + "/git")
	if got := h.stderr.String(); got != h.migratedLine() {
		t.Errorf("stderr = %q, want %q", got, h.migratedLine())
	}
}

func TestMigrationFailureLeavesTheOldFilesAsTheyWere(t *testing.T) {
	tests := []struct {
		name  string
		setup func(h *harness)
		want  string
	}{
		{
			"installationId unset",
			func(h *harness) { h.write(configPath, legacyBody(h.path(legacyKeyPath), 0)) },
			"installationId is not set",
		},
		{
			"private key file is missing",
			func(h *harness) {
				if err := os.Remove(h.path(legacyKeyPath)); err != nil {
					h.t.Fatal(err)
				}
			},
			"cannot read private key",
		},
		{
			"GitHub rejects the credentials",
			func(h *harness) { h.gh.fail["POST /app/installations/42/access_tokens"] = 401 },
			"authentication failed (401)",
		},
		{
			"the App slug cannot be an identity name",
			func(h *harness) { h.gh.slug = config.User },
			`identity name "user" is reserved`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
			h.writeMigratableConfig()
			tt.setup(h)
			h.useWorkDir(newFakeGit("/src/bar", "main"))
			before := h.read(configPath)

			err := h.app.Env(context.Background(), "", cmd.FormatDotEnv)

			prefix := "cannot migrate " + h.path(configPath) + " from the format of botid v0.1.0: "
			if err == nil || !strings.HasPrefix(err.Error(), prefix) || !strings.Contains(err.Error(), tt.want) || !strings.HasSuffix(err.Error(), "(to discard the old configuration instead, remove the file and run botid init)") {
				t.Fatalf("error = %v, want prefix %q, substring %q and the advice to discard the file", err, prefix, tt.want)
			}
			if got := h.read(configPath); got != before {
				t.Errorf("config.json was changed: %q", got)
			}
			if got := h.read(legacyGH + "/hosts.yml"); got != legacyHosts {
				t.Errorf("the old hosts.yml was changed: %q", got)
			}
			if got := h.read(legacyGit + "/config"); got != legacyGitConfig {
				t.Errorf("the old git config was changed: %q", got)
			}
			if got := h.entries(".local/share/botid"); !equalLines(got, []string{"gh", "git"}) {
				t.Errorf(".local/share/botid contents = %v, want [gh git]", got)
			}
			if h.stdout.Len() != 0 || h.stderr.Len() != 0 {
				t.Errorf("stdout = %q, stderr = %q, want both empty", h.stdout.String(), h.stderr.String())
			}
		})
	}
}

func TestMigrationResumesAfterAnInterruptedRun(t *testing.T) {
	h := newHarness(t, "", alice)
	h.writeMigratableConfig()
	h.useWorkDir(newFakeGit("/src/bar", "main"))
	before := h.read(configPath)
	h.write(identityData+"/git/blocker", "x")

	err := h.app.Env(context.Background(), "", cmd.FormatDotEnv)

	wantRename := "rename " + h.path(legacyGit) + " " + h.path(identityData+"/git")
	if err == nil || !strings.Contains(err.Error(), wantRename) {
		t.Fatalf("error = %v, want substring %q", err, wantRename)
	}
	if got := h.read(configPath); got != before {
		t.Errorf("config.json was changed: %q", got)
	}
	if got := h.read(identityData + "/gh/hosts.yml"); got != legacyHosts {
		t.Errorf("the moved hosts.yml = %q, want %q", got, legacyHosts)
	}
	h.assertGone(legacyGH)

	if err := os.RemoveAll(h.path(identityData + "/git")); err != nil {
		t.Fatal(err)
	}
	h.stdout.Reset()
	h.stderr.Reset()

	if err := h.app.Env(context.Background(), "", cmd.FormatDotEnv); err != nil {
		t.Fatalf("second Env: %v", err)
	}

	if got := h.loadSavedConfig(); !reflect.DeepEqual(got, h.migratedConfig()) {
		t.Errorf("config.json = %+v, want %+v", got, h.migratedConfig())
	}
	if got := h.read(identityData + "/git/config"); got != legacyGitConfig {
		t.Errorf("the moved git config = %q, want %q", got, legacyGitConfig)
	}
	if got := h.read(identityData + "/gh/hosts.yml"); got != wantHosts {
		t.Errorf("hosts.yml = %q, want %q", got, wantHosts)
	}
	wantNotice := h.migratedLine() + h.movedLine(legacyGit, identityData+"/git")
	if got := h.stderr.String(); got != wantNotice {
		t.Errorf("stderr = %q, want %q", got, wantNotice)
	}
}

func TestMigrationRunsOnceWhenCommandsStartAtTheSameTime(t *testing.T) {
	const commands = 8
	h := newHarness(t, "", alice)
	h.writeMigratableConfig()
	h.useWorkDir(newFakeGit("/src/bar", "main"))

	var wg sync.WaitGroup
	errs := make([]error, commands)
	stdouts := make([]*bytes.Buffer, commands)
	stderrs := make([]*bytes.Buffer, commands)
	for i := range commands {
		app := *h.app
		stdouts[i], stderrs[i] = &bytes.Buffer{}, &bytes.Buffer{}
		app.Stdout, app.Stderr = stdouts[i], stderrs[i]
		wg.Go(func() { errs[i] = app.Env(context.Background(), "", cmd.FormatDotEnv) })
	}
	wg.Wait()

	notices := 0
	for i := range commands {
		if errs[i] != nil {
			t.Errorf("command %d: %v", i, errs[i])
		}
		if got := stdouts[i].String(); got != h.dotEnvOf("my-app") {
			t.Errorf("command %d: stdout = %q, want %q", i, got, h.dotEnvOf("my-app"))
		}
		if strings.Contains(stderrs[i].String(), "botid: migrated") {
			notices++
		}
	}
	if notices != 1 {
		t.Errorf("%d commands migrated the file, want 1", notices)
	}
	wantRequests := []string{"POST /app/installations/42/access_tokens", "GET /app"}
	if got := h.gh.requests(); !equalLines(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
	if got := h.loadSavedConfig(); !reflect.DeepEqual(got, h.migratedConfig()) {
		t.Errorf("config.json = %+v, want %+v", got, h.migratedConfig())
	}
}
