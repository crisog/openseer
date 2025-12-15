package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	openseerv1connect "github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/crisog/openseer/tests/helpers"
)

func TestWorkerLifecycleLeasesAndAcks(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentSrv := env.StartEnrollmentServer(t)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(http.DefaultClient, enrollmentSrv.URL)

	enrollResp, err := enrollmentClient.EnrollWorker(ctx, connect.NewRequest(&openseerv1.EnrollWorkerRequest{
		EnrollmentToken: env.ClusterToken,
		Hostname:        "test-worker",
		WorkerVersion:   "1.0.0",
		Region:          "us-east-1",
	}))
	require.NoError(t, err)
	require.NotEmpty(t, enrollResp.Msg.WorkerId)
	require.NotEmpty(t, enrollResp.Msg.ApiToken)
	require.True(t, enrollResp.Msg.Accepted)

	workerSrv := env.StartWorkerServer(t)
	workerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  5000,
	})
	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	getJobsReq := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 1})
	getJobsReq.Header().Set("Authorization", "Bearer "+enrollResp.Msg.ApiToken)

	getJobsResp, err := workerClient.GetJobs(ctx, getJobsReq)
	require.NoError(t, err)
	require.Len(t, getJobsResp.Msg.Jobs, 1)

	leasedJob := getJobsResp.Msg.Jobs[0]
	require.Equal(t, job.RunID, leasedJob.RunId)
	require.Equal(t, monitor.ID, leasedJob.MonitorId)

	code := int32(200)
	result := &openseerv1.MonitorResult{
		RunId:     leasedJob.RunId,
		MonitorId: leasedJob.MonitorId,
		Region:    "us-east-1",
		Status:    "OK",
		EventAt:   timestamppb.Now(),
		HttpCode:  &code,
	}

	submitReq := connect.NewRequest(&openseerv1.SubmitResultRequest{Result: result})
	submitReq.Header().Set("Authorization", "Bearer "+enrollResp.Msg.ApiToken)

	submitResp, err := workerClient.SubmitResult(ctx, submitReq)
	require.NoError(t, err)
	require.True(t, submitResp.Msg.Committed)
	require.Equal(t, leasedJob.RunId, submitResp.Msg.RunId)

	jobRecord, err := env.Queries.GetJobByRunID(context.Background(), job.RunID)
	require.NoError(t, err)
	require.Equal(t, "done", jobRecord.Status)
	require.True(t, jobRecord.WorkerID.Valid)
	require.Equal(t, enrollResp.Msg.WorkerId, jobRecord.WorkerID.String)

	results, err := env.Queries.GetRecentResults(context.Background(), &sqlc.GetRecentResultsParams{MonitorID: monitor.ID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, leasedJob.RunId, results[0].RunID)
	require.Equal(t, "OK", results[0].Status)
}

func TestSchedulerCreatesJobsForDueMonitor(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1", "eu-west-1"},
		IntervalMs: 1000,
		TimeoutMs:  1000,
	})

	require.Eventually(t, func() bool {
		var count int
		err := env.TestDB.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM app.jobs WHERE monitor_id = $1", monitor.ID).Scan(&count)
		require.NoError(t, err)
		return count == len(monitor.Regions)
	}, 5*time.Second, 100*time.Millisecond)

	require.Eventually(t, func() bool {
		exists, err := env.Queries.CheckJobExists(context.Background(), &sqlc.CheckJobExistsParams{
			MonitorID:     monitor.ID,
			ScheduledAt:   time.Now().Add(-1 * time.Minute),
			ScheduledAt_2: time.Now().Add(1 * time.Minute),
		})
		require.NoError(t, err)
		return exists
	}, 5*time.Second, 100*time.Millisecond)

	updatedMonitor, err := env.Queries.GetMonitor(context.Background(), monitor.ID)
	require.NoError(t, err)
	require.True(t, updatedMonitor.LastScheduledAt.Valid, "scheduler should set last_scheduled_at")
	require.True(t, updatedMonitor.NextDueAt.Valid, "scheduler should set next_due_at")
	require.True(t, updatedMonitor.NextDueAt.Time.After(updatedMonitor.LastScheduledAt.Time))
}

func enrollWorkerForTest(t *testing.T, env *helpers.ControlPlaneTestEnvironment, enrollmentClient openseerv1connect.EnrollmentServiceClient, hostname, region string) (string, string) {
	resp, err := enrollmentClient.EnrollWorker(context.Background(), connect.NewRequest(&openseerv1.EnrollWorkerRequest{
		EnrollmentToken: env.ClusterToken,
		Hostname:        hostname,
		WorkerVersion:   "1.0.0",
		Region:          region,
	}))
	require.NoError(t, err)
	require.True(t, resp.Msg.Accepted)

	return resp.Msg.WorkerId, resp.Msg.ApiToken
}

func TestEnrollmentRenewalAndRevocation(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentSrv := env.StartEnrollmentServer(t)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(http.DefaultClient, enrollmentSrv.URL)

	enrollResp, err := enrollmentClient.EnrollWorker(ctx, connect.NewRequest(&openseerv1.EnrollWorkerRequest{
		EnrollmentToken: env.ClusterToken,
		Hostname:        "renewal-host",
		WorkerVersion:   "1.2.3",
		Region:          "us-east-1",
	}))
	require.NoError(t, err)
	workerID := enrollResp.Msg.WorkerId
	require.NotEmpty(t, workerID)
	require.NotEmpty(t, enrollResp.Msg.ApiToken)

	workerBefore, err := env.Queries.GetWorkerByID(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, "enrolled", workerBefore.Status)

	renewResp, err := enrollmentClient.RenewEnrollment(ctx, connect.NewRequest(&openseerv1.RenewEnrollmentRequest{
		WorkerId: workerID,
	}))
	require.NoError(t, err)
	require.True(t, renewResp.Msg.Renewed)
	require.NotEmpty(t, renewResp.Msg.ApiToken)
	require.NotEqual(t, enrollResp.Msg.ApiToken, renewResp.Msg.ApiToken)

	revokeReason := "rotation complete"
	revokeResp, err := enrollmentClient.RevokeEnrollment(ctx, connect.NewRequest(&openseerv1.RevokeEnrollmentRequest{
		WorkerId: workerID,
		Reason:   revokeReason,
	}))
	require.NoError(t, err)
	require.True(t, revokeResp.Msg.Revoked)

	workerRevoked, err := env.Queries.GetWorkerByID(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, "revoked", workerRevoked.Status)
	require.True(t, workerRevoked.RevokedReason.Valid)
	require.Equal(t, revokeReason, workerRevoked.RevokedReason.String)

	workerSrv := env.StartWorkerServer(t)
	workerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	getJobsReq := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 1})
	getJobsReq.Header().Set("Authorization", "Bearer "+renewResp.Msg.ApiToken)

	_, err = workerClient.GetJobs(ctx, getJobsReq)
	require.Error(t, err)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestLeaseReaperReclaimsExpiredLeases(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	enrollmentSrv := env.StartEnrollmentServer(t)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(http.DefaultClient, enrollmentSrv.URL)
	workerID, apiToken := enrollWorkerForTest(t, env, enrollmentClient, "health-worker", "us-east-1")

	workerSrv := env.StartWorkerServer(t)
	workerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  2000,
	})

	for i := 0; i < 3; i++ {
		helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")
	}

	getJobsReq := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 3})
	getJobsReq.Header().Set("Authorization", "Bearer "+apiToken)

	getJobsResp, err := workerClient.GetJobs(ctx, getJobsReq)
	require.NoError(t, err)
	require.Len(t, getJobsResp.Msg.Jobs, 3)

	expiredRunID := getJobsResp.Msg.Jobs[0].RunId

	_, err = env.TestDB.DB.ExecContext(ctx, "UPDATE app.jobs SET lease_expires_at = NOW() - INTERVAL '1 second' WHERE run_id = $1", expiredRunID)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		job, err := env.Queries.GetJobByRunID(ctx, expiredRunID)
		require.NoError(t, err)
		return job.Status == "ready" && !job.WorkerID.Valid
	}, 6*time.Second, 150*time.Millisecond)

	_, err = env.TestDB.DB.ExecContext(context.Background(), "UPDATE app.workers SET last_seen_at = NOW() - INTERVAL '5 minutes' WHERE id = $1", workerID)
	require.NoError(t, err)

	helpers.WaitForWorkerInactivity(t, env.Queries, workerID, 15*time.Second)
}

func TestConcurrentWorkersLeaseDistinctJobs(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentSrv := env.StartEnrollmentServer(t)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(http.DefaultClient, enrollmentSrv.URL)
	worker1ID, worker1Token := enrollWorkerForTest(t, env, enrollmentClient, "worker-a", "us-east-1")
	worker2ID, worker2Token := enrollWorkerForTest(t, env, enrollmentClient, "worker-b", "us-east-1")

	workerSrv := env.StartWorkerServer(t)
	workerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  2000,
	})

	jobs := make([]*sqlc.AppJob, 0, 2)
	for i := 0; i < 2; i++ {
		jobs = append(jobs, helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1"))
	}

	req1 := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 1})
	req1.Header().Set("Authorization", "Bearer "+worker1Token)

	req2 := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 1})
	req2.Header().Set("Authorization", "Bearer "+worker2Token)

	resp1, err := workerClient.GetJobs(ctx, req1)
	require.NoError(t, err)
	require.Len(t, resp1.Msg.Jobs, 1)

	resp2, err := workerClient.GetJobs(ctx, req2)
	require.NoError(t, err)
	require.Len(t, resp2.Msg.Jobs, 1)

	require.NotEqual(t, resp1.Msg.Jobs[0].RunId, resp2.Msg.Jobs[0].RunId)

	created := map[string]struct{}{jobs[0].RunID: {}, jobs[1].RunID: {}}
	_, ok := created[resp1.Msg.Jobs[0].RunId]
	require.True(t, ok)
	_, ok = created[resp2.Msg.Jobs[0].RunId]
	require.True(t, ok)

	jobRecord1, err := env.Queries.GetJobByRunID(ctx, resp1.Msg.Jobs[0].RunId)
	require.NoError(t, err)
	require.True(t, jobRecord1.WorkerID.Valid)
	require.Equal(t, worker1ID, jobRecord1.WorkerID.String)

	jobRecord2, err := env.Queries.GetJobByRunID(ctx, resp2.Msg.Jobs[0].RunId)
	require.NoError(t, err)
	require.True(t, jobRecord2.WorkerID.Valid)
	require.Equal(t, worker2ID, jobRecord2.WorkerID.String)
}

func TestWorkerLeaseRenewalExtendsExpiration(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentSrv := env.StartEnrollmentServer(t)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(http.DefaultClient, enrollmentSrv.URL)
	workerID, apiToken := enrollWorkerForTest(t, env, enrollmentClient, "lease-renewal-worker", "us-east-1")

	workerSrv := env.StartWorkerServer(t)
	workerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  10000,
	})
	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	getJobsReq := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 1})
	getJobsReq.Header().Set("Authorization", "Bearer "+apiToken)

	getJobsResp, err := workerClient.GetJobs(ctx, getJobsReq)
	require.NoError(t, err)
	require.Len(t, getJobsResp.Msg.Jobs, 1)

	leasedJob := getJobsResp.Msg.Jobs[0]
	require.Equal(t, job.RunID, leasedJob.RunId)

	jobRecord, err := env.Queries.GetJobByRunID(ctx, leasedJob.RunId)
	require.NoError(t, err)
	require.Equal(t, "leased", jobRecord.Status)
	require.True(t, jobRecord.LeaseExpiresAt.Valid)
	initialLeaseExpiry := jobRecord.LeaseExpiresAt.Time

	time.Sleep(2 * time.Second)

	renewReq := connect.NewRequest(&openseerv1.RenewLeaseRequest{RunId: leasedJob.RunId})
	renewReq.Header().Set("Authorization", "Bearer "+apiToken)

	renewResp, err := workerClient.RenewLease(ctx, renewReq)
	require.NoError(t, err)
	require.True(t, renewResp.Msg.Renewed)

	require.Eventually(t, func() bool {
		jobRecord, err := env.Queries.GetJobByRunID(ctx, leasedJob.RunId)
		require.NoError(t, err)
		if !jobRecord.LeaseExpiresAt.Valid {
			return false
		}
		return jobRecord.LeaseExpiresAt.Time.After(initialLeaseExpiry)
	}, 5*time.Second, 100*time.Millisecond, "lease expiry should be extended")

	updatedJob, err := env.Queries.GetJobByRunID(ctx, leasedJob.RunId)
	require.NoError(t, err)
	require.True(t, updatedJob.LeaseExpiresAt.Valid)
	require.Greater(t, updatedJob.LeaseExpiresAt.Time.Unix(), initialLeaseExpiry.Unix())
	require.Equal(t, "leased", updatedJob.Status)
	require.True(t, updatedJob.WorkerID.Valid)
	require.Equal(t, workerID, updatedJob.WorkerID.String)

	code := int32(200)
	submitReq := connect.NewRequest(&openseerv1.SubmitResultRequest{
		Result: &openseerv1.MonitorResult{
			RunId:     leasedJob.RunId,
			MonitorId: leasedJob.MonitorId,
			Region:    "us-east-1",
			Status:    "OK",
			EventAt:   timestamppb.Now(),
			HttpCode:  &code,
		},
	})
	submitReq.Header().Set("Authorization", "Bearer "+apiToken)

	submitResp, err := workerClient.SubmitResult(ctx, submitReq)
	require.NoError(t, err)
	require.True(t, submitResp.Msg.Committed)

	finalJob, err := env.Queries.GetJobByRunID(ctx, leasedJob.RunId)
	require.NoError(t, err)
	require.Equal(t, "done", finalJob.Status)
}

func TestMonitorSoftDeletes(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)

	ctx := context.Background()

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 5000,
		TimeoutMs:  2000,
	})

	dueBefore, err := env.Queries.ListDueMonitors(ctx, sql.NullTime{Time: time.Now().Add(1 * time.Minute), Valid: true})
	require.NoError(t, err)
	foundMonitor := false
	for _, m := range dueBefore {
		if m.ID == monitor.ID {
			foundMonitor = true
			break
		}
	}
	require.True(t, foundMonitor, "monitor should be returned before soft delete")

	_ = helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")
	_ = helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	jobsBefore, err := env.Queries.GetJobsForMonitor(ctx, monitor.ID)
	require.NoError(t, err)
	require.Len(t, jobsBefore, 2, "expected two active jobs before soft delete")

	require.NoError(t, env.Queries.DeleteMonitor(ctx, monitor.ID))
	require.NoError(t, env.Queries.DeleteMonitorJobs(ctx, monitor.ID))

	_, err = env.Queries.GetMonitor(ctx, monitor.ID)
	require.ErrorIs(t, err, sql.ErrNoRows, "soft deleted monitor should not be returned by GetMonitor")

	deletedMonitor, err := env.Queries.GetMonitorIncludingDeleted(ctx, monitor.ID)
	require.NoError(t, err)
	require.True(t, deletedMonitor.DeletedAt.Valid, "deleted monitor should have deleted_at set")

	activeCount, err := env.Queries.CountActiveMonitorsByID(ctx, monitor.ID)
	require.NoError(t, err)
	require.Zero(t, activeCount, "monitor should be excluded from active view after soft delete")

	dueAfter, err := env.Queries.ListDueMonitors(ctx, sql.NullTime{Time: time.Now().Add(1 * time.Minute), Valid: true})
	require.NoError(t, err)
	for _, m := range dueAfter {
		require.NotEqual(t, monitor.ID, m.ID, "soft deleted monitor should not appear in due monitors")
	}

	jobsAfter, err := env.Queries.GetJobsForMonitor(ctx, monitor.ID)
	require.NoError(t, err)
	require.Empty(t, jobsAfter, "soft deleted jobs should be excluded from active job query")

	deletedJobCount, err := env.Queries.CountDeletedJobsForMonitor(ctx, monitor.ID)
	require.NoError(t, err)
	require.EqualValues(t, len(jobsBefore), deletedJobCount, "all jobs should be soft deleted")
}

func TestResultIdempotency(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 5000,
		TimeoutMs:  1000,
	})

	jobID := fmt.Sprintf("%s-us-east-1-%s-%s", monitor.ID, time.Now().Format("20060102150405"), generateRandomID(8))
	_, err := env.Queries.CreateJob(ctx, &sqlc.CreateJobParams{
		RunID:       jobID,
		MonitorID:   monitor.ID,
		Region:      "us-east-1",
		ScheduledAt: time.Now(),
	})
	require.NoError(t, err)

	httpCode := int32(200)
	totalMs := int32(150)
	eventTime := timestamppb.Now()
	result := &openseerv1.MonitorResult{
		RunId:     jobID,
		MonitorId: monitor.ID,
		Region:    "us-east-1",
		Status:    "OK",
		EventAt:   eventTime,
		HttpCode:  &httpCode,
		TotalMs:   &totalMs,
	}

	err = env.Ingest.ProcessResult(ctx, result)
	require.NoError(t, err, "first result submission should succeed")

	countResult, err := env.Queries.CountResultsByRunID(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, int64(1), countResult, "should have exactly one result")

	err = env.Ingest.ProcessResult(ctx, result)
	require.NoError(t, err, "second result submission should succeed (idempotent)")

	countResult, err = env.Queries.CountResultsByRunID(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, int64(1), countResult, "should still have exactly one result after duplicate submission")
}

func generateRandomID(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}

func TestIngestFailureResultPersistsFields(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentSrv := env.StartEnrollmentServer(t)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(http.DefaultClient, enrollmentSrv.URL)
	workerID, apiToken := enrollWorkerForTest(t, env, enrollmentClient, "ingest-worker", "us-east-1")

	workerSrv := env.StartWorkerServer(t)
	workerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  2000,
	})
	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	getJobsReq := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 1})
	getJobsReq.Header().Set("Authorization", "Bearer "+apiToken)

	getJobsResp, err := workerClient.GetJobs(ctx, getJobsReq)
	require.NoError(t, err)
	require.Len(t, getJobsResp.Msg.Jobs, 1)
	leasedJob := getJobsResp.Msg.Jobs[0]
	require.Equal(t, job.RunID, leasedJob.RunId)

	errMsg := "http timeout"
	status := "FAIL"
	failingCode := int32(504)
	sizeBytes := int64(4096)
	totalMs := int32(12000)

	submitReq := connect.NewRequest(&openseerv1.SubmitResultRequest{
		Result: &openseerv1.MonitorResult{
			RunId:        leasedJob.RunId,
			MonitorId:    leasedJob.MonitorId,
			Region:       "us-east-1",
			Status:       status,
			EventAt:      timestamppb.Now(),
			HttpCode:     &failingCode,
			SizeBytes:    &sizeBytes,
			TotalMs:      &totalMs,
			ErrorMessage: &errMsg,
		},
	})
	submitReq.Header().Set("Authorization", "Bearer "+apiToken)

	submitResp, err := workerClient.SubmitResult(ctx, submitReq)
	require.NoError(t, err)
	require.True(t, submitResp.Msg.Committed)
	require.Equal(t, leasedJob.RunId, submitResp.Msg.RunId)

	jobRecord, err := env.Queries.GetJobByRunID(ctx, leasedJob.RunId)
	require.NoError(t, err)
	require.Equal(t, "done", jobRecord.Status)
	require.True(t, jobRecord.WorkerID.Valid)
	require.Equal(t, workerID, jobRecord.WorkerID.String)

	results, err := env.Queries.GetRecentResults(ctx, &sqlc.GetRecentResultsParams{MonitorID: monitor.ID, Limit: 1})
	require.NoError(t, err)
	require.Len(t, results, 1)
	stored := results[0]
	require.Equal(t, status, stored.Status)
	require.True(t, stored.ErrorMessage.Valid)
	require.Equal(t, errMsg, stored.ErrorMessage.String)
	require.True(t, stored.TotalMs.Valid)
	require.Equal(t, totalMs, stored.TotalMs.Int32)
	require.True(t, stored.SizeBytes.Valid)
	require.Equal(t, sizeBytes, stored.SizeBytes.Int64)
	require.True(t, stored.HttpCode.Valid)
	require.Equal(t, failingCode, stored.HttpCode.Int32)
}

func TestEnrollmentStatusTransitions(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentSrv := env.StartEnrollmentServer(t)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(http.DefaultClient, enrollmentSrv.URL)

	enrollResp, err := enrollmentClient.EnrollWorker(ctx, connect.NewRequest(&openseerv1.EnrollWorkerRequest{
		EnrollmentToken: env.ClusterToken,
		Hostname:        "status-worker",
		WorkerVersion:   "1.0.0",
		Region:          "us-east-1",
	}))
	require.NoError(t, err)
	workerID := enrollResp.Msg.WorkerId
	apiToken := enrollResp.Msg.ApiToken

	workerInitial, err := env.Queries.GetWorkerByID(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, "enrolled", workerInitial.Status)

	workerSrv := env.StartWorkerServer(t)
	workerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	getJobsReq := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 1})
	getJobsReq.Header().Set("Authorization", "Bearer "+apiToken)

	_, err = workerClient.GetJobs(ctx, getJobsReq)
	require.NoError(t, err)

	workerAfterRegister, err := env.Queries.GetWorkerByID(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, "active", workerAfterRegister.Status)

	_, err = env.TestDB.DB.ExecContext(ctx, "UPDATE app.workers SET last_seen_at = NOW() - INTERVAL '5 minutes' WHERE id = $1", workerID)
	require.NoError(t, err)

	helpers.WaitForWorkerInactivity(t, env.Queries, workerID, 15*time.Second)

	_, err = enrollmentClient.RevokeEnrollment(ctx, connect.NewRequest(&openseerv1.RevokeEnrollmentRequest{
		WorkerId: workerID,
		Reason:   "test revocation",
	}))
	require.NoError(t, err)

	workerRevoked, err := env.Queries.GetWorkerByID(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, "revoked", workerRevoked.Status)

	getJobsReqAfterRevoke := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 1})
	getJobsReqAfterRevoke.Header().Set("Authorization", "Bearer "+apiToken)

	_, err = workerClient.GetJobs(ctx, getJobsReqAfterRevoke)
	require.Error(t, err, "revoked worker should not be able to connect")
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
}

func TestSchedulerJitterCalculation(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	monitor5s := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 5000,
		TimeoutMs:  1000,
	})

	monitor15s := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 15000,
		TimeoutMs:  1000,
	})

	monitor60s := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  1000,
	})

	require.Eventually(t, func() bool {
		count5s, err := countJobsForMonitor(ctx, env, monitor5s.ID)
		require.NoError(t, err)
		count15s, err := countJobsForMonitor(ctx, env, monitor15s.ID)
		require.NoError(t, err)
		count60s, err := countJobsForMonitor(ctx, env, monitor60s.ID)
		require.NoError(t, err)
		return count5s > 0 && count15s > 0 && count60s > 0
	}, 10*time.Second, 100*time.Millisecond, "waiting for initial jobs")

	time.Sleep(8 * time.Second)

	jobs5s, err := getJobsForMonitor(ctx, env, monitor5s.ID, 3)
	require.NoError(t, err)
	if len(jobs5s) >= 2 {
		for i := 1; i < len(jobs5s); i++ {
			interval := jobs5s[i-1].ScheduledAt.Sub(jobs5s[i].ScheduledAt)
			require.InDelta(t, 5000, interval.Milliseconds(), 100,
				"5s monitor should have no jitter, got interval: %v", interval)
		}
	}

	jobs15s, err := getJobsForMonitor(ctx, env, monitor15s.ID, 3)
	require.NoError(t, err)
	if len(jobs15s) >= 2 {
		for i := 1; i < len(jobs15s); i++ {
			interval := jobs15s[i-1].ScheduledAt.Sub(jobs15s[i].ScheduledAt)
			deviation := abs(interval.Milliseconds() - 15000)
			maxJitter := int64(150)
			require.LessOrEqual(t, deviation, maxJitter,
				"15s monitor jitter exceeded 1%%, got interval: %v (deviation: %dms)", interval, deviation)
		}
	}

	jobs60s, err := getJobsForMonitor(ctx, env, monitor60s.ID, 2)
	require.NoError(t, err)
	if len(jobs60s) >= 2 {
		for i := 1; i < len(jobs60s); i++ {
			interval := jobs60s[i-1].ScheduledAt.Sub(jobs60s[i].ScheduledAt)
			deviation := abs(interval.Milliseconds() - 60000)
			maxJitter := int64(6000)
			require.LessOrEqual(t, deviation, maxJitter,
				"60s monitor jitter exceeded 10%%, got interval: %v (deviation: %dms)", interval, deviation)
		}
	}
}

func countJobsForMonitor(ctx context.Context, env *helpers.ControlPlaneTestEnvironment, monitorID string) (int, error) {
	count, err := env.Queries.CountJobsForMonitor(ctx, monitorID)
	return int(count), err
}

func getJobsForMonitor(ctx context.Context, env *helpers.ControlPlaneTestEnvironment, monitorID string, limit int) ([]*sqlc.AppJob, error) {
	jobs, err := env.Queries.GetJobsForMonitor(ctx, monitorID)
	if err != nil {
		return nil, err
	}

	if limit > 0 && len(jobs) > limit {
		jobs = jobs[:limit]
	}

	return jobs, nil
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func TestDuplicateJobPrevention(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  500,
	})

	require.Eventually(t, func() bool {
		count, err := countJobsForMonitor(ctx, env, monitor.ID)
		require.NoError(t, err)
		return count == 1
	}, 10*time.Second, 100*time.Millisecond, "waiting for initial job")

	initialCount, err := countJobsForMonitor(ctx, env, monitor.ID)
	require.NoError(t, err)
	require.Equal(t, 1, initialCount)

	runID := fmt.Sprintf("%s-us-east-1-%s-%s", monitor.ID, time.Now().Format("20060102150405"), generateRandomID(8))
	now := time.Now()

	_, err = env.Queries.CreateJobIdempotent(ctx, &sqlc.CreateJobIdempotentParams{
		RunID:         runID,
		MonitorID:     monitor.ID,
		Region:        "us-east-1",
		ScheduledAt:   now,
		ScheduledAt_2: now.Add(-2 * time.Minute),
		ScheduledAt_3: now.Add(2 * time.Minute),
	})

	require.Error(t, err, "should return error when duplicate job prevented")
	require.Equal(t, sql.ErrNoRows, err, "should return ErrNoRows when no job created")

	finalCount, err := countJobsForMonitor(ctx, env, monitor.ID)
	require.NoError(t, err)
	require.Equal(t, 1, finalCount, "should still have only one job after duplicate attempt")

	time.Sleep(3 * time.Second)

	afterSchedulerCount, err := countJobsForMonitor(ctx, env, monitor.ID)
	require.NoError(t, err)
	require.Equal(t, 1, afterSchedulerCount, "scheduler should not create duplicates within time window despite multiple runs")
}

func TestRegionalJobDistribution(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	usMonitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	euMonitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"eu-west-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	globalMonitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"global"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	time.Sleep(100 * time.Millisecond)

	_ = helpers.CreateTestJob(t, env.Queries, usMonitor.ID, "us-east-1")
	_ = helpers.CreateTestJob(t, env.Queries, euMonitor.ID, "eu-west-1")
	_ = helpers.CreateTestJob(t, env.Queries, globalMonitor.ID, "global")

	enrollmentSrv := env.StartEnrollmentServer(t)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(http.DefaultClient, enrollmentSrv.URL)
	usWorkerID, usApiToken := enrollWorkerForTest(t, env, enrollmentClient, "us-worker", "us-east-1")

	workerSrv := env.StartWorkerServer(t)
	usWorkerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	getJobsReq := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 5})
	getJobsReq.Header().Set("Authorization", "Bearer "+usApiToken)

	getJobsResp, err := usWorkerClient.GetJobs(ctx, getJobsReq)
	require.NoError(t, err)

	usReceivedJobs := make(map[string]bool)
	for _, job := range getJobsResp.Msg.Jobs {
		usReceivedJobs[job.RunId] = true

		code := int32(200)
		submitReq := connect.NewRequest(&openseerv1.SubmitResultRequest{
			Result: &openseerv1.MonitorResult{
				RunId:     job.RunId,
				MonitorId: job.MonitorId,
				Region:    "us-east-1",
				Status:    "OK",
				EventAt:   timestamppb.Now(),
				HttpCode:  &code,
			},
		})
		submitReq.Header().Set("Authorization", "Bearer "+usApiToken)
		_, err := usWorkerClient.SubmitResult(ctx, submitReq)
		require.NoError(t, err)
	}

	require.Greater(t, len(usReceivedJobs), 0, "US worker should receive at least one job")

	for jobID := range usReceivedJobs {
		job, err := env.Queries.GetJobByRunID(ctx, jobID)
		require.NoError(t, err, "Failed to get job %s", jobID)

		require.True(t, job.Region == "us-east-1" || job.Region == "global",
			"US worker received job for wrong region: %s (job region: %s)", jobID, job.Region)
	}

	euWorkerID, euApiToken := enrollWorkerForTest(t, env, enrollmentClient, "eu-worker", "eu-west-1")
	euWorkerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	euGetJobsReq := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 5})
	euGetJobsReq.Header().Set("Authorization", "Bearer "+euApiToken)

	euGetJobsResp, err := euWorkerClient.GetJobs(ctx, euGetJobsReq)
	require.NoError(t, err)

	euReceivedJobs := make(map[string]bool)
	for _, job := range euGetJobsResp.Msg.Jobs {
		euReceivedJobs[job.RunId] = true
	}

	for jobID := range euReceivedJobs {
		job, err := env.Queries.GetJobByRunID(ctx, jobID)
		require.NoError(t, err, "Failed to get job %s", jobID)

		require.True(t, job.Region == "eu-west-1" || job.Region == "global",
			"EU worker received job for wrong region: %s (job region: %s)", jobID, job.Region)
	}

	totalJobsProcessed := len(usReceivedJobs) + len(euReceivedJobs)
	require.Greater(t, totalJobsProcessed, 0, "At least some jobs should have been processed by workers")

	t.Logf("Test completed successfully - US worker received %d jobs, EU worker received %d jobs",
		len(usReceivedJobs), len(euReceivedJobs))

	_ = usWorkerID
	_ = euWorkerID
}

func TestLeaseReaperBatchReclaim(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	worker := helpers.CreateTestWorker(t, env.Queries, "us-east-1")

	jobs := []*sqlc.AppJob{
		helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1"),
		helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1"),
		helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1"),
	}

	leasedJobs, err := env.Queries.LeaseJobs(ctx, &sqlc.LeaseJobsParams{
		WorkerID:       sql.NullString{String: worker.ID, Valid: true},
		Limit:          int32(len(jobs)),
		Region:         "us-east-1",
		LeaseExpiresAt: sql.NullTime{Time: time.Now().Add(10 * time.Minute), Valid: true},
	})
	require.NoError(t, err)
	require.Len(t, leasedJobs, len(jobs))

	expiredIDs := []string{leasedJobs[0].RunID, leasedJobs[1].RunID}
	activeID := leasedJobs[2].RunID

	for _, runID := range expiredIDs {
		require.NoError(t, env.Queries.ForceExpireJobLease(ctx, &sqlc.ForceExpireJobLeaseParams{
			RunID: runID,
			LeaseExpiresAt: sql.NullTime{
				Time:  time.Now().Add(-1 * time.Minute),
				Valid: true,
			},
		}))
	}

	require.Eventually(t, func() bool {
		for _, runID := range expiredIDs {
			jobRecord, err := env.Queries.GetJobByRunID(ctx, runID)
			if err != nil {
				t.Logf("error fetching job %s: %v", runID, err)
				return false
			}
			if jobRecord.Status != "ready" || jobRecord.LeaseExpiresAt.Valid || jobRecord.WorkerID.Valid {
				return false
			}
		}

		activeJob, err := env.Queries.GetJobByRunID(ctx, activeID)
		if err != nil {
			t.Logf("error fetching active job %s: %v", activeID, err)
			return false
		}
		if activeJob.Status != "leased" || !activeJob.LeaseExpiresAt.Valid || !activeJob.WorkerID.Valid {
			return false
		}

		return true
	}, 5*time.Second, 100*time.Millisecond)
}

func TestAdvisoryLockSchedulerElection(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)

	ctx := context.Background()

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	const schedulerLockID = 12345
	conn, err := env.TestDB.DB.Conn(ctx)
	require.NoError(t, err)
	defer conn.Close()

	_, err = conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", schedulerLockID)
	require.NoError(t, err)
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", schedulerLockID)

	schedulerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		env.Scheduler.Start(schedulerCtx)
		close(done)
	}()

	require.Never(t, func() bool {
		count, err := env.Queries.CountJobsForMonitor(ctx, monitor.ID)
		require.NoError(t, err)
		return count > 0
	}, 1*time.Second, 100*time.Millisecond, "scheduler should not create jobs while lock held elsewhere")

	_, err = conn.ExecContext(ctx, "SELECT pg_advisory_unlock($1)", schedulerLockID)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		count, err := env.Queries.CountJobsForMonitor(ctx, monitor.ID)
		if err != nil {
			t.Logf("failed to count jobs: %v", err)
			return false
		}
		return count == int64(len(monitor.Regions))
	}, 5*time.Second, 100*time.Millisecond, "scheduler should take lock and create jobs once available")

	cancel()
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 2*time.Second, 100*time.Millisecond)
}

func TestInvalidWorkerOperations(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 60000,
		TimeoutMs:  5000,
	})

	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	enrollmentSrv := env.StartEnrollmentServer(t)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(http.DefaultClient, enrollmentSrv.URL)
	worker1ID, worker1Token := enrollWorkerForTest(t, env, enrollmentClient, "worker-1", "us-east-1")
	_, worker2Token := enrollWorkerForTest(t, env, enrollmentClient, "worker-2", "us-east-1")

	workerSrv := env.StartWorkerServer(t)
	workerClient := openseerv1connect.NewWorkerServiceClient(http.DefaultClient, workerSrv.URL)

	getJobsReq := connect.NewRequest(&openseerv1.GetJobsRequest{MaxJobs: 1})
	getJobsReq.Header().Set("Authorization", "Bearer "+worker1Token)

	getJobsResp, err := workerClient.GetJobs(ctx, getJobsReq)
	require.NoError(t, err)
	require.Len(t, getJobsResp.Msg.Jobs, 1)
	assignedJob := getJobsResp.Msg.Jobs[0]
	require.Equal(t, job.RunID, assignedJob.RunId)

	wrongWorkerSubmitReq := connect.NewRequest(&openseerv1.SubmitResultRequest{
		Result: &openseerv1.MonitorResult{
			RunId:     assignedJob.RunId,
			MonitorId: assignedJob.MonitorId,
			Region:    "us-east-1",
			Status:    "OK",
			EventAt:   timestamppb.Now(),
		},
	})
	wrongWorkerSubmitReq.Header().Set("Authorization", "Bearer "+worker2Token)

	wrongResp, err := workerClient.SubmitResult(ctx, wrongWorkerSubmitReq)
	require.NoError(t, err)
	require.False(t, wrongResp.Msg.Committed, "Result from wrong worker should not be committed")

	wrongWorkerRenewReq := connect.NewRequest(&openseerv1.RenewLeaseRequest{RunId: assignedJob.RunId})
	wrongWorkerRenewReq.Header().Set("Authorization", "Bearer "+worker2Token)

	renewResp, err := workerClient.RenewLease(ctx, wrongWorkerRenewReq)
	require.NoError(t, err)
	require.False(t, renewResp.Msg.Renewed, "Wrong worker should not be able to renew lease")

	correctSubmitReq := connect.NewRequest(&openseerv1.SubmitResultRequest{
		Result: &openseerv1.MonitorResult{
			RunId:     assignedJob.RunId,
			MonitorId: assignedJob.MonitorId,
			Region:    "us-east-1",
			Status:    "OK",
			EventAt:   timestamppb.Now(),
		},
	})
	correctSubmitReq.Header().Set("Authorization", "Bearer "+worker1Token)

	correctResp, err := workerClient.SubmitResult(ctx, correctSubmitReq)
	require.NoError(t, err)
	require.True(t, correctResp.Msg.Committed, "Result from correct worker should be committed")

	completedJobs, err := env.Queries.GetCompletedJobsByMonitor(ctx, monitor.ID)
	require.NoError(t, err)
	require.Len(t, completedJobs, 1, "Job should be completed exactly once")
	require.Equal(t, "done", completedJobs[0].Status)
	require.Equal(t, worker1ID, completedJobs[0].WorkerID.String)
}

func TestSchedulerHighFrequencyMonitor(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx := context.Background()

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  500,
	})

	require.Eventually(t, func() bool {
		count, err := env.Queries.CountJobsForMonitor(ctx, monitor.ID)
		if err != nil {
			return false
		}
		return count >= 1
	}, 3*time.Second, 100*time.Millisecond, "first job should be created")

	_, err := env.TestDB.DB.ExecContext(ctx, `
		UPDATE app.jobs SET status = 'done' WHERE monitor_id = $1
	`, monitor.ID)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		count, err := env.Queries.CountJobsForMonitor(ctx, monitor.ID)
		if err != nil {
			return false
		}
		return count >= 2
	}, 3*time.Second, 100*time.Millisecond, "second job should be created after first completes")

	_, err = env.TestDB.DB.ExecContext(ctx, `
		UPDATE app.jobs SET status = 'done' WHERE monitor_id = $1
	`, monitor.ID)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		count, err := env.Queries.CountJobsForMonitor(ctx, monitor.ID)
		if err != nil {
			return false
		}
		return count >= 3
	}, 3*time.Second, 100*time.Millisecond, "third job should be created - proves 1s interval works")

	jobs, err := env.Queries.GetJobsForMonitor(ctx, monitor.ID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(jobs), 3, "should have at least 3 jobs for 1-second interval monitor")

	for i := 1; i < len(jobs); i++ {
		timeDiff := jobs[i-1].ScheduledAt.Sub(jobs[i].ScheduledAt)
		require.InDelta(t, float64(1*time.Second), float64(timeDiff), float64(500*time.Millisecond),
			"jobs should be scheduled ~1 second apart (got %v)", timeDiff)
	}
}
