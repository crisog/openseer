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

func TestDashboardAPIWithSessionAuth(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, userID, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	dashboardClient := openseerv1connect.NewDashboardServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	t.Run("GetDashboardOverview_WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})

		_, err := dashboardClient.GetDashboardOverview(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("GetDashboardOverview_WithValidSession_ReturnsOverview", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := dashboardClient.GetDashboardOverview(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Overview)

		overview := resp.Msg.Overview
		require.Equal(t, int64(0), overview.TotalMonitors)
		require.Equal(t, int64(0), overview.EnabledMonitors)
		require.Equal(t, int64(0), overview.DisabledMonitors)
		require.Equal(t, int64(0), overview.HealthyMonitors)
		require.Equal(t, int64(0), overview.UnhealthyMonitors)
		require.Empty(t, overview.RecentFailures)
		require.Empty(t, overview.SlowestMonitors)
	})

	t.Run("GetDashboardOverview_WithMonitors_ReturnsCorrectCounts", func(t *testing.T) {
		monitor1 := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
			UserID:     userID,
			Name:       "Test Monitor 1",
			URL:        "https://example.com/health1",
			Regions:    []string{"us-east-1"},
			IntervalMs: 60000,
			TimeoutMs:  5000,
			Enabled:    helpers.BoolPtr(true),
		})

		_ = helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
			UserID:     userID,
			Name:       "Test Monitor 2",
			URL:        "https://example.com/health2",
			Regions:    []string{"us-east-1"},
			IntervalMs: 30000,
			TimeoutMs:  3000,
			Enabled:    helpers.BoolPtr(false),
		})

		helpers.CreateTestResult(t, env.Queries, "run-1", monitor1.ID, "us-east-1")

		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := dashboardClient.GetDashboardOverview(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Overview)

		overview := resp.Msg.Overview
		require.Equal(t, int64(2), overview.TotalMonitors)
		require.Equal(t, int64(1), overview.EnabledMonitors)
		require.Equal(t, int64(1), overview.DisabledMonitors)
		require.Equal(t, int64(1), overview.HealthyMonitors)
		require.Equal(t, int64(0), overview.UnhealthyMonitors)
	})

	t.Run("GetDashboardOverview_WithInvalidSession_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetDashboardOverviewRequest{})
		req.Header().Set("Authorization", "Bearer invalid.token")

		_, err := dashboardClient.GetDashboardOverview(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})
}

