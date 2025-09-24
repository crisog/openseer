package api_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	openseerv1connect "github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	"github.com/crisog/openseer/tests/helpers"
)

func TestMonitorsAPI_ListMonitors(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, userID, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	monitorsClient := openseerv1connect.NewMonitorsServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	t.Run("WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.ListMonitorsRequest{})

		_, err := monitorsClient.ListMonitors(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("WithValidSession_ReturnsMonitors", func(t *testing.T) {
		helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
			UserID:     userID,
			Name:       "Test Monitor",
			URL:        "https://example.com/health",
			Regions:    []string{"us-east-1"},
			IntervalMs: 60000,
			TimeoutMs:  5000,
		})

		req := connect.NewRequest(&openseerv1.ListMonitorsRequest{})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.ListMonitors(ctx, req)
		require.NoError(t, err)
		require.Len(t, resp.Msg.Monitors, 1)

		monitor := resp.Msg.Monitors[0]
		require.Equal(t, "Test Monitor", monitor.Name)
		require.Equal(t, "https://example.com/health", monitor.Url)
		require.Equal(t, []string{"us-east-1"}, monitor.Regions)
		require.Equal(t, int32(60000), monitor.IntervalMs)
		require.Equal(t, int32(5000), monitor.TimeoutMs)
	})

	t.Run("WithMultipleUsers_ReturnsOnlyUserMonitors", func(t *testing.T) {
		helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
			UserID:  userID,
			Name:    "User Monitor",
			URL:     "https://example.com/user",
			Regions: []string{"us-east-1"},
		})

		helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
			UserID:  "other-user",
			Name:    "Other User Monitor",
			URL:     "https://example.com/other",
			Regions: []string{"us-east-1"},
		})

		req := connect.NewRequest(&openseerv1.ListMonitorsRequest{})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.ListMonitors(ctx, req)
		require.NoError(t, err)

		userMonitorCount := 0
		for _, monitor := range resp.Msg.Monitors {
			if monitor.Name == "User Monitor" {
				userMonitorCount++
			}
			require.NotEqual(t, "Other User Monitor", monitor.Name)
		}
		require.Equal(t, 1, userMonitorCount)
	})
}

func TestMonitorsAPI_CreateMonitor(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, _, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	monitorsClient := openseerv1connect.NewMonitorsServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	t.Run("WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.CreateMonitorRequest{
			Id:   uuid.NewString(),
			Name: "Test Monitor",
			Url:  "https://example.com/test",
		})

		_, err := monitorsClient.CreateMonitor(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("WithValidData_CreatesMonitor", func(t *testing.T) {
		monitorID := uuid.NewString()
		req := connect.NewRequest(&openseerv1.CreateMonitorRequest{
			Id:         monitorID,
			Name:       "New Test Monitor",
			Url:        "https://example.com/new",
			IntervalMs: 30000,
			TimeoutMs:  3000,
			Regions:    []string{"us-east-1", "eu-west-1"},
			Method:     "POST",
			Enabled:    helpers.BoolPtr(true),
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.CreateMonitor(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Monitor)

		monitor := resp.Msg.Monitor
		require.Equal(t, monitorID, monitor.Id)
		require.Equal(t, "New Test Monitor", monitor.Name)
		require.Equal(t, "https://example.com/new", monitor.Url)
		require.Equal(t, int32(30000), monitor.IntervalMs)
		require.Equal(t, int32(3000), monitor.TimeoutMs)
		require.Equal(t, []string{"us-east-1", "eu-west-1"}, monitor.Regions)
		require.Equal(t, "POST", monitor.Method)
		require.True(t, monitor.Enabled)
	})

	t.Run("WithHeaders_CreatesMonitorWithHeaders", func(t *testing.T) {
		headers, err := structpb.NewStruct(map[string]interface{}{
			"Authorization": "Bearer token",
			"Content-Type":  "application/json",
		})
		require.NoError(t, err)

		req := connect.NewRequest(&openseerv1.CreateMonitorRequest{
			Id:      uuid.NewString(),
			Name:    "Monitor with Headers",
			Url:     "https://api.example.com/endpoint",
			Headers: headers,
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.CreateMonitor(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Monitor.Headers)
		require.Equal(t, "Bearer token", resp.Msg.Monitor.Headers.Fields["Authorization"].GetStringValue())
	})

	t.Run("WithMissingRequiredFields_ReturnsInvalidArgument", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.CreateMonitorRequest{
			Name: "Incomplete Monitor",
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		_, err := monitorsClient.CreateMonitor(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestMonitorsAPI_GetMonitor(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, userID, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	monitorsClient := openseerv1connect.NewMonitorsServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		UserID:  userID,
		Name:    "Get Test Monitor",
		URL:     "https://example.com/get",
		Regions: []string{"us-east-1"},
	})

	t.Run("WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorRequest{
			Id: monitor.ID,
		})

		_, err := monitorsClient.GetMonitor(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("WithValidID_ReturnsMonitor", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorRequest{
			Id: monitor.ID,
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.GetMonitor(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Monitor)

		returnedMonitor := resp.Msg.Monitor
		require.Equal(t, monitor.ID, returnedMonitor.Id)
		require.Equal(t, "Get Test Monitor", returnedMonitor.Name)
		require.Equal(t, "https://example.com/get", returnedMonitor.Url)
	})

	t.Run("WithNonExistentID_ReturnsNotFound", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorRequest{
			Id: uuid.NewString(),
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		_, err := monitorsClient.GetMonitor(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("WithOtherUserMonitor_ReturnsNotFound", func(t *testing.T) {
		otherUserMonitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
			UserID:  "other-user",
			Name:    "Other User Monitor",
			URL:     "https://example.com/other",
			Regions: []string{"us-east-1"},
		})

		req := connect.NewRequest(&openseerv1.GetMonitorRequest{
			Id: otherUserMonitor.ID,
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		_, err := monitorsClient.GetMonitor(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}

func TestMonitorsAPI_UpdateMonitor(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, userID, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	monitorsClient := openseerv1connect.NewMonitorsServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		UserID:  userID,
		Name:    "Update Test Monitor",
		URL:     "https://example.com/update",
		Regions: []string{"us-east-1"},
	})

	t.Run("WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.UpdateMonitorRequest{
			Id:   monitor.ID,
			Name: helpers.StringPtr("Updated Name"),
		})

		_, err := monitorsClient.UpdateMonitor(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("WithValidUpdate_UpdatesMonitor", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.UpdateMonitorRequest{
			Id:         monitor.ID,
			Name:       helpers.StringPtr("Updated Monitor Name"),
			Url:        helpers.StringPtr("https://updated.example.com"),
			IntervalMs: helpers.Int32Ptr(45000),
			TimeoutMs:  helpers.Int32Ptr(8000),
			Enabled:    helpers.BoolPtr(false),
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.UpdateMonitor(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Monitor)

		updatedMonitor := resp.Msg.Monitor
		require.Equal(t, monitor.ID, updatedMonitor.Id)
		require.Equal(t, "Updated Monitor Name", updatedMonitor.Name)
		require.Equal(t, "https://updated.example.com", updatedMonitor.Url)
		require.Equal(t, int32(45000), updatedMonitor.IntervalMs)
		require.Equal(t, int32(8000), updatedMonitor.TimeoutMs)
		require.False(t, updatedMonitor.Enabled)
	})

	t.Run("WithNonExistentID_ReturnsNotFound", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.UpdateMonitorRequest{
			Id:   uuid.NewString(),
			Name: helpers.StringPtr("Updated Name"),
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		_, err := monitorsClient.UpdateMonitor(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}

func TestMonitorsAPI_GetMonitorMetrics(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, userID, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	monitorsClient := openseerv1connect.NewMonitorsServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		UserID:  userID,
		Name:    "Metrics Test Monitor",
		URL:     "https://example.com/metrics",
		Regions: []string{"us-east-1"},
	})

	t.Run("WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorMetricsRequest{
			MonitorId: monitor.ID,
		})

		_, err := monitorsClient.GetMonitorMetrics(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("WithValidMonitorID_ReturnsEmptyMetrics", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorMetricsRequest{
			MonitorId: monitor.ID,
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.GetMonitorMetrics(ctx, req)
		require.NoError(t, err)
		require.Equal(t, 0, len(resp.Msg.Metrics))
	})

	t.Run("WithNonExistentMonitorID_ReturnsNotFound", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorMetricsRequest{
			MonitorId: uuid.NewString(),
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		_, err := monitorsClient.GetMonitorMetrics(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}

func TestMonitorsAPI_GetMonitorUptime(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, userID, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	monitorsClient := openseerv1connect.NewMonitorsServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		UserID:  userID,
		Name:    "Uptime Test Monitor",
		URL:     "https://example.com/uptime",
		Regions: []string{"us-east-1"},
	})

	t.Run("WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorUptimeRequest{
			MonitorId: monitor.ID,
			TimeRange: "24h",
		})

		_, err := monitorsClient.GetMonitorUptime(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("WithValidTimeRange_ReturnsUptime", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorUptimeRequest{
			MonitorId: monitor.ID,
			TimeRange: "24h",
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.GetMonitorUptime(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg)
		require.GreaterOrEqual(t, resp.Msg.TotalChecks, int64(0))
		require.GreaterOrEqual(t, resp.Msg.UptimePercentage, 0.0)
	})

	t.Run("WithInvalidTimeRange_ReturnsInvalidArgument", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorUptimeRequest{
			MonitorId: monitor.ID,
			TimeRange: "invalid",
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		_, err := monitorsClient.GetMonitorUptime(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}

func TestMonitorsAPI_GetMonitorResults(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, userID, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	monitorsClient := openseerv1connect.NewMonitorsServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		UserID:  userID,
		Name:    "Results Test Monitor",
		URL:     "https://example.com/results",
		Regions: []string{"us-east-1"},
	})

	helpers.CreateTestResult(t, env.Queries, "result-1", monitor.ID, "us-east-1")
	helpers.CreateTestResult(t, env.Queries, "result-2", monitor.ID, "us-east-1")

	t.Run("WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorResultsRequest{
			MonitorId: monitor.ID,
		})

		_, err := monitorsClient.GetMonitorResults(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("WithValidMonitorID_ReturnsResults", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorResultsRequest{
			MonitorId: monitor.ID,
			Limit:     10,
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.GetMonitorResults(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Results)
		require.Len(t, resp.Msg.Results, 2)

		for _, result := range resp.Msg.Results {
			require.Equal(t, monitor.ID, result.MonitorId)
			require.Equal(t, "us-east-1", result.Region)
			require.Equal(t, "OK", result.Status)
		}
	})

	t.Run("WithNonExistentMonitorID_ReturnsNotFound", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.GetMonitorResultsRequest{
			MonitorId: uuid.NewString(),
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		_, err := monitorsClient.GetMonitorResults(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})
}

func TestMonitorsAPI_DeleteMonitor(t *testing.T) {
	require.NoError(t, os.Setenv("BETTER_AUTH_SECRET", "test-secret-for-integration-tests"))
	defer os.Unsetenv("BETTER_AUTH_SECRET")

	env := helpers.SetupControlPlane(t)
	webServer := env.StartWebServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sessionToken, userID, err := helpers.MintSession(ctx, t, env.TestDB.DB)
	require.NoError(t, err)

	monitorsClient := openseerv1connect.NewMonitorsServiceClient(
		http.DefaultClient,
		webServer.URL,
	)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		UserID:  userID,
		Name:    "Delete Test Monitor",
		URL:     "https://example.com/delete",
		Regions: []string{"us-east-1"},
	})

	t.Run("WithoutAuth_ReturnsUnauthenticated", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.DeleteMonitorRequest{
			Id: monitor.ID,
		})

		_, err := monitorsClient.DeleteMonitor(ctx, req)
		require.Error(t, err)
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("WithValidID_DeletesMonitor", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.DeleteMonitorRequest{
			Id: monitor.ID,
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.DeleteMonitor(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg)

		_, err = env.Queries.GetMonitor(ctx, monitor.ID)
		require.Error(t, err, "GetMonitor should fail for deleted monitor")

		deletedMonitor, err := env.Queries.GetMonitorIncludingDeleted(ctx, monitor.ID)
		require.NoError(t, err)
		require.NotNil(t, deletedMonitor)
		require.True(t, deletedMonitor.DeletedAt.Valid, "deleted monitor should have deleted_at set")

		listReq := connect.NewRequest(&openseerv1.ListMonitorsRequest{})
		listReq.Header().Set("Authorization", "Bearer "+sessionToken)
		listResp, err := monitorsClient.ListMonitors(ctx, listReq)
		require.NoError(t, err)

		for _, m := range listResp.Msg.Monitors {
			require.NotEqual(t, monitor.ID, m.Id, "Deleted monitor should not appear in list")
		}
	})

	t.Run("WithNonExistentID_ReturnsSuccess", func(t *testing.T) {
		req := connect.NewRequest(&openseerv1.DeleteMonitorRequest{
			Id: uuid.NewString(),
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.DeleteMonitor(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg)
	})

	t.Run("WithOtherUserMonitor_DoesNotDelete", func(t *testing.T) {
		otherUserMonitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
			UserID:  "other-user-id",
			Name:    "Other User Monitor",
			URL:     "https://example.com/other-delete",
			Regions: []string{"us-east-1"},
		})

		req := connect.NewRequest(&openseerv1.DeleteMonitorRequest{
			Id: otherUserMonitor.ID,
		})
		req.Header().Set("Authorization", "Bearer "+sessionToken)

		resp, err := monitorsClient.DeleteMonitor(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp.Msg)

		stillExists, err := env.Queries.GetMonitor(ctx, otherUserMonitor.ID)
		require.NoError(t, err)
		require.NotNil(t, stillExists)
		require.Equal(t, otherUserMonitor.ID, stillExists.ID)
	})
}

