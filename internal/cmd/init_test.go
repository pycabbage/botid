package cmd_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/ghapp"
)

var (
	alice = ghapp.Installation{ID: 42, Login: "alice"}
	acme  = ghapp.Installation{ID: 77, Login: "acme-org"}
)

func requireSystemd(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Skipf("init checks /run/systemd/system before it reads any input: %v", err)
	}
}

func (h *harness) writeExistingConfig() {
	h.t.Helper()
	h.write(".config/botid/config.json", "{\"clientId\":\"old-client\",\"privateKeyPath\":\"/old/key.pem\",\"installationId\":1}\n")
	h.write(".config/botid/private-key.pem", "old key")
}

func (h *harness) assertExistingConfigIntact() {
	h.t.Helper()
	if got := h.read(".config/botid/config.json"); got != "{\"clientId\":\"old-client\",\"privateKeyPath\":\"/old/key.pem\",\"installationId\":1}\n" {
		h.t.Errorf("config.json was changed: %q", got)
	}
	if got := h.read(".config/botid/private-key.pem"); got != "old key" {
		h.t.Errorf("private-key.pem was changed: %q", got)
	}
	if got := h.entries(".config/botid"); !equalLines(got, []string{"config.json", "private-key.pem"}) {
		h.t.Errorf(".config/botid contents = %v", got)
	}
	if got := h.entries(".local"); got != nil {
		h.t.Errorf("output directory was created: %v", got)
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

	t.Run("existing config.json is not asked about and stays byte-identical", func(t *testing.T) {
		h := newHarness(t, "", alice)
		h.writeExistingConfig()
		h.app.Exe = func() (string, error) { return "/home/x/.cache/go-build/ab/cd-d/botid", nil }
		stdin := strings.NewReader("y\n" + input)
		h.app.Stdin = stdin

		err := h.app.Init(context.Background())

		h.assertInitRejectedBeforeAnySideEffect(err, "cannot create units from a temporary or go run executable", stdin, len("y\n"+input))
		h.assertExistingConfigIntact()
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
		"  \"clientId\": \"Iv23liTEST\",\n" +
		"  \"privateKeyPath\": \"" + h.home + "/.config/botid/private-key.pem\",\n" +
		"  \"installationId\": 42\n" +
		"}\n"
	if got := h.read(".config/botid/config.json"); got != wantConfig {
		t.Errorf("config.json =\n%s\nwant\n%s", got, wantConfig)
	}
	if got := h.read(".config/botid/private-key.pem"); got != pem {
		t.Errorf("private-key.pem = %q, want it to match the pasted PEM", got)
	}
	if got := h.read(".local/share/botid/gh/hosts.yml"); got != wantHosts {
		t.Errorf("hosts.yml =\n%s\nwant\n%s", got, wantHosts)
	}
	for _, f := range []string{".config/botid/config.json", ".config/botid/private-key.pem", ".local/share/botid/gh/hosts.yml"} {
		if m := h.mode(f); m != 0o600 {
			t.Errorf("%s mode = %o, want 600", f, m)
		}
	}
	for _, d := range []string{".config/botid", ".local/share/botid", ".local/share/botid/gh", ".local/share/botid/git"} {
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
	if got := h.read(".config/botid/private-key.pem"); got != pem {
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
		if got := h.read(".config/botid/private-key.pem"); got != pem {
			t.Errorf("private-key.pem = %q, want it to match the PEM in the file", got)
		}
		if m := h.mode(".config/botid/private-key.pem"); m != 0o600 {
			t.Errorf("private-key.pem mode = %o, want 600", m)
		}
		if got := h.read(".config/botid/config.json"); !strings.Contains(got, `"privateKeyPath": "`+h.home+`/.config/botid/private-key.pem"`) {
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
		if got := h.read(".config/botid/private-key.pem"); got != pem {
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
		if got := h.read(".config/botid/config.json"); !strings.Contains(got, `"installationId": 77`) {
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
		if got := h.read(".config/botid/config.json"); !strings.Contains(got, `"installationId": 42`) {
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

	if got := h.entries(".config/botid"); !equalLines(got, []string{"config.json", "private-key.pem"}) {
		t.Errorf(".config/botid contents = %v, want [config.json private-key.pem]", got)
	}
	if got := h.entries(".local/share/botid"); !equalLines(got, []string{"gh", "git"}) {
		t.Errorf(".local/share/botid contents = %v, want [gh git]", got)
	}
	if got := h.entries(".local/share/botid/gh"); !equalLines(got, []string{"hosts.yml"}) {
		t.Errorf(".local/share/botid/gh contents = %v, want [hosts.yml]", got)
	}
	if got := h.entries(".config"); !equalLines(got, []string{"botid", "systemd"}) {
		t.Errorf(".config contents = %v, want [botid systemd]", got)
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
		"GH_CONFIG_DIR=" + h.home + "/.local/share/botid/gh",
		"GIT_CONFIG_GLOBAL=" + h.home + "/.local/share/botid/git/config",
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

func TestInitPrintsSummaryWithEnvAndLingerGuidance(t *testing.T) {
	requireSystemd(t)
	h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)

	if err := h.app.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}

	out := h.stdout.String()
	for _, want := range []string{
		"botid init is complete.",
		h.home + "/.config/botid/config.json",
		h.home + "/.config/botid/private-key.pem",
		h.home + "/.local/share/botid/gh/hosts.yml (my-app[bot])",
		h.home + "/.local/share/botid/git/config",
		"enabled botid-issue.timer (runs botid issue every 45 minutes)",
		"Run botid env and set the printed environment variables once in the coding agent's environment.",
		"loginctl enable-linger",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout does not contain %q:\n%s", want, out)
		}
	}
	h.assertNoOutputLeaksToken()
}

func TestInitExistingConfigConfirmation(t *testing.T) {
	requireSystemd(t)

	t.Run("y overwrites", func(t *testing.T) {
		h := newHarness(t, "y\n"+testClientID+"\n"+pkcs1PEM(t), alice)
		h.writeExistingConfig()

		if err := h.app.Init(context.Background()); err != nil {
			t.Fatalf("Init: %v", err)
		}
		if got := h.read(".config/botid/config.json"); !strings.Contains(got, `"clientId": "Iv23liTEST"`) || !strings.Contains(got, `"installationId": 42`) {
			t.Errorf("config.json was not overwritten: %s", got)
		}
		if got := h.read(".config/botid/private-key.pem"); got != pkcs1PEM(t) {
			t.Errorf("private-key.pem was not overwritten: %q", got)
		}
		if got := h.stderr.String(); got != "Warning: "+h.home+"/.config/botid/config.json already exists. If you continue, the configuration and private key will be overwritten.\n" {
			t.Errorf("stderr = %q", got)
		}
		if !strings.Contains(h.stdout.String(), "Overwrite? [y/n]: ") {
			t.Errorf("stdout does not contain the confirmation prompt: %q", h.stdout.String())
		}
	})

	for _, tt := range []struct {
		name  string
		input string
	}{
		{"n changes nothing", "n\n" + testClientID + "\n" + pkcs1PEM(t)},
		{"EOF changes nothing", ""},
		{"empty line changes nothing", "\n" + testClientID + "\n" + pkcs1PEM(t)},
		{"Y (uppercase) is not treated as y", "Y\n" + testClientID + "\n" + pkcs1PEM(t)},
		{"yes is not treated as y", "yes\n" + testClientID + "\n" + pkcs1PEM(t)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.input, alice)
			h.writeExistingConfig()

			if err := h.app.Init(context.Background()); err != nil {
				t.Fatalf("Init returned an error (expected exit code 0): %v", err)
			}
			h.assertExistingConfigIntact()
		})
	}
}

func TestInitFailureAfterWritingKeepsWrittenFiles(t *testing.T) {
	requireSystemd(t)

	t.Run("written files are kept when git config fails", func(t *testing.T) {
		h := newHarness(t, testClientID+"\n"+pkcs1PEM(t), alice)
		h.rec.errs["git config --global user.name my-app[bot]"] = errors.New("exit status 128")
		h.rec.out["git config --global user.name my-app[bot]"] = "fatal: cannot write\n"

		err := h.app.Init(context.Background())
		if err == nil || !strings.Contains(err.Error(), "git config --global user.name my-app[bot] failed") || !strings.Contains(err.Error(), "fatal: cannot write") {
			t.Fatalf("error = %v", err)
		}
		if got := h.entries(".config/botid"); !equalLines(got, []string{"config.json", "private-key.pem"}) {
			t.Errorf(".config/botid contents = %v", got)
		}
		if got := h.read(".local/share/botid/gh/hosts.yml"); got != wantHosts {
			t.Errorf("hosts.yml was not kept: %q", got)
		}
		if got := h.rec.lines(); !equalLines(got, []string{"git config --global user.name my-app[bot]"}) {
			t.Errorf("external command calls = %v, want it to stop at the failure", got)
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
		if got := h.entries(".config/botid"); !equalLines(got, []string{"config.json", "private-key.pem"}) {
			t.Errorf(".config/botid contents = %v", got)
		}
		if got := h.read(".local/share/botid/gh/hosts.yml"); got != wantHosts {
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
