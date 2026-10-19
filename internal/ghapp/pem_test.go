package ghapp_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/pycabbage/botid/internal/ghapp"
)

func TestParsePEMAcceptsRSAKeys(t *testing.T) {
	key := testKey(t)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		pem  []byte
	}{
		{"PKCS#1", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})},
		{"PKCS#8", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ghapp.ParsePEM(tt.pem)
			if err != nil {
				t.Fatalf("ParsePEM: %v", err)
			}
			if !got.Equal(key) {
				t.Fatal("parsed key does not match the original key")
			}
		})
	}
}

func TestParsePEMRejects(t *testing.T) {
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecPKCS8, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	ecSEC1, err := x509.MarshalECPrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		pem  []byte
		want string
	}{
		{
			name: "encrypted PEM (Proc-Type)",
			pem: pem.EncodeToMemory(&pem.Block{
				Type:    "RSA PRIVATE KEY",
				Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-128-CBC,00112233445566778899AABBCCDDEEFF"},
				Bytes:   []byte("dummy"),
			}),
			want: "encrypted private keys are not supported",
		},
		{
			name: "encrypted PEM (ENCRYPTED PRIVATE KEY)",
			pem:  pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte("dummy")}),
			want: "encrypted private keys are not supported",
		},
		{
			name: "input that is not PEM",
			pem:  []byte("this is not a pem"),
			want: "cannot read private key as PEM",
		},
		{
			name: "empty input",
			pem:  nil,
			want: "cannot read private key as PEM",
		},
		{
			name: "non-RSA key (ECDSA in PKCS#8)",
			pem:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecPKCS8}),
			want: "private key is not RSA",
		},
		{
			name: "non-RSA key (EC PRIVATE KEY)",
			pem:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecSEC1}),
			want: `unsupported PEM type "EC PRIVATE KEY"`,
		},
		{
			name: "broken PKCS#1",
			pem:  pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("broken")}),
			want: "cannot read PKCS#1 private key",
		},
		{
			name: "broken PKCS#8",
			pem:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("broken")}),
			want: "cannot read PKCS#8 private key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ghapp.ParsePEM(tt.pem)
			if err == nil {
				t.Fatalf("expected an error but got a key: %v", got)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}
