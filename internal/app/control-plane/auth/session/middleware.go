package session

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type userContextKeyType struct{}

var userContextKey userContextKeyType

type User struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	EmailVerified bool      `json:"emailVerified"`
	Image         *string   `json:"image"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type Session struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expiresAt"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	IPAddress *string   `json:"ipAddress"`
	UserAgent *string   `json:"userAgent"`
	UserID    string    `json:"userId"`
	User      *User     `json:"user"`
}

type Middleware struct {
	db *sql.DB
}

func NewMiddleware(db *sql.DB) *Middleware {
	return &Middleware{
		db: db,
	}
}

func (m *Middleware) VerifySession(sessionToken string) (*Session, error) {
	parts := strings.SplitN(sessionToken, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid session token format: missing signature")
	}

	tokenId := parts[0]
	providedSignature := parts[1]

	secret := os.Getenv("BETTER_AUTH_SECRET")
	if secret == "" {
		return nil, fmt.Errorf("BETTER_AUTH_SECRET is not set")
	}

	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(tokenId))
	expectedSignature := base64.StdEncoding.EncodeToString(h.Sum(nil))

	if !hmac.Equal([]byte(expectedSignature), []byte(providedSignature)) {
		return nil, fmt.Errorf("invalid session token signature")
	}

	query := `
		SELECT
			s.id, s."expiresAt", s.token, s."createdAt", s."updatedAt", s."ipAddress", s."userAgent", s."userId",
			u.id, u.name, u.email, u."emailVerified", u.image, u."createdAt", u."updatedAt"
		FROM session s
		JOIN "user" u ON s."userId" = u.id
		WHERE s.token = $1 AND s."expiresAt" > NOW()`

	var session Session
	var user User
	var userImage, sessionIP, sessionUA sql.NullString

	err := m.db.QueryRow(query, tokenId).Scan(
		&session.ID, &session.ExpiresAt, &session.Token, &session.CreatedAt, &session.UpdatedAt,
		&sessionIP, &sessionUA, &session.UserID,
		&user.ID, &user.Name, &user.Email, &user.EmailVerified, &userImage, &user.CreatedAt, &user.UpdatedAt,
	)

	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found or expired")
		}
		return nil, fmt.Errorf("failed to verify session: %w", err)
	}

	if userImage.Valid {
		user.Image = &userImage.String
	}
	if sessionIP.Valid {
		session.IPAddress = &sessionIP.String
	}
	if sessionUA.Valid {
		session.UserAgent = &sessionUA.String
	}

	session.User = &user
	return &session, nil
}

func (m *Middleware) ExtractSessionToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && parts[0] == "Bearer" {
			return parts[1]
		}
	}

	cookie, err := r.Cookie("openseer.session_token")
	if err == nil {
		decoded, err := url.QueryUnescape(cookie.Value)
		if err != nil {
			return cookie.Value
		}
		return decoded
	}

	return ""
}

func GetUserFromContext(ctx context.Context) (*User, bool) {
	user, ok := ctx.Value(userContextKey).(*User)
	return user, ok
}

func (m *Middleware) WithSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := m.ExtractSessionToken(r)
		if token != "" {
			if sess, err := m.VerifySession(token); err == nil && sess != nil && sess.User != nil {
				ctx := context.WithValue(r.Context(), userContextKey, sess.User)
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}
