package worker

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
)

type Worker struct {
	id               string
	region           string
	version          string
	apiEndpoint      string
	enrollmentURL    string
	clusterToken     string
	httpClient       *http.Client
	workerClient     openseerv1connect.WorkerServiceClient
	enrollmentClient openseerv1connect.EnrollmentServiceClient

	apiToken string

	refreshMu sync.Mutex

	mu                        sync.RWMutex
	maxConcurrency            int32
	activeJobs                map[string]context.CancelFunc
	pollBaseInterval          time.Duration
	pollMaxInterval           time.Duration
	leaseRenewalThreshold     int32
	leaseRenewalInterval      time.Duration
	resultSubmitMaxAttempts   int
	resultSubmitRetryInterval time.Duration
	resultSubmitTimeout       time.Duration
}

func NewWorker(id, region, version, enrollmentURL, clusterToken string, maxConcurrency int32, httpClient *http.Client) *Worker {
	return &Worker{
		id:                        id,
		region:                    region,
		version:                   version,
		enrollmentURL:             enrollmentURL,
		clusterToken:              clusterToken,
		maxConcurrency:            maxConcurrency,
		activeJobs:                make(map[string]context.CancelFunc),
		httpClient:                httpClient,
		pollBaseInterval:          1 * time.Second,
		pollMaxInterval:           10 * time.Second,
		leaseRenewalThreshold:     20000,
		leaseRenewalInterval:      10 * time.Second,
		resultSubmitMaxAttempts:   6,
		resultSubmitRetryInterval: 5 * time.Second,
		resultSubmitTimeout:       10 * time.Second,
	}
}

func (w *Worker) WithLeaseRenewalConfig(threshold int32, interval time.Duration) *Worker {
	if threshold > 0 {
		w.leaseRenewalThreshold = threshold
	}
	if interval > 0 {
		w.leaseRenewalInterval = interval
	}
	return w
}

func (w *Worker) WithPollingConfig(baseInterval, maxInterval time.Duration) *Worker {
	if baseInterval <= 0 {
		baseInterval = time.Second
	}
	if maxInterval < baseInterval {
		maxInterval = baseInterval
	}
	w.pollBaseInterval = baseInterval
	w.pollMaxInterval = maxInterval
	return w
}

func (w *Worker) WithResultRetryConfig(maxAttempts int, retryInterval, submitTimeout time.Duration) *Worker {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if retryInterval <= 0 {
		retryInterval = 5 * time.Second
	}
	if submitTimeout <= 0 {
		submitTimeout = 10 * time.Second
	}
	w.resultSubmitMaxAttempts = maxAttempts
	w.resultSubmitRetryInterval = retryInterval
	w.resultSubmitTimeout = submitTimeout
	return w
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.enroll(ctx); err != nil {
		return fmt.Errorf("enrollment failed: %w", err)
	}

	log.Printf("Worker %s enrolled successfully, starting polling loop", w.id)

	return w.pollLoop(ctx)
}

func (w *Worker) enroll(ctx context.Context) error {
	w.enrollmentClient = openseerv1connect.NewEnrollmentServiceClient(w.httpClient, w.enrollmentURL)

	req := &openseerv1.EnrollWorkerRequest{
		WorkerVersion:   w.version,
		Region:          w.region,
		Hostname:        w.id,
		EnrollmentToken: w.clusterToken,
		Capabilities:    map[string]string{},
	}

	resp, err := w.enrollmentClient.EnrollWorker(ctx, connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("enrollment request failed: %w", err)
	}

	if !resp.Msg.Accepted {
		return fmt.Errorf("enrollment rejected: %s", resp.Msg.Reason)
	}

	w.mu.Lock()
	w.id = resp.Msg.WorkerId
	w.apiToken = resp.Msg.ApiToken
	w.apiEndpoint = resp.Msg.ApiEndpoint
	w.mu.Unlock()

	w.workerClient = openseerv1connect.NewWorkerServiceClient(w.httpClient, w.apiEndpoint)

	log.Printf("Enrolled as worker %s, API endpoint: %s", w.id, w.apiEndpoint)
	return nil
}

func (w *Worker) refreshAPIToken(ctx context.Context) error {
	w.refreshMu.Lock()
	defer w.refreshMu.Unlock()

	if w.enrollmentClient == nil {
		w.enrollmentClient = openseerv1connect.NewEnrollmentServiceClient(w.httpClient, w.enrollmentURL)
	}

	w.mu.RLock()
	workerID := w.id
	w.mu.RUnlock()

	if workerID == "" {
		return fmt.Errorf("cannot renew API token without worker ID")
	}

	resp, err := w.enrollmentClient.RenewEnrollment(ctx, connect.NewRequest(&openseerv1.RenewEnrollmentRequest{
		WorkerId: workerID,
	}))
	if err != nil {
		return fmt.Errorf("renew enrollment request failed: %w", err)
	}

	if !resp.Msg.Renewed || resp.Msg.ApiToken == "" {
		reason := resp.Msg.Reason
		if reason == "" {
			reason = "renewal rejected"
		}
		return fmt.Errorf("failed to renew enrollment for worker %s: %s", workerID, reason)
	}

	w.mu.Lock()
	w.apiToken = resp.Msg.ApiToken
	w.mu.Unlock()

	log.Printf("Renewed API token for worker %s", workerID)
	return nil
}

func (w *Worker) authHeader() http.Header {
	w.mu.RLock()
	token := w.apiToken
	w.mu.RUnlock()

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	return headers
}
