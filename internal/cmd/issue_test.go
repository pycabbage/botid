package cmd_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/config"
	"github.com/pycabbage/botid/internal/ghapp"
)

func hostsFor(token string) string {
	return "github.com:\n" +
		"    users:\n" +
		"        \"my-app[bot]\":\n" +
		"            oauth_token: " + token + "\n" +
		"    git_protocol: https\n" +
		"    user: \"my-app[bot]\"\n" +
		"    oauth_token: " + token + "\n"
}

var wantHosts = hostsFor(testToken)

const hostsPath = ".local/share/botid/identities/my-app/gh/hosts.yml"

func (h *harness) appIdentity(name string, installationID int64) config.Identity {
	h.t.Helper()
	rel := ".config/botid/identities/" + name + "/private-key.pem"
	h.write(rel, pkcs1PEM(h.t))
	return config.Identity{ClientID: testClientID, PrivateKeyPath: h.path(rel), InstallationID: installationID}
}

func (h *harness) writeIssueConfig(installationID int64) {
	h.t.Helper()
	h.saveConfig(config.Config{
		Default:    "my-app",
		Identities: map[string]config.Identity{"my-app": h.appIdentity("my-app", installationID)},
	})
}

func TestIssueWritesHostsYML(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.writeIssueConfig(42)

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if got := h.read(hostsPath); got != wantHosts {
		t.Errorf("hosts.yml =\n%s\nwant\n%s", got, wantHosts)
	}
	if m := h.mode(hostsPath); m != 0o600 {
		t.Errorf("hosts.yml mode = %o, want 600", m)
	}
	for _, dir := range []string{
		".local/share/botid",
		".local/share/botid/identities",
		".local/share/botid/identities/my-app",
		".local/share/botid/identities/my-app/gh",
	} {
		if m := h.mode(dir); m != 0o700 {
			t.Errorf("%s mode = %o, want 700", dir, m)
		}
	}
	if got := h.entries(".local/share/botid/identities/my-app/gh"); !equalLines(got, []string{"hosts.yml"}) {
		t.Errorf("gh directory contents = %v, want [hosts.yml]", got)
	}
	h.assertNoOutputLeaksToken()
}

func TestIssueRunsNoExternalCommandAndSkipsInstallationList(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"}, ghapp.Installation{ID: 43, Login: "acme"})
	h.writeIssueConfig(42)

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	wantReqs := []string{"POST /app/installations/42/access_tokens", "GET /app"}
	if got := h.gh.requests(); !equalLines(got, wantReqs) {
		t.Errorf("GitHub requests = %v, want %v", got, wantReqs)
	}
	if got := h.rec.lines(); len(got) != 0 {
		t.Errorf("external commands were run: %v", got)
	}
}

func TestIssueReadsInlinePrivateKey(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.saveConfig(config.Config{
		Default: "my-app",
		Identities: map[string]config.Identity{
			"my-app": {ClientID: testClientID, PrivateKey: pkcs8PEM(t), InstallationID: 42},
		},
	})

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := h.read(hostsPath); got != wantHosts {
		t.Errorf("hosts.yml =\n%s\nwant\n%s", got, wantHosts)
	}
	if len(h.keys) != 1 || !h.keys[0].Equal(testKey(t)) || h.clientIDs[0] != testClientID {
		t.Errorf("credentials passed to Client do not match config.json: clientIDs=%v", h.clientIDs)
	}
}

func TestIssueReplacesExistingHostsYML(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.writeIssueConfig(42)
	h.write(hostsPath, "old")
	if err := os.Chmod(h.path(hostsPath), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := h.read(hostsPath); got != wantHosts {
		t.Errorf("hosts.yml =\n%s\nwant\n%s", got, wantHosts)
	}
	if m := h.mode(hostsPath); m != 0o600 {
		t.Errorf("hosts.yml mode = %o, want 600", m)
	}
}

func TestIssueDoesNotChangeConfigJSON(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	c := config.Config{
		Default:    "my-app",
		Identities: map[string]config.Identity{"my-app": h.appIdentity("my-app", 42)},
		Rules:      []config.Rule{{Remote: "github.com/octocorp/*", Branch: "release/**", Identity: "user"}},
	}
	h.saveConfig(c)
	before := h.read(".config/botid/config.json")

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := h.read(".config/botid/config.json"); got != before {
		t.Errorf("config.json changed:\n%s\nwant\n%s", got, before)
	}
}

func TestIssueWritesEveryAppIdentityWithItsOwnToken(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"}, ghapp.Installation{ID: 43, Login: "acme"})
	h.gh.tokens[42] = "ghs_token_alpha"
	h.gh.tokens[43] = "ghs_token_beta"
	h.saveConfig(config.Config{
		Default: "beta",
		Identities: map[string]config.Identity{
			"beta":  h.appIdentity("beta", 43),
			"alpha": h.appIdentity("alpha", 42),
		},
		Rules: []config.Rule{{Remote: "github.com/*/*", Identity: "user"}},
	})

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	for name, token := range map[string]string{"alpha": "ghs_token_alpha", "beta": "ghs_token_beta"} {
		rel := ".local/share/botid/identities/" + name + "/gh/hosts.yml"
		if got := h.read(rel); got != hostsFor(token) {
			t.Errorf("%s =\n%s\nwant\n%s", rel, got, hostsFor(token))
		}
	}
	wantReqs := []string{
		"POST /app/installations/42/access_tokens", "GET /app",
		"POST /app/installations/43/access_tokens", "GET /app",
	}
	if got := h.gh.requests(); !equalLines(got, wantReqs) {
		t.Errorf("GitHub requests = %v, want %v (identities in name order)", got, wantReqs)
	}
	if got := h.entries(".local/share/botid/identities"); !equalLines(got, []string{"alpha", "beta"}) {
		t.Errorf("identity directories = %v, want [alpha beta]", got)
	}
}

func TestIssueContinuesAfterOneIdentityFailsAndReportsEveryFailure(t *testing.T) {
	h := newHarness(t, "",
		ghapp.Installation{ID: 41, Login: "a"}, ghapp.Installation{ID: 42, Login: "b"},
		ghapp.Installation{ID: 43, Login: "c"}, ghapp.Installation{ID: 44, Login: "d"})
	h.gh.fail["POST /app/installations/42/access_tokens"] = 401
	h.saveConfig(config.Config{
		Default: "ok-1",
		Identities: map[string]config.Identity{
			"ok-1":        h.appIdentity("ok-1", 41),
			"bad-network": h.appIdentity("bad-network", 42),
			"no-install":  h.appIdentity("no-install", 0),
			"ok-2":        h.appIdentity("ok-2", 44),
		},
	})

	err := h.app.Issue(context.Background())

	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{
		`identity "bad-network": `,
		"authentication failed (401)",
		`identity "no-install": has no installationId; run botid init again`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want substring %q", err, want)
		}
	}
	if strings.Contains(err.Error(), `identity "ok-`) {
		t.Errorf("error = %q mentions an identity that succeeded", err)
	}
	if strings.Index(err.Error(), `"bad-network"`) > strings.Index(err.Error(), `"no-install"`) {
		t.Errorf("error = %q, want the failures in name order", err)
	}
	for _, name := range []string{"ok-1", "ok-2"} {
		if got := h.read(".local/share/botid/identities/" + name + "/gh/hosts.yml"); got != wantHosts {
			t.Errorf("hosts.yml of %s =\n%s\nwant\n%s", name, got, wantHosts)
		}
	}
	for _, name := range []string{"bad-network", "no-install"} {
		if got := h.entries(".local/share/botid/identities/" + name); got != nil {
			t.Errorf("%s has %v, want nothing written for a failed identity", name, got)
		}
	}
}

func TestIssueWithOnlyTheUserIdentityDoesNothing(t *testing.T) {
	h := newHarness(t, "")
	h.saveConfig(config.Config{
		Default: config.User,
		Rules:   []config.Rule{{Path: "/srv/**", Identity: config.User}},
	})

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := h.gh.requests(); len(got) != 0 {
		t.Errorf("requests were sent to GitHub: %v", got)
	}
	if got := h.entries(".local"); got != nil {
		t.Errorf("output directory was created: %v", got)
	}
}

func TestIssueWithoutConfigFile(t *testing.T) {
	h := newHarness(t, "")

	err := h.app.Issue(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	want := h.home + "/.config/botid/config.json does not exist; run botid init"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	h.assertHomeUntouched()
	if got := h.gh.requests(); len(got) != 0 {
		t.Errorf("requests were sent to GitHub: %v", got)
	}
}

func TestIssueMigratesAConfigOfBotidV010ThenIssuesForIt(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.writeMigratableConfig()

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if got := h.loadSavedConfig(); !reflect.DeepEqual(got, h.migratedConfig()) {
		t.Errorf("config.json = %+v, want %+v", got, h.migratedConfig())
	}
	if got := h.read(hostsPath); got != wantHosts {
		t.Errorf("hosts.yml =\n%s\nwant\n%s", got, wantHosts)
	}
	wantRequests := []string{
		"POST /app/installations/42/access_tokens", "GET /app",
		"POST /app/installations/42/access_tokens", "GET /app",
	}
	if got := h.gh.requests(); !equalLines(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
	if got := h.stderr.String(); got != h.migrationNotice() {
		t.Errorf("stderr = %q, want %q", got, h.migrationNotice())
	}
}

func TestIssueWithoutInstallationID(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.writeIssueConfig(0)

	err := h.app.Issue(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	want := `identity "my-app": has no installationId; run botid init again`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if got := h.gh.requests(); len(got) != 0 {
		t.Errorf("requests were sent to GitHub: %v", got)
	}
	if got := h.entries(".local"); got != nil {
		t.Errorf("output directory was created: %v", got)
	}
}

func TestIssueFailureWritesNothing(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.writeIssueConfig(42)
	h.gh.fail["POST /app/installations/42/access_tokens"] = 401

	err := h.app.Issue(context.Background())
	if err == nil || !strings.Contains(err.Error(), "authentication failed (401)") {
		t.Fatalf("error = %v, want a 401 error", err)
	}
	if !strings.HasPrefix(err.Error(), `identity "my-app": `) {
		t.Errorf("error = %q, want it to name the identity", err)
	}
	if got := h.entries(".local"); got != nil {
		t.Errorf("output directory was created: %v", got)
	}
}

func TestIssueFailureKeepsExistingHostsYML(t *testing.T) {
	for _, failing := range []string{"POST /app/installations/42/access_tokens", "GET /app"} {
		t.Run(failing, func(t *testing.T) {
			h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
			h.writeIssueConfig(42)
			h.write(hostsPath, "previous hosts")
			h.gh.fail[failing] = 401

			if err := h.app.Issue(context.Background()); err == nil {
				t.Fatal("expected an error")
			}
			if got := h.read(hostsPath); got != "previous hosts" {
				t.Errorf("hosts.yml = %q, want the existing contents to be kept", got)
			}
			if got := h.entries(".local/share/botid/identities/my-app/gh"); !equalLines(got, []string{"hosts.yml"}) {
				t.Errorf("gh directory contents = %v, want [hosts.yml]", got)
			}
		})
	}
}

func TestIssueRejectsBrokenConfig(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"clientId is empty", `{"default":"bot","identities":{"bot":{"clientId":"","privateKeyPath":"/k.pem","installationId":1}}}`, "clientId is empty"},
		{"both keys are set", `{"default":"bot","identities":{"bot":{"clientId":"id","privateKey":"x","privateKeyPath":"/k.pem","installationId":1}}}`, "both privateKey and privateKeyPath are set"},
		{"unknown field", `{"default":"bot","identities":{"bot":{"clientId":"id","privateKeyPath":"/k.pem","installationId":1}},"x":1}`, `unknown field "x"`},
		{"unknown identity in a rule", `{"default":"bot","identities":{"bot":{"clientId":"id","privateKeyPath":"/k.pem","installationId":1}},"rules":[{"remote":"a/*","identity":"nobody"}]}`, `unknown identity "nobody"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, "")
			h.write(".config/botid/config.json", tt.body)

			err := h.app.Issue(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
			if got := h.gh.requests(); len(got) != 0 {
				t.Errorf("requests were sent to GitHub: %v", got)
			}
			if got := h.entries(".local"); got != nil {
				t.Errorf("output directory was created: %v", got)
			}
		})
	}
}

func TestIssueReportsAMissingKeyFileForTheIdentity(t *testing.T) {
	h := newHarness(t, "")
	h.saveConfig(config.Config{
		Default: "bot",
		Identities: map[string]config.Identity{
			"bot": {ClientID: "id", PrivateKeyPath: "/nonexistent/botid-key.pem", InstallationID: 1},
		},
	})

	err := h.app.Issue(context.Background())

	if err == nil || !strings.Contains(err.Error(), `identity "bot": `) || !strings.Contains(err.Error(), "/nonexistent/botid-key.pem") {
		t.Fatalf("error = %v, want it to name the identity and the key file", err)
	}
	if got := h.gh.requests(); len(got) != 0 {
		t.Errorf("requests were sent to GitHub: %v", got)
	}
}
