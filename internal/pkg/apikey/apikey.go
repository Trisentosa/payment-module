// Package apikey creates and hashes the API keys calling services use to authenticate.
// PayGate only ever stores the SHA-256 hash of a key; the key itself is shown once.
package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// Prefix marks PayGate keys so secret scanners and humans can recognise a leaked one.
const Prefix = "pgk_"

// Generate returns a new random key: Prefix followed by 32 random bytes, base64url-encoded.
func Generate() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return Prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// Hash returns the hex-encoded SHA-256 of a key, the form stored in configuration.
func Hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// ParseHashes reads "service:sha256hex" entries separated by commas. A service may appear
// more than once, so a new key can be added before the old one is retired.
// The result maps each hash to its service name.
func ParseHashes(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, entry := range strings.Split(s, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		service, hash, ok := strings.Cut(entry, ":")
		service, hash = strings.TrimSpace(service), strings.ToLower(strings.TrimSpace(hash))
		if !ok || service == "" {
			return nil, fmt.Errorf("entry %q: want service:sha256hex", entry)
		}
		if b, err := hex.DecodeString(hash); err != nil || len(b) != sha256.Size {
			return nil, fmt.Errorf("entry for %q: hash must be 64 hex characters (SHA-256)", service)
		}
		if other, dup := out[hash]; dup && other != service {
			return nil, fmt.Errorf("the same key hash is configured for both %q and %q", other, service)
		}
		out[hash] = service
	}
	return out, nil
}
