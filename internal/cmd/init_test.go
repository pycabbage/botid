package cmd_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
)

var (
	alice = ghapp.Installation{ID: 42, Login: "alice"}
	acme  = ghapp.Installation{ID: 77, Login: "acme-org"}
)

const (
	configPath   = ".config/botid/config.json"
	identityKey  = ".config/botid/identities/my-app/private-key.pem"
	identityData = ".local/share/botid/identities/my-app"
	legacyConfig = "{\"clientId\":\"old-client\",\"privateKeyPath\":\"/old/key.pem\",\"installationId\":1}\n"
)

func requireSystemd(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Skipf("init checks /run/systemd/system before it reads any input: %v", err)
	}
}

func (h *harness) writeLegacyConfig() {
	h.t.Helper()
	h.write(configPath, legacyConfig)
	h.write(".config/botid/private-key.pem", "old key")
	h.write(".local/share/botid/gh/hosts.yml", "old hosts")
	h.write(".local/share/botid/git/config", "old git config")
}

func (h *harness) assertLegacyFilesKept() {
	h.t.Helper()
	if got := h.read(".config/botid/private-key.pem"); got != "old key" {
		h.t.Errorf("the old private-key.pem was changed: %q", got)
	}
	if got := h.read(".local/share/botid/gh/hosts.yml"); got != "old hosts" {
		h.t.Errorf("the old hosts.yml was changed: %q", got)
	}
	if got := h.read(".local/share/botid/git/config"); got != "old git config" {
		h.t.Errorf("the old git config was changed: %q", got)
	}
}

func (h *harness) assertLegacyIntact() {
	h.t.Helper()
	if got := h.read(configPath); got != legacyConfig {
		h.t.Errorf("config.json was changed: %q", got)
	}
	h.assertLegacyFilesKept()
	if got := h.entries(".config/botid"); !equalLines(got, []string{"config.json", "private-key.pem"}) {
		h.t.Errorf(".config/botid contents = %v", got)
	}
	if got := h.entries(".local/share/botid"); !equalLines(got, []string{"gh", "git"}) {
		h.t.Errorf(".local/share/botid contents = %v, want [gh git]", got)
	}
	if got := h.entries(".config"); !equalLines(got, []string{"botid"}) {
		h.t.Errorf(".config contents = %v, want [botid]", got)
	}
	if got := h.rec.lines(); len(got) != 0 {
		h.t.Errorf("external commands were run: %v", got)
	}
	if got := h.gh.requests(); len(got) != 0 {
		h.t.Errorf("requests were sent to GitHub: %v", got)
	}
}

func existingConfig() config.Config {
	return config.Config{
		Default: "my-app",
		Identities: map[string]config.Identity{
			"my-app": {ClientID: "old-client", PrivateKeyPath: "/old/my-app.pem", InstallationID: 1},
			"other":  {ClientID: "other-client", PrivateKeyPath: "/old/other.pem", InstallationID: 2},
		},
		Rules: []config.Rule{
			{Remote: "github.com/octocorp/*", Identity: "other"},
			{Path: "~/src/private/**", Branch: "release/**", Identity: "user"},
		},
	}
}

func (h *harness) writeExistingIdentities() (before string) {
	h.t.Helper()
	h.saveConfig(existingConfig())
	h.write(identityKey, "old key")
	h.write(identityData+"/gh/hosts.yml", "old hosts")
	return h.read(configPath)
}

func (h *harness) assertExistingIdentitiesIntact(before string) {
	h.t.Helper()
	if got := h.read(configPath); got != before {
		h.t.Errorf("config.json was changed:\n%s\nwant\n%s", got, before)
	}
	if got := h.read(identityKey); got != "old key" {
		h.t.Errorf("private-key.pem was changed: %q", got)
	}
	if got := h.read(identityData + "/gh/hosts.yml"); got != "old hosts" {
		h.t.Errorf("hosts.yml was changed: %q", got)
	}
	if got := h.entries(".config/botid/identities"); !equalLines(got, []string{"my-app"}) {
		h.t.Errorf("identity directories = %v, want [my-app]", got)
	}
	if got := h.rec.lines(); len(got) != 0 {
		h.t.Errorf("external commands were run: %v", got)
	}
}

func (h *harness) loadSavedConfig() config.Config {
	h.t.Helper()
	c, err := config.Load(h.path(configPath))
	if err != nil {
		h.t.Fatalf("config.json cannot be loaded: %v", err)
	}
	return c
}

// assertInitRejectedBeforeAnySideEffect checks that Init failed with an error containing want
// without reading stdin, printing, calling GitHub, running any external command, or building a client.
func (h *harness) assertInitRejectedBeforeAnySideEffect(err error, want string, stdin *strings.Reader, stdinLen int) {
	h.t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		h.t.Errorf("error = %v, want substring %q", err, want)
	}
	if err != nil && strings.Contains(err.Error(), "saved") {
		h.t.Errorf("error mentions saved data although nothing was saved: %q", err)
	}
	if got := h.gh.requests(); len(got) != 0 {
		h.t.Errorf("requests were sent to GitHub: %v", got)
	}
	if got := h.rec.lines(); len(got) != 0 {
		h.t.Errorf("external commands were run (Exec or Systemd): %v", got)
	}
	if len(h.clientIDs) != 0 {
		h.t.Errorf("a GitHub client was created: %v", h.clientIDs)
	}
	if got := h.stdout.String(); got != "" {
		h.t.Errorf("stdout = %q, want it empty", got)
	}
	if got := h.stderr.String(); got != "" {
		h.t.Errorf("stderr = %q, want it empty", got)
	}
	if stdin.Len() != stdinLen {
		h.t.Errorf("stdin was consumed: %d of %d bytes remain", stdin.Len(), stdinLen)
	}
}

func TestInitRejectsGoRunBinaryBeforeAnySideEffect(t *testing.T) {
	input := testClientID + "\n" + pkcs1PEM(t)

	t.Run("fresh home", func(t *testing.T) {
		h := newHarness(t, "", alice)
		h.app.Exe = func() (string, error) { return "/home/x/.cache/go-build/ab/cd-d/botid", nil }
		stdin := strings.NewReader(input)
		h.app.Stdin = stdin

		err := h.app.Init(context.Background())

		h.assertInitRejectedBeforeAnySideEffect(err, "cannot create units from a temporary or go run executable", stdin, len(input))
		h.assertHomeUntouched()
		for _, rel := range []string{".config/botid", ".local/share/botid", ".config/systemd"} {
			if _, statErr := os.Stat(h.path(rel)); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("%s exists (stat err = %v)", rel, statErr)
			}
		}
		if strings.Contains(h.stdout.String(), "Client ID: ") || strings.Contains(h.stdout.String(), "Overwrite?") {
			t.Errorf("a prompt was printed: %q", h.stdout.String())
		}
	})

	t.Run("a config.json of botid v0.1.0 is not migrated and stays byte-identical", func(t *testing.T) {
		h := newHarness(t, "", alice)
		h.writeLegacyConfig()
		h.app.Exe = func() (string, error) { return "/home/x/.cache/go-build/ab/cd-d/botid", nil }
		stdin := strings.NewReader("y\n" + input)
		h.app.Stdin = stdin

		err := h.app.Init(context.Background())

		h.assertInitRejectedBeforeAnySideEffect(err, "cannot create units from a temporary or go run executable", stdin, len("y\n"+input))
		h.assertLegacyIntact()
		if strings.Contains(h.stdout.String(), "Overwrite?") {
			t.Errorf("the overwrite prompt was printed: %q", h.stdout.String())
		}
	})
}

func TestInitRejectsUnusableExecutableBeforeAnySideEffect(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		want string
	}{
		{"under os.TempDir()", filepath.Join(os.TempDir(), "botid-test-nonexistent", "botid"), "cannot create units from a temporary or go run executable"},
		{"relative path", "botid", `executable path is not absolute: "botid"`},
		{"relative path (./)", "./botid", `executable path is not absolute: "./botid"`},
		{"empty", "", `executable path is not absolute: ""`},
		{"double quote", "/opt/bo\"tid/botid", "executable path contains an unusable character"},
		{"backslash", "/opt/bo\\tid/botid", "executable path contains an unusable character"},
		{"dollar sign", "/opt/bo$tid/botid", "executable path contains an unusable character"},
		{"newline", "/opt/bo\ntid/botid", "executable path contains an unusable character"},
		{"carriage return", "/opt/bo\rtid/botid", "executable path contains an unusable character"},
	}
	input := testClientID + "\n" + pkcs1PEM(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "", alice)
			h.app.Exe = func() (string, error) { return tt.exe, nil }
			stdin := strings.NewReader(input)
			h.app.Stdin = stdin

			err := h.app.Init(context.Background())

			h.assertInitRejectedBeforeAnySideEffect(err, tt.want, stdin, len(input))
			h.assertHomeUntouched()
		})
	}
}

func TestInitWithPastedPEMSingleInstallation(t *testing.T) {
	requireSystemd(t)
	pem := pkcs1PEM(t)
	h := newHarness(t, testClientID+"\n"+pem, alice)

	if err := h.app.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	wantConfig := "{\n" +
		"  \"default\": \"my-app\",\n" +
		"  \"identities\": {\n" +
		"    \"my-app\": {\n" +
		"      \"clientId\": \"Iv23liTEST\",\n" +
		"      \"privateKeyPath\": \"" + h.home + "/.config/botid/identities/my-app/private-key.pem\",\n" +
		"      \"installationId\": 42\n" +
		"    }\n" +
		"  }\n" +
		"}\n"
	if got := h.read(configPath); got != wantConfig {
		t.Errorf("config.json =\n%s\nwant\n%s", got, wantConfig)
	}
	if got := h.read(identityKey); got != pem {
		t.Errorf("private-key.pem = %q, want it to match the pasted PEM", got)
	}
	if got := h.read(identityData + "/gh/hosts.yml"); got != wantHosts {
		t.Errorf("hosts.yml =\n%s\nwant\n%s", got, wantHosts)
	}
	for _, f := range []string{configPath, identityKey, identityData + "/gh/hosts.yml"} {
		if m := h.mode(f); m != 0o600 {
			t.Errorf("%s mode = %o, want 600", f, m)
		}
	}
	for _, d := range []string{
		".config/botid",
		".config/botid/identities",
		".config/botid/identities/my-app",
		".local/share/botid",
		".local/share/botid/identities",
		identityData,
		identityData + "/gh",
		identityData + "/git",
	} {
		if m := h.mode(d); m != 0o700 {
			t.Errorf("%s mode = %o, want 700", d, m)
		}
	}
	if len(h.keys) != 1 || !h.keys[0].Equal(testKey(t)) || h.clientIDs[0] != testClientID {
		t.Errorf("credentials passed to Client do not match the input: clientIDs=%v", h.clientIDs)
	}
	h.assertNoOutputLeaksToken()
}

func TestInitAcceptsPKCS8PastedPEM(t *testing.T) {
	requireSystemd(t)
	pem := pkcs8PEM(t)
	h := newHarness(t, testClientID+"\n"+pem, alice)

	if err := h.app.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := h.read(identityKey); got != pem {
		t.Errorf("private-key.pem = %q, want it to match the pasted PEM", got)
	}
}

func TestInitWithPrivateKeyFilePathStoresPEMContents(t *testing.T) {
	requireSystemd(t)
	pem := pkcs8PEM(t)

	t.Run("absolute path", func(t *testing.T) {
		h := newHarness(t, "", alice)
		keyFile := filepath.Join(t.TempDir(), "downloaded.pem")
		if err := os.WriteFile(keyFile, []byte(pem), 0o644); err != nil {
			t.Fatal(err)
		}
		h.app.Stdin = strings.NewReader(testClientID + "\n" + keyFile + "\n")

		if err := h.app.Init(context.Background()); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if got := h.read(identityKey); got != pem {
			t.Errorf("private-key.pem = %q, want it to match the PEM in the file", got)
		}
		if m := h.mode(identityKey); m != 0o600 {
			t.Errorf("private-key.pem mode = %o, want 600", m)
		}
		if got := h.read(configPath); !strings.Contains(got, `"privateKeyPath": "`+h.home+`/.config/botid/identities/my-app/private-key.pem"`) {
			t.Errorf("privateKeyPath in config.json does not point to the botid copy: %s", got)
		}
	})

	t.Run("~/ expands to Home", func(t *testing.T) {
		h := newHarness(t, "", alice)
		h.write("Downloads/app.pem", pem)
		h.app.Stdin = strings.NewReader(testClientID + "\n~/Downloads/app.pem\n")

		if err := h.app.Init(context.Background()); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if got := h.read(identityKey); got != pem {
			t.Errorf("private-key.pem = %q, want it to match the PEM in the file", got)
		}
	})
}

func TestInitInstallationSelection(t *testing.T) {
	requireSystemd(t)
	pem := pkcs1PEM(t)

	t.Run("a single installation shows the login without asking", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pem, alice)
		if err := h.app.Init(context.Background()); err != nil {
			t.Fatalf("Init: %v", err)
		}
		out := h.stdout.String()
		if strings.Contains(out, "Installation [") {
			t.Errorf("selection prompt was shown: %q", out)
		}
		if !strings.Contains(out, "Installation: alice (id: 42)\n") {
			t.Errorf("stdout does not show the selected login: %q", out)
		}
	})

	t.Run("two or more installations are chosen by number", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pem+"2\n", alice, acme)
		if err := h.app.Init(context.Background()); err != nil {
			t.Fatalf("Init: %v", err)
		}
		out := h.stdout.String()
		for _, want := range []string{"  1) alice (id: 42)\n", "  2) acme-org (id: 77)\n", "Installation [1-2]: "} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout does not contain %q: %q", want, out)
			}
		}
		if got := h.read(configPath); !strings.Contains(got, `"installationId": 77`) {
			t.Errorf("the selected installationId was not saved: %s", got)
		}
		wantReqs := []string{
			"GET /app/installations",
			"POST /app/installations/77/access_tokens",
			"GET /app",
			"GET /users/my-app%5Bbot%5D",
		}
		if got := h.gh.requests(); !equalLines(got, wantReqs) {
			t.Errorf("GitHub requests = %v, want %v", got, wantReqs)
		}
	})

	t.Run("choosing 1 saves the first installation", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pem+"1\n", alice, acme)
		if err := h.app.Init(context.Background()); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if got := h.read(configPath); !strings.Contains(got, `"installationId": 42`) {
			t.Errorf("the selected installationId was not saved: %s", got)
		}
	})
}

func TestInitInvalidSelectionWritesNothing(t *testing.T) {
	requireSystemd(t)
	pem := pkcs1PEM(t)
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"out of range (too large)", "3\n", "installation number 3 is out of range (1-2)"},
		{"out of range (0)", "0\n", "installation number 0 is out of range (1-2)"},
		{"out of range (negative)", "-1\n", "installation number -1 is out of range (1-2)"},
		{"not a number", "acme\n", `installation number "acme" is not a number`},
		{"empty line", "\n", `installation number "" is not a number`},
		{"EOF", "", "cannot read the installation number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, testClientID+"\n"+pem+tt.input, alice, acme)

			err := h.app.Init(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			h.assertHomeUntouched()
			if got := h.rec.lines(); len(got) != 0 {
				t.Errorf("external commands were run: %v", got)
			}
			if got := h.gh.requests(); !equalLines(got, []string{"GET /app/installations"}) {
				t.Errorf("GitHub requests = %v, want only the installation list", got)
			}
		})
	}
}

func TestInitWithNoInstallationsWritesNothing(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t))

	err := h.app.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "this App is not installed on any account") {
		t.Fatalf("error = %v", err)
	}
	h.assertHomeUntouched()
}

func TestInitWithWrongCredentialsWritesNothing(t *testing.T) {
	requireSystemd(t)
	tests := []struct {
		name    string
		failing string
		want    string
	}{
		{"installation list returns 401", "GET /app/installations", "authentication failed (401)"},
		{"token issuing returns 401", "POST /app/installations/42/access_tokens", "authentication failed (401)"},
		{"App info returns 401", "GET /app", "authentication failed (401)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
			h.gh.fail[tt.failing] = 401

			err := h.app.Init(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			h.assertHomeUntouched()
			if got := h.rec.lines(); len(got) != 0 {
				t.Errorf("external commands were run: %v", got)
			}
		})
	}
}

func TestInitRejectsBadPrivateKeyInputWritesNothing(t *testing.T) {
	requireSystemd(t)
	encrypted := "-----BEGIN RSA PRIVATE KEY-----\n" +
		"Proc-Type: 4,ENCRYPTED\n" +
		"DEK-Info: AES-128-CBC,00112233445566778899AABBCCDDEEFF\n" +
		"\n" +
		"ZHVtbXk=\n" +
		"-----END RSA PRIVATE KEY-----\n"
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"encrypted PEM", testClientID + "\n" + encrypted, "encrypted private keys are not supported"},
		{"EOF in the middle of a PEM", testClientID + "\n-----BEGIN RSA PRIVATE KEY-----\nZHVtbXk=\n", "input ended before the PEM -----END line was found"},
		{"broken PEM", testClientID + "\n-----BEGIN RSA PRIVATE KEY-----\nZHVtbXk=\n-----END RSA PRIVATE KEY-----\n", "cannot read PKCS#1 private key"},
		{"nonexistent path", testClientID + "\n/nonexistent/botid-key.pem\n", "/nonexistent/botid-key.pem"},
		{"private key is empty", testClientID + "\n\n", "private key is empty"},
		{"no private key input", testClientID + "\n", "private key is empty"},
		{"Client ID is empty", "\n", "Client ID is empty"},
		{"no input", "", "Client ID is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.input, alice)

			err := h.app.Init(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			h.assertHomeUntouched()
			if got := h.gh.requests(); len(got) != 0 {
				t.Errorf("requests were sent to GitHub: %v", got)
			}
			if got := h.rec.lines(); len(got) != 0 {
				t.Errorf("external commands were run: %v", got)
			}
		})
	}
}

func TestInitDirectoryLayout(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)

	if err := h.app.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for rel, want := range map[string][]string{
		".config":                                 {"botid", "systemd"},
		".config/botid":                           {"config.json", "identities"},
		".config/botid/identities":                {"my-app"},
		".config/botid/identities/my-app":         {"private-key.pem"},
		".local/share/botid":                      {"identities"},
		".local/share/botid/identities":           {"my-app"},
		".local/share/botid/identities/my-app":    {"gh", "git"},
		".local/share/botid/identities/my-app/gh": {"hosts.yml"},
	} {
		if got := h.entries(rel); !equalLines(got, want) {
			t.Errorf("%s contents = %v, want %v", rel, got, want)
		}
	}
	if got := h.entries(".gitconfig"); got != nil {
		t.Errorf("~/.gitconfig was touched: %v", got)
	}
}

func TestInitRunsGitAndGhIsolatedThenSystemdLast(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)

	if err := h.app.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	wantCalls := []string{
		"git config --global user.name my-app[bot]",
		"git config --global user.email 123456+my-app[bot]@users.noreply.github.com",
		"gh auth setup-git",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now botid-issue.timer",
	}
	if got := h.rec.lines(); !equalLines(got, wantCalls) {
		t.Errorf("external command calls =\n%v\nwant\n%v", got, wantCalls)
	}

	wantEnv := []string{
		"GH_CONFIG_DIR=" + h.home + "/.local/share/botid/identities/my-app/gh",
		"GIT_CONFIG_GLOBAL=" + h.home + "/.local/share/botid/identities/my-app/git/config",
	}
	for _, c := range h.rec.calls[:3] {
		if !equalLines(c.Env, wantEnv) {
			t.Errorf("env passed to %s = %v, want %v", c.line(), c.Env, wantEnv)
		}
	}
	for _, c := range h.rec.calls[3:] {
		if len(c.Env) != 0 {
			t.Errorf("env was passed to %s: %v", c.line(), c.Env)
		}
	}

	wantReqs := []string{
		"GET /app/installations",
		"POST /app/installations/42/access_tokens",
		"GET /app",
		"GET /users/my-app%5Bbot%5D",
	}
	if got := h.gh.requests(); !equalLines(got, wantReqs) {
		t.Errorf("GitHub requests = %v, want %v", got, wantReqs)
	}
}

type ctxKey struct{}

func TestInitPassesItsContextToExternalCommands(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
	ctx := context.WithValue(t.Context(), ctxKey{}, "init-ctx")

	if err := h.app.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	want := []string{
		"git config --global user.name my-app[bot] => init-ctx",
		"git config --global user.email 123456+my-app[bot]@users.noreply.github.com => init-ctx",
		"gh auth setup-git => init-ctx",
		"systemctl --user daemon-reload => init-ctx",
		"systemctl --user enable --now botid-issue.timer => init-ctx",
	}
	var got []string
	for _, c := range h.rec.calls {
		got = append(got, fmt.Sprintf("%s => %v", c.line(), c.Ctx))
	}
	if !equalLines(got, want) {
		t.Errorf("context value seen by each external command =\n%v\nwant\n%v", got, want)
	}
}

func TestInitInstallsSystemdUnitsForTheRunningExecutable(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)

	if err := h.app.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	wantService := "[Unit]\n" +
		"Description=Reissue botid credentials\n" +
		"\n" +
		"[Service]\n" +
		"Type=oneshot\n" +
		"ExecStart=\"/opt/botid/bin/botid\" issue\n" +
		"TimeoutStartSec=2min\n"
	if got := h.read(".config/systemd/user/botid-issue.service"); got != wantService {
		t.Errorf("botid-issue.service =\n%s\nwant\n%s", got, wantService)
	}
	if got := h.entries(".config/systemd/user"); !equalLines(got, []string{"botid-issue.service", "botid-issue.timer"}) {
		t.Errorf("unit directory contents = %v", got)
	}
}

func TestInitPrintsSummaryWithRulesAndLingerGuidance(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)

	if err := h.app.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	out := h.stdout.String()
	for _, want := range []string{
		"botid init is complete.",
		"Identity:     my-app (default)",
		h.home + "/.config/botid/config.json",
		h.home + "/.config/botid/identities/my-app/private-key.pem",
		h.home + "/.local/share/botid/identities/my-app/gh/hosts.yml (my-app[bot])",
		h.home + "/.local/share/botid/identities/my-app/git/config",
		"enabled botid-issue.timer (runs botid issue every 45 minutes)",
		`add rules to "rules" in ` + h.home + "/.config/botid/config.json",
		"loginctl enable-linger",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "botid env") {
		t.Errorf("stdout still tells the user to run botid env:\n%s", out)
	}
	h.assertNoOutputLeaksToken()
}

func TestInitAddsASecondIdentityAndKeepsDefaultAndRules(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
	h.gh.slug = "other-app"
	before := h.writeExistingIdentities()

	if err := h.app.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	want := existingConfig()
	want.Identities["other-app"] = config.Identity{
		ClientID:       testClientID,
		PrivateKeyPath: h.path(".config/botid/identities/other-app/private-key.pem"),
		InstallationID: 42,
	}
	if got := h.loadSavedConfig(); !reflect.DeepEqual(got, want) {
		t.Errorf("config.json = %+v, want %+v", got, want)
	}
	if got := h.read(".config/botid/identities/other-app/private-key.pem"); got != pkcs1PEM(t) {
		t.Errorf("the private key of the new identity was not written: %q", got)
	}
	if got := h.read(".local/share/botid/identities/other-app/gh/hosts.yml"); !strings.Contains(got, `"other-app[bot]"`) {
		t.Errorf("hosts.yml of the new identity = %q", got)
	}
	if got := h.read(identityKey); got != "old key" {
		t.Errorf("the key of the other identity was changed: %q", got)
	}
	if got := h.read(identityData + "/gh/hosts.yml"); got != "old hosts" {
		t.Errorf("hosts.yml of the other identity was changed: %q", got)
	}
	if before == h.read(configPath) {
		t.Error("config.json was not updated")
	}
	if got := h.stderr.String(); got != "" {
		t.Errorf("stderr = %q, want it empty (no identity was replaced)", got)
	}
	out := h.stdout.String()
	if !strings.Contains(out, "Identity:     other-app (not the default)") || strings.Contains(out, "Overwrite?") {
		t.Errorf("stdout = %q", out)
	}
	wantCalls := []string{
		"git config --global user.name other-app[bot]",
		"git config --global user.email 123456+other-app[bot]@users.noreply.github.com",
		"gh auth setup-git",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now botid-issue.timer",
	}
	if got := h.rec.lines(); !equalLines(got, wantCalls) {
		t.Errorf("external command calls =\n%v\nwant\n%v", got, wantCalls)
	}
	wantEnv := []string{
		"GH_CONFIG_DIR=" + h.home + "/.local/share/botid/identities/other-app/gh",
		"GIT_CONFIG_GLOBAL=" + h.home + "/.local/share/botid/identities/other-app/git/config",
	}
	for _, c := range h.rec.calls[:3] {
		if !equalLines(c.Env, wantEnv) {
			t.Errorf("env passed to %s = %v, want %v", c.line(), c.Env, wantEnv)
		}
	}
}

func TestInitKeepsTheUserIdentityAsDefault(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
	h.saveConfig(config.Config{Default: config.User, Rules: []config.Rule{{Path: "/srv/**", Identity: config.User}}})

	if err := h.app.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	want := config.Config{
		Default: config.User,
		Identities: map[string]config.Identity{
			"my-app": {ClientID: testClientID, PrivateKeyPath: h.path(identityKey), InstallationID: 42},
		},
		Rules: []config.Rule{{Path: "/srv/**", Identity: config.User}},
	}
	if got := h.loadSavedConfig(); !reflect.DeepEqual(got, want) {
		t.Errorf("config.json = %+v, want %+v", got, want)
	}
	if !strings.Contains(h.stdout.String(), "Identity:     my-app (not the default)") {
		t.Errorf("stdout = %q", h.stdout.String())
	}
}

func TestInitExistingIdentityConfirmation(t *testing.T) {
	requireSystemd(t)
	warning := func(h *harness) string {
		return `Warning: identity "my-app" already exists in ` + h.home + "/.config/botid/config.json. If you continue, its credentials and private key will be overwritten.\n"
	}

	t.Run("y replaces that identity and keeps the rest", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pkcs1PEM(t)+"y\n", alice)
		h.writeExistingIdentities()

		if err := h.app.Init(context.Background()); err != nil {
			t.Fatalf("Init: %v", err)
		}

		want := existingConfig()
		want.Identities["my-app"] = config.Identity{
			ClientID:       testClientID,
			PrivateKeyPath: h.path(identityKey),
			InstallationID: 42,
		}
		if got := h.loadSavedConfig(); !reflect.DeepEqual(got, want) {
			t.Errorf("config.json = %+v, want %+v", got, want)
		}
		if got := h.read(identityKey); got != pkcs1PEM(t) {
			t.Errorf("private-key.pem was not overwritten: %q", got)
		}
		if got := h.read(identityData + "/gh/hosts.yml"); got != wantHosts {
			t.Errorf("hosts.yml was not overwritten: %q", got)
		}
		if got := h.stderr.String(); got != warning(h) {
			t.Errorf("stderr = %q, want %q", got, warning(h))
		}
		if !strings.Contains(h.stdout.String(), "Overwrite? [y/n]: ") {
			t.Errorf("stdout does not contain the confirmation prompt: %q", h.stdout.String())
		}
		if !strings.Contains(h.stdout.String(), "Identity:     my-app (default)") {
			t.Errorf("stdout = %q, want my-app reported as the default", h.stdout.String())
		}
	})

	for _, tt := range []struct {
		name   string
		answer string
	}{
		{"n changes nothing", "n\n"},
		{"EOF changes nothing", ""},
		{"empty line changes nothing", "\n"},
		{"Y (uppercase) is not treated as y", "Y\n"},
		{"yes is not treated as y", "yes\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, testClientID+"\n"+pkcs1PEM(t)+tt.answer, alice)
			before := h.writeExistingIdentities()

			if err := h.app.Init(context.Background()); err != nil {
				t.Fatalf("Init returned an error (expected exit code 0): %v", err)
			}

			h.assertExistingIdentitiesIntact(before)
			if got := h.stderr.String(); got != warning(h) {
				t.Errorf("stderr = %q, want %q", got, warning(h))
			}
			if !strings.Contains(h.stdout.String(), "Exited without changing anything.") {
				t.Errorf("stdout = %q", h.stdout.String())
			}
			if strings.Contains(h.stdout.String(), "botid init is complete") {
				t.Errorf("completion message was printed: %q", h.stdout.String())
			}
		})
	}
}

func TestInitMigratesAConfigOfBotidV010First(t *testing.T) {
	requireSystemd(t)
	warning := func(h *harness) string {
		return h.migrationNotice() + `Warning: identity "my-app" already exists in ` + h.home + "/.config/botid/config.json. If you continue, its credentials and private key will be overwritten.\n"
	}

	t.Run("y replaces the migrated identity", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pkcs1PEM(t)+"y\n", alice)
		h.writeMigratableConfig()

		if err := h.app.Init(context.Background()); err != nil {
			t.Fatalf("Init: %v", err)
		}

		want := config.Config{
			Default: "my-app",
			Identities: map[string]config.Identity{
				"my-app": {ClientID: testClientID, PrivateKeyPath: h.path(identityKey), InstallationID: 42},
			},
		}
		if got := h.loadSavedConfig(); !reflect.DeepEqual(got, want) {
			t.Errorf("config.json = %+v, want %+v", got, want)
		}
		if got := h.read(identityData + "/git/config"); got != legacyGitConfig {
			t.Errorf("the moved git config = %q, want %q", got, legacyGitConfig)
		}
		if got := h.stderr.String(); got != warning(h) {
			t.Errorf("stderr = %q, want %q", got, warning(h))
		}
		if !strings.Contains(h.stdout.String(), "Identity:     my-app (default)") {
			t.Errorf("stdout = %q", h.stdout.String())
		}
	})

	t.Run("n keeps the migrated identity", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pkcs1PEM(t)+"n\n", alice)
		h.writeMigratableConfig()

		if err := h.app.Init(context.Background()); err != nil {
			t.Fatalf("Init returned an error (expected exit code 0): %v", err)
		}

		if got := h.loadSavedConfig(); !reflect.DeepEqual(got, h.migratedConfig()) {
			t.Errorf("config.json = %+v, want %+v", got, h.migratedConfig())
		}
		if got := h.read(legacyKeyPath); got != pkcs1PEM(t) {
			t.Errorf("the private key was changed: %q", got)
		}
		if got := h.stderr.String(); got != warning(h) {
			t.Errorf("stderr = %q, want %q", got, warning(h))
		}
		if !strings.Contains(h.stdout.String(), "Exited without changing anything.") {
			t.Errorf("stdout = %q", h.stdout.String())
		}
	})
}

func TestInitNeverOverwritesAConfigItCannotRead(t *testing.T) {
	requireSystemd(t)
	tests := []struct {
		name string
		body string
		want string
	}{
		{"unknown identity in a rule", `{"default":"user","identities":{},"rules":[{"remote":"a/*","identity":"nobody"}]}`, `unknown identity "nobody"`},
		{"not JSON", "{broken", "cannot read"},
		{"hand-written unknown key", `{"default":"user","identities":{},"note":"mine"}`, `unknown field "note"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "y\n"+testClientID+"\n"+pkcs1PEM(t), alice)
			h.write(configPath, tt.body)

			err := h.app.Init(context.Background())

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if got := h.read(configPath); got != tt.body {
				t.Errorf("config.json was changed: %q", got)
			}
			if got := h.stdout.String(); got != "" {
				t.Errorf("stdout = %q, want no prompt", got)
			}
			if got := h.gh.requests(); len(got) != 0 {
				t.Errorf("requests were sent to GitHub: %v", got)
			}
			if got := h.rec.lines(); len(got) != 0 {
				t.Errorf("external commands were run: %v", got)
			}
			if got := h.entries(".config/botid"); !equalLines(got, []string{"config.json"}) {
				t.Errorf(".config/botid contents = %v", got)
			}
		})
	}
}

func TestInitRejectsAppSlugThatCannotBeAnIdentityName(t *testing.T) {
	requireSystemd(t)
	tests := []struct {
		slug string
		want string
	}{
		{"user", `identity name "user" is reserved for the built-in identity`},
		{".hidden", `identity name ".hidden" must match`},
	}
	for _, tt := range tests {
		t.Run(tt.slug, func(t *testing.T) {
			h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
			h.gh.slug = tt.slug

			err := h.app.Init(context.Background())

			if err == nil || !strings.Contains(err.Error(), "cannot use the App slug as an identity name") || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substrings %q", err, tt.want)
			}
			h.assertHomeUntouched()
			if got := h.rec.lines(); len(got) != 0 {
				t.Errorf("external commands were run: %v", got)
			}
		})
	}
}

func TestInitFailureAfterWritingKeepsWrittenFiles(t *testing.T) {
	requireSystemd(t)

	t.Run("written files are kept and config.json is not written when git config fails", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
		h.rec.errs["git config --global user.name my-app[bot]"] = errors.New("exit status 128")
		h.rec.out["git config --global user.name my-app[bot]"] = "fatal: cannot write\n"

		err := h.app.Init(context.Background())
		if err == nil || !strings.Contains(err.Error(), "git config --global user.name my-app[bot] failed") || !strings.Contains(err.Error(), "fatal: cannot write") {
			t.Fatalf("error = %v", err)
		}
		if got := h.entries(".config/botid"); !equalLines(got, []string{"identities"}) {
			t.Errorf(".config/botid contents = %v, want config.json to be written last", got)
		}
		if got := h.read(identityKey); got != pkcs1PEM(t) {
			t.Errorf("private-key.pem was not kept: %q", got)
		}
		if got := h.read(identityData + "/gh/hosts.yml"); got != wantHosts {
			t.Errorf("hosts.yml was not kept: %q", got)
		}
		if got := h.rec.lines(); !equalLines(got, []string{"git config --global user.name my-app[bot]"}) {
			t.Errorf("external command calls = %v, want it to stop at the failure", got)
		}
	})

	t.Run("an existing config.json is left unchanged when git config fails", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
		h.gh.slug = "other-app"
		before := h.writeExistingIdentities()
		h.rec.errs["gh auth setup-git"] = errors.New("exit status 1")

		if err := h.app.Init(context.Background()); err == nil {
			t.Fatal("expected an error")
		}
		if got := h.read(configPath); got != before {
			t.Errorf("config.json was changed:\n%s\nwant\n%s", got, before)
		}
	})

	t.Run("gh is missing", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
		h.rec.errs["gh auth setup-git"] = &exec.Error{Name: "gh", Err: exec.ErrNotFound}

		err := h.app.Init(context.Background())
		if err == nil || !strings.Contains(err.Error(), "gh not found") {
			t.Fatalf("error = %v, want an error saying gh was not found", err)
		}
		for _, line := range h.rec.lines() {
			if strings.HasPrefix(line, "systemctl") {
				t.Errorf("systemctl was run: %v", h.rec.lines())
			}
		}
	})

	t.Run("git is missing", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
		h.rec.errs["git config --global user.name my-app[bot]"] = &exec.Error{Name: "git", Err: exec.ErrNotFound}

		err := h.app.Init(context.Background())
		if err == nil || !strings.Contains(err.Error(), "git not found") {
			t.Fatalf("error = %v, want an error saying git was not found", err)
		}
	})

	t.Run("written files are kept when systemd registration fails", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
		h.rec.errs["systemctl --user enable --now botid-issue.timer"] = errors.New("exit status 1")
		h.rec.out["systemctl --user enable --now botid-issue.timer"] = "Failed to enable unit: Access denied\n"

		err := h.app.Init(context.Background())
		if err == nil {
			t.Fatal("expected an error")
		}
		for _, want := range []string{"the configuration and credentials were saved", "Failed to enable unit: Access denied", "systemctl --user enable --now botid-issue.timer"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want substring %q", err, want)
			}
		}
		if got := h.entries(".config/botid"); !equalLines(got, []string{"config.json", "identities"}) {
			t.Errorf(".config/botid contents = %v", got)
		}
		if got := h.loadSavedConfig(); got.Default != "my-app" || len(got.Identities) != 1 {
			t.Errorf("config.json = %+v", got)
		}
		if got := h.read(identityData + "/gh/hosts.yml"); got != wantHosts {
			t.Errorf("hosts.yml was not kept: %q", got)
		}
		if strings.Contains(h.stdout.String(), "botid init is complete") {
			t.Errorf("completion message was printed despite the failure: %q", h.stdout.String())
		}
	})
}

func TestInitFailsWhenExecutablePathIsUnavailable(t *testing.T) {
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
	h.app.Exe = func() (string, error) { return "", errors.New("no /proc") }

	err := h.app.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cannot get the path of the botid executable") {
		t.Fatalf("error = %v", err)
	}
	for _, line := range h.rec.lines() {
		if strings.HasPrefix(line, "systemctl") {
			t.Errorf("systemctl was run: %v", h.rec.lines())
		}
	}
	h.assertHomeUntouched()
	if got := h.gh.requests(); len(got) != 0 {
		t.Errorf("requests were sent to GitHub: %v", got)
	}
	if got := h.stdout.String(); got != "" {
		t.Errorf("stdout = %q, want it empty", got)
	}
}

func TestInitStopsOnBotUserIDFailureBeforeRunningGit(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
	h.gh.fail["GET /users/my-app%5Bbot%5D"] = 401

	err := h.app.Init(context.Background())
	if err == nil || !strings.Contains(err.Error(), "installation token is invalid or expired (401)") {
		t.Fatalf("error = %v", err)
	}
	if got := h.rec.lines(); len(got) != 0 {
		t.Errorf("external commands were run: %v", got)
	}
	h.assertNoOutputLeaksToken()
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error contains the token: %q", err)
	}
}
