package paths

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	got := New("/home/alice")
	want := Paths{
		ConfigDir:  "/home/alice/.config/botid",
		ConfigFile: "/home/alice/.config/botid/config.json",
		DataDir:    "/home/alice/.local/share/botid",
	}
	if got != want {
		t.Errorf("New(\"/home/alice\") = %+v, want %+v", got, want)
	}
}

func TestIdentity(t *testing.T) {
	got := New("/home/alice").Identity("my-app")
	want := Identity{
		ConfigDir:      "/home/alice/.config/botid/identities/my-app",
		PrivateKeyFile: "/home/alice/.config/botid/identities/my-app/private-key.pem",
		DataDir:        "/home/alice/.local/share/botid/identities/my-app",
		GHConfigDir:    "/home/alice/.local/share/botid/identities/my-app/gh",
		HostsFile:      "/home/alice/.local/share/botid/identities/my-app/gh/hosts.yml",
		GitConfigDir:   "/home/alice/.local/share/botid/identities/my-app/git",
		GitConfigFile:  "/home/alice/.local/share/botid/identities/my-app/git/config",
	}
	if got != want {
		t.Errorf("Identity(\"my-app\") = %+v, want %+v", got, want)
	}
}

func TestIdentitiesDoNotShareFiles(t *testing.T) {
	p := New("/home/alice")
	a, b := p.Identity("bot-a"), p.Identity("bot-b")
	if a.PrivateKeyFile == b.PrivateKeyFile || a.HostsFile == b.HostsFile || a.GitConfigFile == b.GitConfigFile {
		t.Errorf("identities share a file: %+v, %+v", a, b)
	}
}

func TestIdentitySeparatesConfigAndGeneratedFiles(t *testing.T) {
	id := New("/home/alice").Identity("my-app")
	for name, path := range map[string]string{
		"ConfigDir":      id.ConfigDir,
		"PrivateKeyFile": id.PrivateKeyFile,
	} {
		if !strings.HasPrefix(path, "/home/alice/.config/botid/") {
			t.Errorf("%s = %q, want under /home/alice/.config/botid/", name, path)
		}
	}
	for name, path := range map[string]string{
		"DataDir":       id.DataDir,
		"HostsFile":     id.HostsFile,
		"GitConfigFile": id.GitConfigFile,
		"GHConfigDir":   id.GHConfigDir,
		"GitConfigDir":  id.GitConfigDir,
	} {
		if !strings.HasPrefix(path, "/home/alice/.local/share/botid/") {
			t.Errorf("%s = %q, want under /home/alice/.local/share/botid/", name, path)
		}
	}
}

func TestNewIgnoresXDGEnvironment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	t.Setenv("XDG_DATA_HOME", "/xdg/data")
	t.Setenv("HOME", "/somewhere/else")

	got := New("/home/alice")
	want := Paths{
		ConfigDir:  "/home/alice/.config/botid",
		ConfigFile: "/home/alice/.config/botid/config.json",
		DataDir:    "/home/alice/.local/share/botid",
	}
	if got != want {
		t.Errorf("New(\"/home/alice\") = %+v, want %+v", got, want)
	}
}

func TestHome(t *testing.T) {
	t.Setenv("HOME", "/home/bob")
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	got, err := Home()
	if err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	if got != "/home/bob" {
		t.Errorf("Home() = %q, want %q", got, "/home/bob")
	}
}

func TestHomeRejectsRelativePath(t *testing.T) {
	t.Setenv("HOME", "relative/home")
	got, err := Home()
	if err == nil {
		t.Fatalf("Home() = %q, want error", got)
	}
	if got != "" {
		t.Errorf("Home() = %q on error, want empty string", got)
	}
}

func TestHomeRejectsEmpty(t *testing.T) {
	t.Setenv("HOME", "")
	got, err := Home()
	if err == nil {
		t.Fatalf("Home() = %q, want error", got)
	}
}
