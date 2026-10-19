package paths

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	got := New("/home/alice")
	want := Paths{
		ConfigDir:      "/home/alice/.config/botid",
		ConfigFile:     "/home/alice/.config/botid/config.json",
		PrivateKeyFile: "/home/alice/.config/botid/private-key.pem",
		DataDir:        "/home/alice/.local/share/botid",
		GHConfigDir:    "/home/alice/.local/share/botid/gh",
		HostsFile:      "/home/alice/.local/share/botid/gh/hosts.yml",
		GitConfigDir:   "/home/alice/.local/share/botid/git",
		GitConfigFile:  "/home/alice/.local/share/botid/git/config",
	}
	if got != want {
		t.Errorf("New(\"/home/alice\") = %+v, want %+v", got, want)
	}
}

func TestNewSeparatesConfigAndGeneratedFiles(t *testing.T) {
	p := New("/home/alice")
	for name, path := range map[string]string{
		"ConfigFile":     p.ConfigFile,
		"PrivateKeyFile": p.PrivateKeyFile,
	} {
		if !strings.HasPrefix(path, "/home/alice/.config/botid/") {
			t.Errorf("%s = %q, want under /home/alice/.config/botid/", name, path)
		}
	}
	for name, path := range map[string]string{
		"HostsFile":     p.HostsFile,
		"GitConfigFile": p.GitConfigFile,
		"GHConfigDir":   p.GHConfigDir,
		"GitConfigDir":  p.GitConfigDir,
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
		ConfigDir:      "/home/alice/.config/botid",
		ConfigFile:     "/home/alice/.config/botid/config.json",
		PrivateKeyFile: "/home/alice/.config/botid/private-key.pem",
		DataDir:        "/home/alice/.local/share/botid",
		GHConfigDir:    "/home/alice/.local/share/botid/gh",
		HostsFile:      "/home/alice/.local/share/botid/gh/hosts.yml",
		GitConfigDir:   "/home/alice/.local/share/botid/git",
		GitConfigFile:  "/home/alice/.local/share/botid/git/config",
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
