package worker

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/gen/openseer/v1/openseerv1connect"
	"github.com/crisog/openseer/internal/pkg/recovery"
	"golang.org/x/net/http2"
)

type Worker struct {
	id               string
	region           string
	version          string
	controlPlaneAddr string
	enrollmentPort   string
	clusterToken     string
	enrollmentScheme string
	enrollmentTLS    *tls.Config
	workerClient     openseerv1connect.WorkerServiceClient
	enrollmentClient openseerv1connect.EnrollmentServiceClient
	stream           *connect.BidiStreamForClient[openseerv1.WorkerMessage, openseerv1.ServerMessage]
	httpClient       *http.Client

	certPEM    []byte
	keyPEM     []byte
	caCertPEM  []byte
	certExpiry time.Time

	mu                sync.RWMutex
	maxConcurrency    int32
	activeJobs        map[string]context.CancelFunc
	pendingResults    map[string]*openseerv1.MonitorResult
	reconnectAttempts int
	connected         bool
	receiveCancel     context.CancelFunc
	sendMu            sync.Mutex
}

func NewWorker(id, region, version, controlPlaneAddr, enrollmentPort, clusterToken string, maxConcurrency int32, enrollmentScheme string, enrollmentTLSConfig *tls.Config, httpClient *http.Client) *Worker {
	return &Worker{
		id:               id,
		region:           region,
		version:          version,
		controlPlaneAddr: controlPlaneAddr,
		enrollmentPort:   enrollmentPort,
		clusterToken:     clusterToken,
		enrollmentScheme: enrollmentScheme,
		enrollmentTLS:    enrollmentTLSConfig,
		maxConcurrency:   maxConcurrency,
		activeJobs:       make(map[string]context.CancelFunc),
		pendingResults:   make(map[string]*openseerv1.MonitorResult),
		httpClient:       httpClient,
	}
}

func (w *Worker) Run(ctx context.Context) error {
	backoff := time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := w.connectAndMaintainStream(ctx); err != nil {
			log.Printf("Stream disconnected: %v. Reconnecting in %v...", err, backoff)
			select {
			case <-time.After(backoff):
				if backoff < maxBackoff {
					backoff *= 2
				}
				w.reconnectAttempts++
			case <-ctx.Done():
				return ctx.Err()
			}
		} else {
			backoff = time.Second
			w.reconnectAttempts = 0
		}
	}
}

func (w *Worker) connectAndMaintainStream(ctx context.Context) error {
	if w.needsCertificate() {
		log.Printf("Enrolling for certificate...")
		if err := w.enrollForCertificate(ctx, w.controlPlaneAddr); err != nil {
			return fmt.Errorf("certificate enrollment failed: %w", err)
		}
	}

	tlsConfig, err := w.createClientTLSConfig()
	if err != nil {
		return fmt.Errorf("failed to create TLS config: %w", err)
	}

	httpClient := &http.Client{
		Transport: &http2.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	connectURL := "https://" + w.controlPlaneAddr

	w.workerClient = openseerv1connect.NewWorkerServiceClient(httpClient, connectURL, connect.WithGRPC())
	w.enrollmentClient = openseerv1connect.NewEnrollmentServiceClient(httpClient, connectURL, connect.WithGRPC())

	stream := w.workerClient.WorkerStream(ctx)

	w.mu.Lock()
	w.stream = stream
	w.connected = true
	w.reconnectAttempts = 0
	w.mu.Unlock()

	if err := w.sendMessage(&openseerv1.WorkerMessage{
		Message: &openseerv1.WorkerMessage_Register{
			Register: &openseerv1.RegisterRequest{
				WorkerVersion: w.version,
				Region:        w.region,
			},
		},
	}); err != nil {
		return fmt.Errorf("failed to register: %w", err)
	}

	log.Printf("Connected to control plane (attempt %d)", w.reconnectAttempts+1)

	var wg sync.WaitGroup
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	wg.Add(1)
	go func() {
		defer wg.Done()
		recovery.WithRecoverAndRestart("job-requester", func() { w.jobRequester(streamCtx) })()
	}()

	w.mu.Lock()
	w.receiveCancel = streamCancel
	w.mu.Unlock()

	wg.Add(1)
	go func() {
		defer wg.Done()
		w.receiveLoop(streamCtx)
	}()

	select {
	case <-ctx.Done():
		streamCancel()
		wg.Wait()
		w.mu.Lock()
		w.connected = false
		w.receiveCancel = nil
		w.mu.Unlock()
		return ctx.Err()
	case <-streamCtx.Done():
		streamCancel()
		wg.Wait()
		w.mu.Lock()
		w.connected = false
		w.receiveCancel = nil
		w.mu.Unlock()
		return fmt.Errorf("stream closed")
	}
}

func (w *Worker) sendMessage(msg *openseerv1.WorkerMessage) error {
	w.sendMu.Lock()
	defer w.sendMu.Unlock()

	w.mu.RLock()
	stream := w.stream
	connected := w.connected
	w.mu.RUnlock()

	if stream == nil || !connected {
		return fmt.Errorf("stream unavailable")
	}

	if err := stream.Send(msg); err != nil {
		w.mu.Lock()
		w.connected = false
		w.mu.Unlock()
		return err
	}

	return nil
}

func (w *Worker) needsCertificate() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.certPEM == nil || w.keyPEM == nil || w.caCertPEM == nil {
		return true
	}
	if time.Until(w.certExpiry) < 5*time.Minute {
		return true
	}
	return false
}

func (w *Worker) enrollForCertificate(ctx context.Context, controlPlaneAddr string) error {
	host := controlPlaneAddr
	if idx := strings.Index(controlPlaneAddr, ":"); idx >= 0 {
		host = controlPlaneAddr[:idx]
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	w.mu.RLock()
	workerID := w.id
	w.mu.RUnlock()

	csrTemplate := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: workerID,
		},
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, privateKey)
	if err != nil {
		return fmt.Errorf("failed to create CSR: %w", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrBytes,
	})

	var transport http.RoundTripper
	if w.enrollmentTLS != nil {
		transport = &http.Transport{
			TLSClientConfig: w.enrollmentTLS.Clone(),
		}
	}

	enrollClient := &http.Client{
		Timeout: 30 * time.Second,
	}
	if transport != nil {
		enrollClient.Transport = transport
	}

	scheme := w.enrollmentScheme
	if scheme == "" {
		if w.enrollmentTLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	connectURL := fmt.Sprintf("%s://%s:%s", scheme, host, w.enrollmentPort)
	enrollmentClient := openseerv1connect.NewEnrollmentServiceClient(enrollClient, connectURL)

	req := &openseerv1.EnrollWorkerRequest{
		WorkerVersion:   w.version,
		Region:          w.region,
		Hostname:        w.id,
		EnrollmentToken: w.clusterToken,
		Capabilities:    map[string]string{},
		CsrPem:          string(csrPEM),
	}

	resp, err := enrollmentClient.EnrollWorker(ctx, connect.NewRequest(req))
	if err != nil {
		return fmt.Errorf("enrollment failed: %w", err)
	}

	if !resp.Msg.Accepted {
		return fmt.Errorf("enrollment rejected: %s", resp.Msg.Reason)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	w.mu.Lock()
	w.certPEM = []byte(resp.Msg.Certificate)
	w.keyPEM = keyPEM
	w.caCertPEM = []byte(resp.Msg.CaCertificate)
	w.certExpiry = time.Unix(resp.Msg.ExpiresAt, 0)
	w.id = resp.Msg.WorkerId
	w.mu.Unlock()

	return nil
}

func (w *Worker) createClientTLSConfig() (*tls.Config, error) {
	w.mu.RLock()
	if w.certPEM == nil || w.keyPEM == nil || w.caCertPEM == nil {
		w.mu.RUnlock()
		return nil, fmt.Errorf("worker certificates not available, enrollment required")
	}

	certPEM := make([]byte, len(w.certPEM))
	copy(certPEM, w.certPEM)
	keyPEM := make([]byte, len(w.keyPEM))
	copy(keyPEM, w.keyPEM)
	caCertPEM := make([]byte, len(w.caCertPEM))
	copy(caCertPEM, w.caCertPEM)
	w.mu.RUnlock()

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	host := w.controlPlaneAddr
	if idx := strings.Index(w.controlPlaneAddr, ":"); idx >= 0 {
		host = w.controlPlaneAddr[:idx]
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		ServerName:   host,
	}, nil
}
