package api

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func hashEmail(email string) string {
	normalizedEmail := strings.ToLower(strings.TrimSpace(email))
	hashedEmail := sha256.Sum256([]byte(normalizedEmail))
	return hex.EncodeToString(hashedEmail[:])[:16] // Use first 16 chars of hash for uniqueness and obfuscation
}
