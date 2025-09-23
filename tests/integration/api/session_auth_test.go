package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	openseerv1connect "github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	"github.com/crisog/openseer/tests/helpers"
)

func TestSessionBasedAuthentication(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	dashboardClient := openseerv1connect.NewDashboardServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	t.Run("MalformedToken_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})
		req.Header().Set("Authorization", "Bearer malformed-token")

		_, err := dashboardClient.GetDashboardOverview(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("MissingBearer_ReturnsUnauthenticated", func(t *testing.T) {
		sessionToken, _, err := helpers.MintSession(ctx, t, env.TestDB.DB)
		require.NoError(t, err)

		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})
		req.Header().Set("Authorization", sessionToken)

		_, err = dashboardClient.GetDashboardOverview(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("ExpiredSession_ReturnsUnauthenticated", func(t *testing.T) {
		userID := "expired-user"

		now := time.Now()
		_, err := env.TestDB.DB.ExecContext(ctx, `
			INSERT INTO "user"(id, name, email, "emailVerified", "createdAt", "updatedAt")
			VALUES ($1, $2, $3, $4, $5, $6)`,
			userID, "Expired User", userID+"@test.local", true, now, now)
		require.NoError(t, err)

		expiredTime := now.Add(-1 * time.Hour)
		tokenID := "expired-token-id"
		sessionID := "expired-session-id"

		_, err = env.TestDB.DB.ExecContext(ctx, `
			INSERT INTO session(id, "expiresAt", token, "createdAt", "updatedAt", "userId")
			VALUES ($1, $2, $3, $4, $5, $6)`,
			sessionID, expiredTime, tokenID, now, now, userID)
		require.NoError(t, err)

		secret := os.Getenv("BETTER_AUTH_SECRET")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(tokenID))
		sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
		expiredSessionToken := tokenID + "." + sig

		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})
		req.Header().Set("Authorization", "Bearer "+expiredSessionToken)

		_, err = dashboardClient.GetDashboardOverview(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("InvalidSignature_ReturnsUnauthenticated", func(t *testing.T) {
		sessionToken, _, err := helpers.MintSession(ctx, t, env.TestDB.DB)
		require.NoError(t, err)

		parts := sessionToken
		invalidToken := parts + "invalid"

		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})
		req.Header().Set("Authorization", "Bearer "+invalidToken)

		_, err = dashboardClient.GetDashboardOverview(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("EmptyAuthorizationHeader_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})

		_, err := dashboardClient.GetDashboardOverview(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("ValidSession_AllowsAccess", func(t *testing.T) {
		sessionToken, _, err := helpers.MintSession(ctx, t, env.TestDB.DB)
		require.NoError(t, err)

		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := dashboardClient.GetDashboardOverview(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Overview)
	})
}