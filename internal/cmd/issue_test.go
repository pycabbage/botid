package cmd_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/ghapp"
)

func (h *harness) writeIssueConfig(installationID int64) {
	h.t.Helper()
	h.write(".config/botid/private-key.pem", pkcs1PEM(h.t))
	body, err := json.Marshal(map[string]any{
		"clientId":       testClientID,
		"privateKeyPath": h.path(".config/botid/private-key.pem"),
		"installationId": installationID,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.write(".config/botid/config.json", string(body))
}

const wantHosts = "github.com:\n" +
	"    users:\n" +
	"        \"my-app[bot]\":\n" +
	"            oauth_token: ghs_secret_token\n" +
	"    git_protocol: https\n" +
	"    user: \"my-app[bot]\"\n" +
	"    oauth_token: ghs_secret_token\n"

func TestIssueWritesHostsYML(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.writeIssueConfig(42)

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}

	if got := h.read(".local/share/botid/gh/hosts.yml"); got != wantHosts {
		t.Errorf("hosts.yml =\n%s\nwant\n%s", got, wantHosts)
	}
	if m := h.mode(".local/share/botid/gh/hosts.yml"); m != 0o600 {
		t.Errorf("hosts.yml mode = %o, want 600", m)
	}
	for _, dir := range []string{".local/share/botid", ".local/share/botid/gh"} {
		if m := h.mode(dir); m != 0o700 {
			t.Errorf("%s mode = %o, want 700", dir, m)
		}
	}
	if got := h.entries(".local/share/botid/gh"); !equalLines(got, []string{"hosts.yml"}) {
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
	body, err := json.Marshal(map[string]any{
		"clientId":       testClientID,
		"privateKey":     pkcs8PEM(t),
		"installationId": 42,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.write(".config/botid/config.json", string(body))

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := h.read(".local/share/botid/gh/hosts.yml"); got != wantHosts {
		t.Errorf("hosts.yml =\n%s\nwant\n%s", got, wantHosts)
	}
	if len(h.keys) != 1 || !h.keys[0].Equal(testKey(t)) || h.clientIDs[0] != testClientID {
		t.Errorf("credentials passed to Client do not match config.json: clientIDs=%v", h.clientIDs)
	}
}

func TestIssueReplacesExistingHostsYML(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.writeIssueConfig(42)
	h.write(".local/share/botid/gh/hosts.yml", "old")
	if err := os.Chmod(h.path(".local/share/botid/gh/hosts.yml"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := h.app.Issue(context.Background()); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if got := h.read(".local/share/botid/gh/hosts.yml"); got != wantHosts {
		t.Errorf("hosts.yml =\n%s\nwant\n%s", got, wantHosts)
	}
	if m := h.mode(".local/share/botid/gh/hosts.yml"); m != 0o600 {
		t.Errorf("hosts.yml mode = %o, want 600", m)
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

func TestIssueWithoutInstallationID(t *testing.T) {
	h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
	h.write(".config/botid/private-key.pem", pkcs1PEM(t))
	h.write(".config/botid/config.json", `{"clientId":"`+testClientID+`","privateKeyPath":"`+h.path(".config/botid/private-key.pem")+`"}`)

	err := h.app.Issue(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	want := h.home + "/.config/botid/config.json has no installationId; run botid init again"
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
	if got := h.entries(".local"); got != nil {
		t.Errorf("output directory was created: %v", got)
	}
}

func TestIssueFailureKeepsExistingHostsYML(t *testing.T) {
	for _, failing := range []string{"POST /app/installations/42/access_tokens", "GET /app"} {
		t.Run(failing, func(t *testing.T) {
			h := newHarness(t, "", ghapp.Installation{ID: 42, Login: "alice"})
			h.writeIssueConfig(42)
			h.write(".local/share/botid/gh/hosts.yml", "previous hosts")
			h.gh.fail[failing] = 401

			if err := h.app.Issue(context.Background()); err == nil {
				t.Fatal("expected an error")
			}
			if got := h.read(".local/share/botid/gh/hosts.yml"); got != "previous hosts" {
				t.Errorf("hosts.yml = %q, want the existing contents to be kept", got)
			}
			if got := h.entries(".local/share/botid/gh"); !equalLines(got, []string{"hosts.yml"}) {
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
		{"clientId is empty", `{"clientId":"","privateKeyPath":"/k.pem","installationId":1}`, "clientId is empty"},
		{"both keys are set", `{"clientId":"id","privateKey":"x","privateKeyPath":"/k.pem","installationId":1}`, "both privateKey and privateKeyPath are set"},
		{"unknown field", `{"clientId":"id","privateKeyPath":"/k.pem","installationId":1,"x":1}`, `unknown field "x"`},
		{"key file is missing", `{"clientId":"id","privateKeyPath":"/nonexistent/botid-key.pem","installationId":1}`, "/nonexistent/botid-key.pem"},
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
