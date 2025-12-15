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

	mu             sync.RWMutex
	maxConcurrency int32
	activeJobs     map[string]context.CancelFunc
	pollInterval   time.Duration
}

func NewWorker(id, region, version, enrollmentURL, clusterToken string, maxConcurrency int32, httpClient *http.Client) *Worker {
	return &Worker{
		id:             id,
		region:         region,
		version:        version,
		enrollmentURL:  enrollmentURL,
		clusterToken:   clusterToken,
		maxConcurrency: maxConcurrency,
		activeJobs:     make(map[string]context.CancelFunc),
		httpClient:     httpClient,
		pollInterval:   200 * time.Millisecond,
	}
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

func (w *Worker) authHeader() http.Header {
	w.mu.RLock()
	token := w.apiToken
	w.mu.RUnlock()

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+token)
	return headers
}
