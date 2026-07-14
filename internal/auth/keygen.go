package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// keyBytes is the entropy behind a public key. 16 bytes (128 bits) is far more
// than brute force can reach, which matters because this key is the only thing
// standing between a stranger and a customer's ingest quota.
const keyBytes = 16

// NewIngestKey mints a public ingest key: pk_live_<32 hex chars>.
//
// crypto/rand, not math/rand — a guessable ingest key lets anyone flood a
// project they do not own. This is the one place in the ingest path where the
// choice of random source is a security decision rather than a style one.
func NewIngestKey() (string, error) {
	buf := make([]byte, keyBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate ingest key: %w", err)
	}
	return KeyPrefix + "live_" + hex.EncodeToString(buf), nil
}
