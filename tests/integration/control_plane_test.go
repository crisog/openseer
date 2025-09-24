package integration_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	openseerv1connect "github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	"github.com/crisog/openseer/internal/app/control-plane/store/sqlc"
	"github.com/crisog/openseer/internal/pkg/auth"
	"github.com/crisog/openseer/tests/helpers"
)

func TestWorkerLifecycleLeasesAndAcks(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))

	enrollmentHTTPClient := helpers.NewHTTP2Client(tlsConfig(t, caPool), 5*time.Second)
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(enrollmentHTTPClient, enrollmentSrv.URL)

	csr, workerKey := helpers.MustGenerateCSR(t, "pending-worker")
	enrollReq := connect.NewRequest(&openseerv1.EnrollWorkerRequest{
		EnrollmentToken: env.ClusterToken,
		Hostname:        "test-worker",
		WorkerVersion:   "1.0.0",
		Region:          "us-east-1",
		CsrPem:          csr,
	})

	enrollResp, err := enrollmentClient.EnrollWorker(ctx, enrollReq)
	require.NoError(t, err)
	require.NotEmpty(t, enrollResp.Msg.WorkerId)
	require.NotEmpty(t, enrollResp.Msg.Certificate)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(workerKey)})
	workerTLS, err := auth.CreateClientTLSConfig([]byte(enrollResp.Msg.Certificate), keyPEM, env.PKI.GetCACertPEM(), "localhost")
	require.NoError(t, err)

	workerHTTPClient := helpers.NewHTTP2Client(workerTLS, 10*time.Second)
	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	workerClient := openseerv1connect.NewWorkerServiceClient(workerHTTPClient, workerSrv.URL)

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer streamCancel()
	stream := workerClient.WorkerStream(streamCtx)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  5000,
	})
	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))

	serverMsg, err := stream.Receive()
	require.NoError(t, err)
	registered := serverMsg.GetRegistered()
	require.NotNil(t, registered)
	require.Equal(t, enrollResp.Msg.WorkerId, registered.WorkerId)

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 1},
		},
	}))

	jobMsg, err := stream.Receive()
	require.NoError(t, err)
	leasedJob := jobMsg.GetJob()
	require.NotNil(t, leasedJob)
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
	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Result{Result: result},
	}))

	ackMsg, err := stream.Receive()
	require.NoError(t, err)
	ack := ackMsg.GetAck()
	require.NotNil(t, ack)
	require.True(t, ack.Committed)
	require.Equal(t, leasedJob.RunId, ack.RunId)

	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())

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

func newEnrollmentClient(t *testing.T, env *helpers.ControlPlaneTestEnvironment) openseerv1connect.EnrollmentServiceClient {
	caPool := x509.NewCertPool()
	require.True(t, caPool.AppendCertsFromPEM(env.PKI.GetCACertPEM()))

	httpClient := helpers.NewHTTP2Client(tlsConfig(t, caPool), 5*time.Second)
	enrollmentSrv := env.StartEnrollmentServer(t, "127.0.0.1", "localhost")

	return openseerv1connect.NewEnrollmentServiceClient(httpClient, enrollmentSrv.URL)
}

func enrollWorkerForTest(t *testing.T, env *helpers.ControlPlaneTestEnvironment, enrollmentClient openseerv1connect.EnrollmentServiceClient, hostname, region string) (string, *tls.Config) {
	csr, key := helpers.MustGenerateCSR(t, hostname)

	resp, err := enrollmentClient.EnrollWorker(context.Background(), connect.NewRequest(&openseerv1.EnrollWorkerRequest{
		EnrollmentToken: env.ClusterToken,
		Hostname:        hostname,
		WorkerVersion:   "1.0.0",
		Region:          region,
		CsrPem:          csr,
	}))
	require.NoError(t, err)
	require.True(t, resp.Msg.Accepted)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	workerTLS, err := auth.CreateClientTLSConfig([]byte(resp.Msg.Certificate), keyPEM, env.PKI.GetCACertPEM(), "localhost")
	require.NoError(t, err)

	return resp.Msg.WorkerId, workerTLS
}

func TestEnrollmentRenewalAndRevocation(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentClient := newEnrollmentClient(t, env)

	csr, _ := helpers.MustGenerateCSR(t, "renew-worker")
	enrollResp, err := enrollmentClient.EnrollWorker(ctx, connect.NewRequest(&openseerv1.EnrollWorkerRequest{
		EnrollmentToken: env.ClusterToken,
		Hostname:        "renewal-host",
		WorkerVersion:   "1.2.3",
		Region:          "us-east-1",
		CsrPem:          csr,
	}))
	require.NoError(t, err)
	workerID := enrollResp.Msg.WorkerId
	require.NotEmpty(t, workerID)
	initialExpiry := time.Unix(enrollResp.Msg.ExpiresAt, 0)

	workerBefore, err := env.Queries.GetWorkerByID(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, "enrolled", workerBefore.Status)
	require.True(t, workerBefore.CertificateExpiresAt.Valid)
	require.WithinDuration(t, initialExpiry, workerBefore.CertificateExpiresAt.Time, 5*time.Second)

	newCSR, renewedKey := helpers.MustGenerateCSR(t, "renew-worker")
	renewResp, err := enrollmentClient.RenewEnrollment(ctx, connect.NewRequest(&openseerv1.RenewEnrollmentRequest{
		WorkerId: workerID,
		CsrPem:   newCSR,
	}))
	require.NoError(t, err)
	renewedExpiry := time.Unix(renewResp.Msg.ExpiresAt, 0)
	require.False(t, renewedExpiry.Before(initialExpiry), "renewal should not reduce certificate expiry")
	require.NotEqual(t, enrollResp.Msg.Certificate, renewResp.Msg.Certificate)

	var workerAfter *sqlc.AppWorker
	require.Eventually(t, func() bool {
		w, err := env.Queries.GetWorkerByID(ctx, workerID)
		require.NoError(t, err)
		workerAfter = w
		if !w.CertificateExpiresAt.Valid {
			return false
		}
		delta := w.CertificateExpiresAt.Time.Sub(renewedExpiry)
		return delta.Abs() <= 2*time.Second
	}, 5*time.Second, 50*time.Millisecond, "worker certificate expiry not updated")
	require.NotNil(t, workerAfter)

	delta := workerAfter.CertificateExpiresAt.Time.Sub(renewedExpiry)
	t.Logf("renewed expiry delta: %v (db=%s api=%s)", delta, workerAfter.CertificateExpiresAt.Time.Format(time.RFC3339Nano), renewedExpiry.Format(time.RFC3339Nano))

	renewedKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(renewedKey)})
	workerTLS, err := auth.CreateClientTLSConfig([]byte(renewResp.Msg.Certificate), renewedKeyPEM, env.PKI.GetCACertPEM(), "localhost")
	require.NoError(t, err)

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

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	workerHTTPClient := helpers.NewHTTP2Client(workerTLS, 5*time.Second)
	workerClient := openseerv1connect.NewWorkerServiceClient(workerHTTPClient, workerSrv.URL)

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer streamCancel()
	stream := workerClient.WorkerStream(streamCtx)

	err = stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.2.3"},
		},
	})
	require.NoError(t, err)

	_, err = stream.Receive()
	require.Error(t, err)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	_, err = enrollmentClient.EnrollWorker(ctx, connect.NewRequest(&openseerv1.EnrollWorkerRequest{
		EnrollmentToken: env.ClusterToken,
		Hostname:        "second-worker",
		WorkerVersion:   "1.2.3",
		Region:          "us-east-1",
		CsrPem:          csr,
	}))
	require.NoError(t, err)
}

func TestDispatcherHealthAndReaper(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	enrollmentClient := newEnrollmentClient(t, env)
	workerID, workerTLS := enrollWorkerForTest(t, env, enrollmentClient, "health-worker", "us-east-1")

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	workerHTTPClient := helpers.NewHTTP2Client(workerTLS, 10*time.Second)
	workerClient := openseerv1connect.NewWorkerServiceClient(workerHTTPClient, workerSrv.URL)

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer streamCancel()
	stream := workerClient.WorkerStream(streamCtx)

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))

	serverMsg, err := stream.Receive()
	require.NoError(t, err)
	require.NotNil(t, serverMsg.GetRegistered())

	workerAfterRegister, err := env.Queries.GetWorkerByID(ctx, workerID)
	require.NoError(t, err)
	lastSeenAfterRegister := workerAfterRegister.LastSeenAt

	var ping *openseerv1.Ping
	for ping == nil {
		msg, err := stream.Receive()
		require.NoError(t, err)
		if m := msg.GetPing(); m != nil {
			ping = m
		}
	}

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Pong{
			Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
		},
	}))

	require.Eventually(t, func() bool {
		worker, err := env.Queries.GetWorkerByID(ctx, workerID)
		require.NoError(t, err)
		return worker.LastSeenAt.After(lastSeenAfterRegister)
	}, 5*time.Second, 100*time.Millisecond)

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  2000,
	})

	createdJobs := make(map[string]struct{})
	for i := 0; i < 3; i++ {
		job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")
		createdJobs[job.RunID] = struct{}{}
	}

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 3},
		},
	}))

	receivedRuns := make(map[string]struct{})
	for len(receivedRuns) < 3 {
		msg, err := stream.Receive()
		require.NoError(t, err)
		if job := msg.GetJob(); job != nil {
			_, exists := createdJobs[job.RunId]
			require.Truef(t, exists, "leased unexpected job %s", job.RunId)
			receivedRuns[job.RunId] = struct{}{}
			continue
		}
		if heartbeatPing := msg.GetPing(); heartbeatPing != nil {
			require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_Pong{
					Pong: &openseerv1.Pong{Timestamp: heartbeatPing.Timestamp},
				},
			}))
			continue
		}
	}

	require.Equal(t, 3, len(receivedRuns))

	var expiredRunID string
	for runID := range receivedRuns {
		expiredRunID = runID
		break
	}

	_, err = env.TestDB.DB.ExecContext(ctx, "UPDATE app.jobs SET lease_expires_at = NOW() - INTERVAL '1 second' WHERE run_id = $1", expiredRunID)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		job, err := env.Queries.GetJobByRunID(ctx, expiredRunID)
		require.NoError(t, err)
		return job.Status == "ready" && !job.WorkerID.Valid
	}, 6*time.Second, 150*time.Millisecond)

	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	streamCancel()

	_, err = env.TestDB.DB.ExecContext(context.Background(), "UPDATE app.workers SET last_seen_at = NOW() - INTERVAL '5 minutes' WHERE id = $1", workerID)
	require.NoError(t, err)

	helpers.WaitForWorkerInactivity(t, env.Queries, workerID, 15*time.Second)

	helpers.WaitForWorkerRegistration(t, env.Dispatcher, 0, 10*time.Second)
}

func TestConcurrentWorkersLeaseDistinctJobs(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentClient := newEnrollmentClient(t, env)
	worker1ID, worker1TLS := enrollWorkerForTest(t, env, enrollmentClient, "worker-a", "us-east-1")
	worker2ID, worker2TLS := enrollWorkerForTest(t, env, enrollmentClient, "worker-b", "us-east-1")

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	workerClient1 := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(worker1TLS, 10*time.Second), workerSrv.URL)
	workerClient2 := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(worker2TLS, 10*time.Second), workerSrv.URL)

	streamCtx1, cancelStream1 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelStream1()
	stream1 := workerClient1.WorkerStream(streamCtx1)
	require.NoError(t, stream1.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err := stream1.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())

	streamCtx2, cancelStream2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelStream2()
	stream2 := workerClient2.WorkerStream(streamCtx2)
	require.NoError(t, stream2.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err = stream2.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  2000,
	})

	jobs := make([]*sqlc.AppJob, 0, 2)
	for i := 0; i < 2; i++ {
		jobs = append(jobs, helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1"))
	}

	require.NoError(t, stream1.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 1},
		},
	}))
	require.NoError(t, stream2.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 1},
		},
	}))

	job1 := waitForJob(t, stream1)
	require.NotNil(t, job1)
	job2 := waitForJob(t, stream2)
	require.NotNil(t, job2)

	require.NotEqual(t, job1.RunId, job2.RunId)

	created := map[string]struct{}{jobs[0].RunID: {}, jobs[1].RunID: {}}
	_, ok := created[job1.RunId]
	require.True(t, ok)
	_, ok = created[job2.RunId]
	require.True(t, ok)

	jobRecord1, err := env.Queries.GetJobByRunID(ctx, job1.RunId)
	require.NoError(t, err)
	require.True(t, jobRecord1.WorkerID.Valid)
	require.Equal(t, worker1ID, jobRecord1.WorkerID.String)

	jobRecord2, err := env.Queries.GetJobByRunID(ctx, job2.RunId)
	require.NoError(t, err)
	require.True(t, jobRecord2.WorkerID.Valid)
	require.Equal(t, worker2ID, jobRecord2.WorkerID.String)

	require.NoError(t, stream1.CloseRequest())
	require.NoError(t, stream1.CloseResponse())
	require.NoError(t, stream2.CloseRequest())
	require.NoError(t, stream2.CloseResponse())
}

func TestIngestFailureResultPersistsFields(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentClient := newEnrollmentClient(t, env)
	workerID, workerTLS := enrollWorkerForTest(t, env, enrollmentClient, "ingest-worker", "us-east-1")

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	workerClient := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(workerTLS, 10*time.Second), workerSrv.URL)

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer streamCancel()
	stream := workerClient.WorkerStream(streamCtx)

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err := stream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  2000,
	})
	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 1},
		},
	}))

	leasedJob := waitForJob(t, stream)
	require.Equal(t, job.RunID, leasedJob.RunId)

	errMsg := "http timeout"
	status := "FAIL"
	failingCode := int32(504)
	sizeBytes := int64(4096)
	totalMs := int32(12000)

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Result{
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
		},
	}))

	var ack *openseerv1.ResultAck
	for {
		m, err := stream.Receive()
		require.NoError(t, err)
		if a := m.GetAck(); a != nil {
			ack = a
			break
		}
		if ping := m.GetPing(); ping != nil {
			require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_Pong{
					Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
				},
			}))
		}
	}

	require.True(t, ack.Committed)
	require.Equal(t, leasedJob.RunId, ack.RunId)

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

	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
}

type workerStream interface {
	Receive() (*openseerv1.ServerMessage, error)
	Send(*openseerv1.WorkerMessage) error
}

func waitForJob(t *testing.T, stream workerStream) *openseerv1.MonitorJob {
	for {
		msg, err := stream.Receive()
		require.NoError(t, err)
		if job := msg.GetJob(); job != nil {
			return job
		}
		if ping := msg.GetPing(); ping != nil {
			require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_Pong{
					Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
				},
			}))
		}
	}
}

func waitForAck(t *testing.T, stream workerStream, runID string) {
	for {
		msg, err := stream.Receive()
		require.NoError(t, err)
		if ack := msg.GetAck(); ack != nil && ack.RunId == runID {
			require.True(t, ack.Committed, "Result should be committed")
			return
		}
		if ping := msg.GetPing(); ping != nil {
			require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_Pong{
					Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
				},
			}))
		}
	}
}

func TestWorkerLeaseRenewalExtendsExpiration(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentClient := newEnrollmentClient(t, env)
	workerID, workerTLS := enrollWorkerForTest(t, env, enrollmentClient, "lease-renewal-worker", "us-east-1")

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	workerClient := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(workerTLS, 10*time.Second), workerSrv.URL)

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer streamCancel()
	stream := workerClient.WorkerStream(streamCtx)

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err := stream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  10000,
	})
	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 1},
		},
	}))

	leasedJob := waitForJob(t, stream)
	require.Equal(t, job.RunID, leasedJob.RunId)

	jobRecord, err := env.Queries.GetJobByRunID(ctx, leasedJob.RunId)
	require.NoError(t, err)
	require.Equal(t, "leased", jobRecord.Status)
	require.True(t, jobRecord.LeaseExpiresAt.Valid)
	initialLeaseExpiry := jobRecord.LeaseExpiresAt.Time

	time.Sleep(2 * time.Second)

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_LeaseRenewal{
			LeaseRenewal: &openseerv1.LeaseRenewal{
				RunId: leasedJob.RunId,
			},
		},
	}))

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
	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Result{
			Result: &openseerv1.MonitorResult{
				RunId:     leasedJob.RunId,
				MonitorId: leasedJob.MonitorId,
				Region:    "us-east-1",
				Status:    "OK",
				EventAt:   timestamppb.Now(),
				HttpCode:  &code,
			},
		},
	}))

	var ack *openseerv1.ResultAck
	for {
		m, err := stream.Receive()
		require.NoError(t, err)
		if a := m.GetAck(); a != nil {
			ack = a
			break
		}
		if ping := m.GetPing(); ping != nil {
			require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_Pong{
					Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
				},
			}))
		}
	}

	require.True(t, ack.Committed)
	require.Equal(t, leasedJob.RunId, ack.RunId)

	finalJob, err := env.Queries.GetJobByRunID(ctx, leasedJob.RunId)
	require.NoError(t, err)
	require.Equal(t, "done", finalJob.Status)

	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
}

func TestWorkerResultNegativeAckOnStorageFailure(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)

	enrollmentClient := newEnrollmentClient(t, env)
	_, workerTLS := enrollWorkerForTest(t, env, enrollmentClient, "storage-failure-worker", "us-east-1")

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	workerClient := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(workerTLS, 10*time.Second), workerSrv.URL)

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer streamCancel()
	stream := workerClient.WorkerStream(streamCtx)

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err := stream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())

	monitor := helpers.CreateMonitorWithUser(t, env.Queries, env.TestDB.DB, helpers.MonitorConfig{
		Regions:    []string{"us-east-1"},
		IntervalMs: 1000,
		TimeoutMs:  2000,
	})
	job := helpers.CreateTestJob(t, env.Queries, monitor.ID, "us-east-1")

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 1},
		},
	}))

	leasedJob := waitForJob(t, stream)
	require.Equal(t, job.RunID, leasedJob.RunId)

	env.TestDB.DB.Close()

	code := int32(200)
	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Result{
			Result: &openseerv1.MonitorResult{
				RunId:     leasedJob.RunId,
				MonitorId: leasedJob.MonitorId,
				Region:    "us-east-1",
				Status:    "OK",
				EventAt:   timestamppb.Now(),
				HttpCode:  &code,
			},
		},
	}))

	var ack *openseerv1.ResultAck
	for {
		m, err := stream.Receive()
		require.NoError(t, err)
		if a := m.GetAck(); a != nil {
			ack = a
			break
		}
		if ping := m.GetPing(); ping != nil {
			require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_Pong{
					Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
				},
			}))
		}
	}

	require.False(t, ack.Committed, "ack should indicate failure when storage fails")
	require.Equal(t, leasedJob.RunId, ack.RunId)

	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
}

func TestEnrollmentStatusTransitions(t *testing.T) {
	t.Parallel()

	env := helpers.SetupControlPlane(t)
	env.StartBackgroundServices()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	enrollmentClient := newEnrollmentClient(t, env)

	csr, workerKey := helpers.MustGenerateCSR(t, "status-transition-worker")
	enrollResp, err := enrollmentClient.EnrollWorker(ctx, connect.NewRequest(&openseerv1.EnrollWorkerRequest{
		EnrollmentToken: env.ClusterToken,
		Hostname:        "status-worker",
		WorkerVersion:   "1.0.0",
		Region:          "us-east-1",
		CsrPem:          csr,
	}))
	require.NoError(t, err)
	workerID := enrollResp.Msg.WorkerId

	workerInitial, err := env.Queries.GetWorkerByID(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, "enrolled", workerInitial.Status)

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(workerKey)})
	workerTLS, err := auth.CreateClientTLSConfig([]byte(enrollResp.Msg.Certificate), keyPEM, env.PKI.GetCACertPEM(), "localhost")
	require.NoError(t, err)

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	workerClient := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(workerTLS, 10*time.Second), workerSrv.URL)

	streamCtx, streamCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer streamCancel()
	stream := workerClient.WorkerStream(streamCtx)

	require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err := stream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())

	workerAfterRegister, err := env.Queries.GetWorkerByID(ctx, workerID)
	require.NoError(t, err)
	require.Equal(t, "active", workerAfterRegister.Status)

	var receivedPing bool
	for !receivedPing {
		msg, err := stream.Receive()
		require.NoError(t, err)
		if ping := msg.GetPing(); ping != nil {
			receivedPing = true
			require.NoError(t, stream.Send(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_Pong{
					Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
				},
			}))
		}
	}

	require.NoError(t, stream.CloseRequest())
	require.NoError(t, stream.CloseResponse())
	streamCancel()

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

	newStreamCtx, newStreamCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer newStreamCancel()
	newStream := workerClient.WorkerStream(newStreamCtx)

	err = newStream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	})
	require.NoError(t, err)

	_, err = newStream.Receive()
	require.Error(t, err, "revoked worker should not be able to connect")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
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
			interval := jobs5s[i].ScheduledAt.Sub(jobs5s[i-1].ScheduledAt)
			require.InDelta(t, 5000, interval.Milliseconds(), 100,
				"5s monitor should have no jitter, got interval: %v", interval)
		}
	}

	jobs15s, err := getJobsForMonitor(ctx, env, monitor15s.ID, 3)
	require.NoError(t, err)
	if len(jobs15s) >= 2 {
		for i := 1; i < len(jobs15s); i++ {
			interval := jobs15s[i].ScheduledAt.Sub(jobs15s[i-1].ScheduledAt)
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
			interval := jobs60s[i].ScheduledAt.Sub(jobs60s[i-1].ScheduledAt)
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
		IntervalMs: 1000,
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

func generateRandomID(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
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

	httpCode2 := int32(500)
	totalMs2 := int32(250)
	resultModified := &openseerv1.MonitorResult{
		RunId:     jobID,
		MonitorId: monitor.ID,
		Region:    "us-east-1",
		Status:    "FAIL",
		EventAt:   eventTime,
		HttpCode:  &httpCode2,
		TotalMs:   &totalMs2,
	}

	err = env.Ingest.ProcessResult(ctx, resultModified)
	require.NoError(t, err, "modified result submission should succeed")

	countResult, err = env.Queries.CountResultsByRunID(ctx, jobID)
	require.NoError(t, err)
	require.Equal(t, int64(1), countResult, "should still have exactly one result after update")

	resultRow, err := env.Queries.GetResultByRunIDAndTime(ctx, &sqlc.GetResultByRunIDAndTimeParams{
		RunID:   jobID,
		EventAt: eventTime.AsTime(),
	})
	require.NoError(t, err)
	require.Equal(t, "FAIL", resultRow.Status, "status should be updated")
	require.True(t, resultRow.HttpCode.Valid)
	require.Equal(t, int32(500), resultRow.HttpCode.Int32, "http_code should be updated")
	require.True(t, resultRow.TotalMs.Valid)
	require.Equal(t, int32(250), resultRow.TotalMs.Int32, "total_ms should be updated")
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

	enrollmentClient := newEnrollmentClient(t, env)
	usWorkerID, usWorkerTLS := enrollWorkerForTest(t, env, enrollmentClient, "us-worker", "us-east-1")

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")
	usWorkerClient := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(usWorkerTLS, 10*time.Second), workerSrv.URL)

	usStream := usWorkerClient.WorkerStream(ctx)
	defer usStream.CloseRequest()

	require.NoError(t, usStream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err := usStream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())
	require.Equal(t, usWorkerID, msg.GetRegistered().WorkerId)

	require.NoError(t, usStream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 5},
		},
	}))

	usReceivedJobs := make(map[string]bool)
	jobsReceived := 0
	maxExpectedJobs := 2
	timeout := time.After(10 * time.Second)

	for jobsReceived < maxExpectedJobs {
		select {
		case <-timeout:
			t.Logf("Timeout reached after receiving %d jobs", jobsReceived)
			goto checkResults
		default:
			msg, err := usStream.Receive()
			if err != nil {
				t.Logf("Stream receive error: %v", err)
				goto checkResults
			}

			if job := msg.GetJob(); job != nil {
				usReceivedJobs[job.RunId] = true
				jobsReceived++
				t.Logf("US worker received job: %s", job.RunId)

				require.NoError(t, usStream.Send(&openseerv1.WorkerMessage{
					Message: &openseerv1.WorkerMessage_Result{
						Result: &openseerv1.MonitorResult{
							RunId:     job.RunId,
							MonitorId: job.MonitorId,
							Region:    "us-east-1",
							Status:    "OK",
							EventAt:   timestamppb.Now(),
						},
					},
				}))
			} else if ping := msg.GetPing(); ping != nil {
				require.NoError(t, usStream.Send(&openseerv1.WorkerMessage{
					Message: &openseerv1.WorkerMessage_Pong{
						Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
					},
				}))
			}
		}
	}

checkResults:
	require.Greater(t, jobsReceived, 0, "US worker should receive at least one job")

	for jobID := range usReceivedJobs {
		job, err := env.Queries.GetJobByRunID(ctx, jobID)
		require.NoError(t, err, "Failed to get job %s", jobID)

		require.True(t, job.Region == "us-east-1" || job.Region == "global",
			"US worker received job for wrong region: %s (job region: %s)", jobID, job.Region)
	}

	euWorkerID, euWorkerTLS := enrollWorkerForTest(t, env, enrollmentClient, "eu-worker", "eu-west-1")
	euWorkerClient := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(euWorkerTLS, 10*time.Second), workerSrv.URL)

	euStream := euWorkerClient.WorkerStream(ctx)
	defer euStream.CloseRequest()

	require.NoError(t, euStream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "eu-west-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err = euStream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())
	require.Equal(t, euWorkerID, msg.GetRegistered().WorkerId)

	require.NoError(t, euStream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 5},
		},
	}))

	euReceivedJobs := make(map[string]bool)
	jobsReceived = 0
	timeout = time.After(5 * time.Second)

euJobLoop:
	for jobsReceived < 1 {
		select {
		case <-timeout:
			break euJobLoop
		default:
			msg, err := euStream.Receive()
			if err != nil {
				break euJobLoop
			}

			if job := msg.GetJob(); job != nil {
				euReceivedJobs[job.RunId] = true
				jobsReceived++

				require.NoError(t, euStream.Send(&openseerv1.WorkerMessage{
					Message: &openseerv1.WorkerMessage_Result{
						Result: &openseerv1.MonitorResult{
							RunId:     job.RunId,
							MonitorId: job.MonitorId,
							Region:    "eu-west-1",
							Status:    "OK",
							EventAt:   timestamppb.Now(),
						},
					},
				}))
			} else if ping := msg.GetPing(); ping != nil {
				require.NoError(t, euStream.Send(&openseerv1.WorkerMessage{
					Message: &openseerv1.WorkerMessage_Pong{
						Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
					},
				}))
			}
		}
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

	worker := helpers.CreateTestWorker(t, env.Queries, "us-east-1")
	leasedJobs, err := env.Queries.LeaseJobs(ctx, &sqlc.LeaseJobsParams{
		WorkerID: sql.NullString{String: worker.ID, Valid: true},
		Limit:    1,
		Region:   "us-east-1",
	})
	require.NoError(t, err)
	require.Len(t, leasedJobs, 0, "soft deleted jobs should not be leasable")
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
		WorkerID: sql.NullString{String: worker.ID, Valid: true},
		Limit:    int32(len(jobs)),
		Region:   "us-east-1",
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

	enrollmentClient := newEnrollmentClient(t, env)
	worker1ID, worker1TLS := enrollWorkerForTest(t, env, enrollmentClient, "worker-1", "us-east-1")
	worker2ID, worker2TLS := enrollWorkerForTest(t, env, enrollmentClient, "worker-2", "us-east-1")

	workerSrv := env.StartWorkerServer(t, "127.0.0.1", "localhost")

	worker1Client := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(worker1TLS, 10*time.Second), workerSrv.URL)
	worker1Stream := worker1Client.WorkerStream(ctx)
	defer worker1Stream.CloseRequest()

	require.NoError(t, worker1Stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err := worker1Stream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())
	require.Equal(t, worker1ID, msg.GetRegistered().WorkerId)

	worker2Client := openseerv1connect.NewWorkerServiceClient(helpers.NewHTTP2Client(worker2TLS, 10*time.Second), workerSrv.URL)
	worker2Stream := worker2Client.WorkerStream(ctx)
	defer worker2Stream.CloseRequest()

	require.NoError(t, worker2Stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{Region: "us-east-1", WorkerVersion: "1.0.0"},
		},
	}))
	msg, err = worker2Stream.Receive()
	require.NoError(t, err)
	require.NotNil(t, msg.GetRegistered())
	require.Equal(t, worker2ID, msg.GetRegistered().WorkerId)

	require.NoError(t, worker1Stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_JobRequest{
			JobRequest: &openseerv1.JobRequest{Count: 1},
		},
	}))

	var assignedJob *openseerv1.MonitorJob
	for {
		msg, err = worker1Stream.Receive()
		require.NoError(t, err)
		if j := msg.GetJob(); j != nil {
			assignedJob = j
			break
		}
		if ping := msg.GetPing(); ping != nil {
			require.NoError(t, worker1Stream.Send(&openseerv1.WorkerMessage{
				Message: &openseerv1.WorkerMessage_Pong{
					Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
				},
			}))
		}
	}
	require.NotNil(t, assignedJob)
	require.Equal(t, job.RunID, assignedJob.RunId)

	err = worker2Stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Result{
			Result: &openseerv1.MonitorResult{
				RunId:     assignedJob.RunId,
				MonitorId: assignedJob.MonitorId,
				Region:    "us-east-1",
				Status:    "OK",
				EventAt:   timestamppb.Now(),
			},
		},
	})
	require.NoError(t, err)

	ackReceived := false
	timeout := time.After(5 * time.Second)
waitForNegativeAck:
	for !ackReceived {
		select {
		case <-timeout:
			break waitForNegativeAck
		default:
			msg, err = worker2Stream.Receive()
			if err != nil {
				break waitForNegativeAck
			}
			if ack := msg.GetAck(); ack != nil && ack.RunId == assignedJob.RunId {
				require.False(t, ack.Committed, "Result from wrong worker should not be committed")
				ackReceived = true
			} else if ping := msg.GetPing(); ping != nil {
				require.NoError(t, worker2Stream.Send(&openseerv1.WorkerMessage{
					Message: &openseerv1.WorkerMessage_Pong{
						Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
					},
				}))
			}
		}
	}

	err = worker2Stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_LeaseRenewal{
			LeaseRenewal: &openseerv1.LeaseRenewal{
				RunId: assignedJob.RunId,
			},
		},
	})
	require.NoError(t, err)

	err = worker1Stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Result{
			Result: &openseerv1.MonitorResult{
				RunId:     assignedJob.RunId,
				MonitorId: assignedJob.MonitorId,
				Region:    "us-east-1",
				Status:    "OK",
				EventAt:   timestamppb.Now(),
			},
		},
	})
	require.NoError(t, err)

	ackReceived = false
	timeout = time.After(5 * time.Second)
waitForPositiveAck:
	for !ackReceived {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for ACK")
		default:
			msg, err = worker1Stream.Receive()
			if err != nil {
				break waitForPositiveAck
			}
			if ack := msg.GetAck(); ack != nil && ack.RunId == assignedJob.RunId {
				require.True(t, ack.Committed, "Result from correct worker should be committed")
				ackReceived = true
			} else if ping := msg.GetPing(); ping != nil {
				require.NoError(t, worker1Stream.Send(&openseerv1.WorkerMessage{
					Message: &openseerv1.WorkerMessage_Pong{
						Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
					},
				}))
			}
		}
	}

	err = worker1Stream.Send(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Result{
			Result: &openseerv1.MonitorResult{
				RunId:     assignedJob.RunId,
				MonitorId: assignedJob.MonitorId,
				Region:    "us-east-1",
				Status:    "FAIL",
				EventAt:   timestamppb.Now(),
			},
		},
	})
	require.NoError(t, err)

	ackReceived = false
	timeout = time.After(5 * time.Second)
waitForDuplicateAck:
	for !ackReceived {
		select {
		case <-timeout:
			break waitForDuplicateAck
		default:
			msg, err = worker1Stream.Receive()
			if err != nil {
				break waitForDuplicateAck
			}
			if ack := msg.GetAck(); ack != nil && ack.RunId == assignedJob.RunId {
				require.True(t, ack.Committed, "Result should be committed due to UPSERT")
				ackReceived = true
			} else if ping := msg.GetPing(); ping != nil {
				require.NoError(t, worker1Stream.Send(&openseerv1.WorkerMessage{
					Message: &openseerv1.WorkerMessage_Pong{
						Pong: &openseerv1.Pong{Timestamp: ping.Timestamp},
					},
				}))
			}
		}
	}

	completedJobs, err := env.Queries.GetCompletedJobsByMonitor(ctx, monitor.ID)
	require.NoError(t, err)
	require.Len(t, completedJobs, 1, "Job should be completed exactly once")
	require.Equal(t, "done", completedJobs[0].Status)
}

func tlsConfig(t *testing.T, caPool *x509.CertPool) *tls.Config {
	t.Helper()
	return &tls.Config{
		RootCAs:    caPool,
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	}
}
