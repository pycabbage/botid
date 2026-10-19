package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var generateKey = sync.OnceValues(func() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
})

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := generateKey()
	if err != nil {
		t.Fatalf("cannot generate test key: %v", err)
	}
	return key
}

func pkcs1PEM(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func pkcs8PEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSaveThenLoadRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		in   Config
	}{
		{"privateKeyPath", Config{ClientID: "Iv23liABC", PrivateKeyPath: "/home/alice/.config/botid/private-key.pem", InstallationID: 42}},
		{"privateKey", Config{ClientID: "Iv23liABC", PrivateKey: "-----BEGIN RSA PRIVATE KEY-----\nxyz\n-----END RSA PRIVATE KEY-----\n", InstallationID: 7}},
		{"installationId unset", Config{ClientID: "Iv23liABC", PrivateKeyPath: "/k.pem"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := Save(path, tt.in); err != nil {
				t.Fatalf("Save: %v", err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got != tt.in {
				t.Errorf("Load = %+v, want %+v", got, tt.in)
			}
		})
	}
}

func TestSaveWritesExpectedJSONWith0600(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := Config{ClientID: "Iv23liABC", PrivateKeyPath: "/home/alice/.config/botid/private-key.pem", InstallationID: 42}
	if err := Save(path, c); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n" +
		"  \"clientId\": \"Iv23liABC\",\n" +
		"  \"privateKeyPath\": \"/home/alice/.config/botid/private-key.pem\",\n" +
		"  \"installationId\": 42\n" +
		"}\n"
	if string(got) != want {
		t.Errorf("config.json =\n%s\nwant\n%s", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSaveReplacesExistingFilePermission(t *testing.T) {
	path := writeConfig(t, "old")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Config{ClientID: "id", PrivateKeyPath: "/k.pem", InstallationID: 1}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadMissingFileIsErrNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	_, err := Load(path)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want fs.ErrNotExist", err)
	}
}

func TestLoadDoesNotRequireInstallationID(t *testing.T) {
	path := writeConfig(t, `{"clientId":"Iv23liABC","privateKeyPath":"/k.pem"}`)
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{ClientID: "Iv23liABC", PrivateKeyPath: "/k.pem"}
	if got != want {
		t.Errorf("Load = %+v, want %+v", got, want)
	}
}

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"empty clientId", `{"clientId":"","privateKeyPath":"/k.pem","installationId":1}`, "clientId is empty"},
		{"missing clientId", `{"privateKeyPath":"/k.pem","installationId":1}`, "clientId is empty"},
		{"both key sources", `{"clientId":"id","privateKey":"x","privateKeyPath":"/k.pem","installationId":1}`, "both privateKey and privateKeyPath are set"},
		{"neither key source", `{"clientId":"id","installationId":1}`, "either privateKey or privateKeyPath is required"},
		{"both key sources empty", `{"clientId":"id","privateKey":"","privateKeyPath":"","installationId":1}`, "either privateKey or privateKeyPath is required"},
		{"unknown field", `{"clientId":"id","privateKeyPath":"/k.pem","installationId":1,"extra":true}`, `unknown field "extra"`},
		{"not JSON", `clientId=id`, "invalid character"},
		{"empty file", ``, "EOF"},
		{"wrong type", `{"clientId":"id","privateKeyPath":"/k.pem","installationId":"1"}`, "installationId"},
		{"trailing content", `{"clientId":"id","privateKeyPath":"/k.pem"} {}`, "unexpected extra content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.body)
			got, err := Load(path)
			if err == nil {
				t.Fatalf("want error, got result: %+v", got)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want substring %q", err, tt.want)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to contain config.json path %q", err, path)
			}
		})
	}
}

func TestKeyReadsPKCS1AndPKCS8FromPathAndInline(t *testing.T) {
	key := testKey(t)
	for _, format := range []struct {
		name string
		pem  string
	}{
		{"PKCS#1", pkcs1PEM(key)},
		{"PKCS#8", pkcs8PEM(t, key)},
	} {
		t.Run(format.name+" (privateKeyPath)", func(t *testing.T) {
			keyPath := filepath.Join(t.TempDir(), "key.pem")
			if err := os.WriteFile(keyPath, []byte(format.pem), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := Config{ClientID: "id", PrivateKeyPath: keyPath}.Key()
			if err != nil {
				t.Fatalf("Key: %v", err)
			}
			if !got.Equal(key) {
				t.Error("loaded key does not match the original key")
			}
		})
		t.Run(format.name+" (privateKey)", func(t *testing.T) {
			got, err := Config{ClientID: "id", PrivateKey: format.pem}.Key()
			if err != nil {
				t.Fatalf("Key: %v", err)
			}
			if !got.Equal(key) {
				t.Error("loaded key does not match the original key")
			}
		})
	}
}

func TestKeyErrorsNameThePathButNeverTheKeyContents(t *testing.T) {
	key := testKey(t)
	body := pkcs1PEM(key)
	bodyLines := strings.Split(strings.TrimSpace(body), "\n")
	secretLine := bodyLines[1]

	t.Run("broken PEM (privateKeyPath)", func(t *testing.T) {
		keyPath := filepath.Join(t.TempDir(), "key.pem")
		broken := "-----BEGIN RSA PRIVATE KEY-----\n" + secretLine + "\n-----END RSA PRIVATE KEY-----\n"
		if err := os.WriteFile(keyPath, []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := Config{ClientID: "id", PrivateKeyPath: keyPath}.Key()
		if err == nil {
			t.Fatal("want error")
		}
		if !strings.Contains(err.Error(), keyPath) {
			t.Errorf("error = %q, want it to contain key path %q", err, keyPath)
		}
		if strings.Contains(err.Error(), secretLine) {
			t.Errorf("error contains key contents: %q", err)
		}
	})

	t.Run("broken PEM (privateKey)", func(t *testing.T) {
		broken := "-----BEGIN RSA PRIVATE KEY-----\n" + secretLine + "\n-----END RSA PRIVATE KEY-----\n"
		_, err := Config{ClientID: "id", PrivateKey: broken}.Key()
		if err == nil {
			t.Fatal("want error")
		}
		if strings.Contains(err.Error(), secretLine) {
			t.Errorf("error contains key contents: %q", err)
		}
	})

	t.Run("non-PEM string (privateKey)", func(t *testing.T) {
		const secret = "super-secret-not-a-pem"
		_, err := Config{ClientID: "id", PrivateKey: secret}.Key()
		if err == nil {
			t.Fatal("want error")
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error contains key contents: %q", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		keyPath := filepath.Join(t.TempDir(), "missing.pem")
		_, err := Config{ClientID: "id", PrivateKeyPath: keyPath}.Key()
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("error = %v, want fs.ErrNotExist", err)
		}
		if !strings.Contains(err.Error(), keyPath) {
			t.Errorf("error = %q, want it to contain key path %q", err, keyPath)
		}
	})
}

func TestKeyRejectsEncryptedPEM(t *testing.T) {
	encrypted := string(pem.EncodeToMemory(&pem.Block{
		Type:    "RSA PRIVATE KEY",
		Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-128-CBC,00112233445566778899AABBCCDDEEFF"},
		Bytes:   []byte("dummy"),
	}))
	_, err := Config{ClientID: "id", PrivateKey: encrypted}.Key()
	if err == nil || !strings.Contains(err.Error(), "encrypted private keys are not supported") {
		t.Fatalf("error = %v, want unsupported encrypted PEM error", err)
	}
}

func TestKeyRejectsInvalidKeySource(t *testing.T) {
	tests := []struct {
		name string
		c    Config
		want string
	}{
		{"both", Config{ClientID: "id", PrivateKey: "x", PrivateKeyPath: "/k.pem"}, "both privateKey and privateKeyPath are set"},
		{"neither", Config{ClientID: "id"}, "either privateKey or privateKeyPath is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.c.Key()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}
