package ghapp

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

const jwtHeader = `{"alg":"RS256","typ":"JWT"}`

type jwtClaims struct {
	Iss string `json:"iss"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
}

// jwt is created on every call. iat is set back 60 seconds to absorb clock skew, and exp is 540 seconds, within the 10 minute maximum.
func (c *Client) jwt() (string, error) {
	if c.Key == nil {
		return "", errors.New("private key is not set")
	}
	now := c.now().Unix()
	payload, err := json.Marshal(jwtClaims{Iss: c.ClientID, Iat: now - 60, Exp: now + 540})
	if err != nil {
		return "", fmt.Errorf("cannot create JWT payload: %w", err)
	}

	enc := base64.RawURLEncoding
	signingInput := enc.EncodeToString([]byte(jwtHeader)) + "." + enc.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(nil, c.Key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("cannot sign JWT: %w", err)
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}
