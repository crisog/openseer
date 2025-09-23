package api_test

import (
	"context"
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

func TestUserAPIWithSessionAuth(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, _, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	userClient := openseerv1connect.NewUserServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	t.Run("GetUserProfile_WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetUserProfileRequest{})

		_, err := userClient.GetUserProfile(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("GetUserProfile_WithValidSession_ReturnsUser", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetUserProfileRequest{})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := userClient.GetUserProfile(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.User)

		user := resp.Msg.User
		require.NotEmpty(t, user.Id)
		require.Equal(t, "Test User", user.Name)
		require.Contains(t, user.Email, "@test.local")
	})

	t.Run("GetUserProfile_WithInvalidSession_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetUserProfileRequest{})
		req.Header().Set("Authorization", "Bearer invalid.token")

		_, err := userClient.GetUserProfile(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}