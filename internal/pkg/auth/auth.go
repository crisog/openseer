package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const tokenPrefix = "ostk_"

func GenerateAPIToken() (token, hash string, err error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	token = tokenPrefix + hex.EncodeToString(bytes)
	hash = HashToken(token)
	return token, hash, nil
}

func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func ValidateTokenFormat(token string) bool {
	if !strings.HasPrefix(token, tokenPrefix) {
		return false
	}
	hexPart := strings.TrimPrefix(token, tokenPrefix)
	if len(hexPart) != 64 {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}
