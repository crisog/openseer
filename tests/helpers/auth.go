package helpers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"math/big"
	"os"
	"testing"
	"time"
)

func MintSession(ctx context.Context, t *testing.T, db *sql.DB) (string, string, error) {
	t.Helper()

	userID := "test-user-" + randomString(8)

	now := time.Now()
	_, err := db.ExecContext(ctx, `
		INSERT INTO "user"(id, name, email, "emailVerified", "createdAt", "updatedAt")
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		userID, "Test User", userID+"@test.local", true, now, now)
	if err != nil {
		return "", "", err
	}

	tokenID := randomString(32)
	sessionID := randomString(24)
	expires := now.Add(24 * time.Hour)

	_, err = db.ExecContext(ctx, `
		INSERT INTO session(id, "expiresAt", token, "createdAt", "updatedAt", "userId")
		VALUES ($1, $2, $3, $4, $5, $6)`,
		sessionID, expires, tokenID, now, now, userID)
	if err != nil {
		return "", "", err
	}

	secret := os.Getenv("BETTER_AUTH_SECRET")
	if secret == "" {
		secret = "test-secret-for-integration-tests"
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tokenID))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return tokenID + "." + sig, userID, nil
}

func MintSessionForUser(ctx context.Context, t *testing.T, db *sql.DB, userID string) (string, error) {
	t.Helper()

	now := time.Now()
	_, err := db.ExecContext(ctx, `
		INSERT INTO "user"(id, name, email, "emailVerified", "createdAt", "updatedAt")
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO NOTHING`,
		userID, "Test User", userID+"@test.local", true, now, now)
	if err != nil {
		return "", err
	}

	tokenID := randomString(32)
	sessionID := randomString(24)
	expires := now.Add(24 * time.Hour)

	_, err = db.ExecContext(ctx, `
		INSERT INTO session(id, "expiresAt", token, "createdAt", "updatedAt", "userId")
		VALUES ($1, $2, $3, $4, $5, $6)`,
		sessionID, expires, tokenID, now, now, userID)
	if err != nil {
		return "", err
	}

	secret := os.Getenv("BETTER_AUTH_SECRET")
	if secret == "" {
		secret = "test-secret-for-integration-tests"
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(tokenID))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	return tokenID + "." + sig, nil
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			panic(err)
		}
		b[i] = letters[idx.Int64()]
	}
	return string(b)
}