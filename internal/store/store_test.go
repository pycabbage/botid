package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestWriteFileAtomicReplacesExistingFileWithNewPerm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	if m := mode(t, path); m != 0o600 {
		t.Errorf("mode = %o, want 600", m)
	}
}

func TestWriteFileAtomicCreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	if err := WriteFileAtomic(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Errorf("content = %q, want %q", got, "{}")
	}
	if m := mode(t, path); m != 0o600 {
		t.Errorf("mode = %o, want 600", m)
	}
}

func TestWriteFileAtomicLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts.yml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "hosts.yml" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("entries = %v, want [hosts.yml]", names)
	}
}

func TestWriteFileAtomicFailureKeepsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Renaming onto a non-empty directory fails.
	if err := WriteFileAtomic(path, []byte("new"), 0o600); err == nil {
		t.Fatal("WriteFileAtomic into a non-empty directory path succeeded, want error")
	}

	got, err := os.ReadFile(filepath.Join(path, "keep"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Errorf("existing content = %q, want %q", got, "keep")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "target" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("entries after failure = %v, want [target]", names)
	}
}

func TestWriteFileAtomicFailsWithoutParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "file")

	if err := WriteFileAtomic(path, []byte("x"), 0o600); err == nil {
		t.Fatal("WriteFileAtomic succeeded without parent directory, want error")
	}
	if _, err := os.Stat(filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Errorf("parent directory was created: stat err = %v", err)
	}
}

func TestEnsureDirCreatesWith0700(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b")

	if err := EnsureDir(path); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if m := mode(t, path); m != 0o700 {
		t.Errorf("mode = %o, want 700", m)
	}
}

func TestEnsureDirFixesExistingPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := EnsureDir(path); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}
	if m := mode(t, path); m != 0o700 {
		t.Errorf("mode = %o, want 700", m)
	}
}

func TestEnsureDirLeavesAncestorsUntouched(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "share")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "botid", "gh")

	if err := EnsureDir(target); err != nil {
		t.Fatalf("EnsureDir: %v", err)
	}

	if m := mode(t, parent); m != 0o755 {
		t.Errorf("existing ancestor mode = %o, want 755", m)
	}
	if m := mode(t, target); m != 0o700 {
		t.Errorf("target mode = %o, want 700", m)
	}
}

func TestEnsureDirIsIndependentOfUmask(t *testing.T) {
	for _, umask := range []int{0o000, 0o022, 0o077} {
		path := filepath.Join(t.TempDir(), "x", "y")
		old := syscall.Umask(umask)
		err := EnsureDir(path)
		syscall.Umask(old)
		if err != nil {
			t.Fatalf("umask %04o: EnsureDir: %v", umask, err)
		}
		if m := mode(t, path); m != 0o700 {
			t.Errorf("umask %04o: mode = %o, want 700", umask, m)
		}
	}
}

func TestHostsYAML(t *testing.T) {
	got, err := HostsYAML("my-app", "ghs_abc123")
	if err != nil {
		t.Fatalf("HostsYAML: %v", err)
	}
	want := "github.com:\n" +
		"    users:\n" +
		"        \"my-app[bot]\":\n" +
		"            oauth_token: ghs_abc123\n" +
		"    git_protocol: https\n" +
		"    user: \"my-app[bot]\"\n" +
		"    oauth_token: ghs_abc123\n"
	if string(got) != want {
		t.Errorf("HostsYAML =\n%s\nwant\n%s", got, want)
	}
}

func TestHostsYAMLAcceptsNewStyleToken(t *testing.T) {
	got, err := HostsYAML("app", "ghs_12345_eyJhbGciOiJSUzI1NiJ9.e30.sig-_")
	if err != nil {
		t.Fatalf("HostsYAML: %v", err)
	}
	want := "github.com:\n" +
		"    users:\n" +
		"        \"app[bot]\":\n" +
		"            oauth_token: ghs_12345_eyJhbGciOiJSUzI1NiJ9.e30.sig-_\n" +
		"    git_protocol: https\n" +
		"    user: \"app[bot]\"\n" +
		"    oauth_token: ghs_12345_eyJhbGciOiJSUzI1NiJ9.e30.sig-_\n"
	if string(got) != want {
		t.Errorf("HostsYAML =\n%s\nwant\n%s", got, want)
	}
}

func TestHostsYAMLRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name        string
		slug, token string
	}{
		{"empty slug", "", "ghs_x"},
		{"empty token", "app", ""},
		{"newline in slug", "a\nb", "ghs_x"},
		{"newline in token", "app", "ghs_x\nevil: 1"},
		{"carriage return in token", "app", "ghs_x\rb"},
		{"double quote in slug", `a"b`, "ghs_x"},
		{"double quote in token", "app", `ghs_"x`},
		{"backslash in slug", `a\b`, "ghs_x"},
		{"backslash in token", "app", `ghs_\x`},
		{"space in token", "app", "ghs_x #c"},
		{"tab in slug", "a\tb", "ghs_x"},
		{"NUL in token", "app", "ghs_\x00"},
		{"yaml alias token", "app", "*alias"},
		{"yaml anchor token", "app", "&anchor"},
		{"yaml flow token", "app", "{a: b}"},
		{"invalid utf-8 token", "app", "ghs_\xff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := HostsYAML(tt.slug, tt.token)
			if err == nil {
				t.Fatalf("HostsYAML(%q, %q) = %q, want error", tt.slug, tt.token, got)
			}
			if got != nil {
				t.Errorf("HostsYAML returned %q alongside error, want nil", got)
			}
		})
	}
}

func TestDotEnv(t *testing.T) {
	got, err := DotEnv("/home/alice/.local/share/botid/gh", "/home/alice/.local/share/botid/git/config")
	if err != nil {
		t.Fatalf("DotEnv: %v", err)
	}
	want := "GH_CONFIG_DIR='/home/alice/.local/share/botid/gh'\n" +
		"GIT_CONFIG_GLOBAL='/home/alice/.local/share/botid/git/config'\n"
	if got != want {
		t.Errorf("DotEnv = %q, want %q", got, want)
	}
}

func TestDotEnvRejectsUnsafePaths(t *testing.T) {
	const okGH = "/home/alice/.local/share/botid/gh"
	const okGit = "/home/alice/.local/share/botid/git/config"
	tests := []struct {
		name    string
		gh, git string
	}{
		{"quote in gh dir", "/home/o'brien/gh", okGit},
		{"quote in git config", okGH, "/home/o'brien/git/config"},
		{"newline in gh dir", "/home/a\nb/gh", okGit},
		{"newline in git config", okGH, "/home/a\nb/config"},
		{"carriage return in gh dir", "/home/a\rb/gh", okGit},
		{"relative gh dir", "gh", okGit},
		{"relative git config", okGH, "git/config"},
		{"tilde gh dir", "~/.local/share/botid/gh", okGit},
		{"empty gh dir", "", okGit},
		{"empty git config", okGH, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DotEnv(tt.gh, tt.git)
			if err == nil {
				t.Fatalf("DotEnv(%q, %q) = %q, want error", tt.gh, tt.git, got)
			}
			if got != "" {
				t.Errorf("DotEnv returned %q alongside error, want empty string", got)
			}
		})
	}
}
