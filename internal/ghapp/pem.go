package ghapp

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// ParsePEM reads RSA private keys in PKCS#1 (RSA PRIVATE KEY) and PKCS#8 (PRIVATE KEY) format.
// Encrypted PEM cannot be decrypted with the standard library, so it is rejected with an error.
func ParsePEM(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("cannot read private key as PEM")
	}
	if block.Type == "ENCRYPTED PRIVATE KEY" || strings.Contains(block.Headers["Proc-Type"], "ENCRYPTED") {
		return nil, errors.New("encrypted private keys are not supported; provide a PEM without a passphrase")
	}

	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("cannot read PKCS#1 private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("cannot read PKCS#8 private key: %w", err)
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA (%T); GitHub App private keys are RSA", parsed)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("unsupported PEM type %q; RSA PRIVATE KEY or PRIVATE KEY is required", block.Type)
	}
}
