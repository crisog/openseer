package auth

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"github.com/crisog/openseer/internal/pkg/recovery"
)

type AuthService struct {
	ca           *CA
	clusterToken string
	logger       *zap.Logger

	mu          sync.RWMutex
	clientCerts map[string]*CertInfo
}

type CertInfo struct {
	cert      *ClientCert
	expiresAt time.Time
}

type RegistrationRequest struct {
	ClusterToken  string
	WorkerID      string
	Region        string
	WorkerVersion string
}

type RegistrationResponse struct {
	WorkerID  string
	CertPEM   []byte
	KeyPEM    []byte
	CACertPEM []byte
	ExpiresAt time.Time
}

func NewAuthService(clusterToken string, pki *PKI, logger *zap.Logger) (*AuthService, error) {
	return &AuthService{
		ca:           pki.ca,
		clusterToken: clusterToken,
		logger:       logger,
		clientCerts:  make(map[string]*CertInfo),
	}, nil
}

func (a *AuthService) Register(req *RegistrationRequest) (*RegistrationResponse, error) {
	if subtle.ConstantTimeCompare([]byte(req.ClusterToken), []byte(a.clusterToken)) != 1 {
		return nil, fmt.Errorf("invalid cluster token")
	}

	a.mu.RLock()
	if certInfo, exists := a.clientCerts[req.WorkerID]; exists {
		if time.Now().Before(certInfo.expiresAt.Add(-5 * time.Minute)) {
			a.mu.RUnlock()
			return &RegistrationResponse{
				WorkerID:  req.WorkerID,
				CertPEM:   certInfo.cert.GetCertPEM(),
				KeyPEM:    certInfo.cert.GetKeyPEM(),
				CACertPEM: a.ca.GetCertPEM(),
				ExpiresAt: certInfo.expiresAt,
			}, nil
		}
	}
	a.mu.RUnlock()

	clientCert, err := a.ca.NewClientCert(req.WorkerID, 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("failed to create client certificate: %w", err)
	}

	expiresAt := time.Now().Add(24 * time.Hour)
	a.mu.Lock()
	a.clientCerts[req.WorkerID] = &CertInfo{
		cert:      clientCert,
		expiresAt: expiresAt,
	}
	a.mu.Unlock()

	return &RegistrationResponse{
		WorkerID:  req.WorkerID,
		CertPEM:   clientCert.GetCertPEM(),
		KeyPEM:    clientCert.GetKeyPEM(),
		CACertPEM: a.ca.GetCertPEM(),
		ExpiresAt: expiresAt,
	}, nil
}

func (a *AuthService) CreateServerTLSConfig(hosts []string) (*tls.Config, error) {
	serverCert, err := a.ca.NewServerCert(hosts, 365*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("failed to create server certificate: %w", err)
	}

	cert, err := tls.X509KeyPair(serverCert.GetCertPEM(), serverCert.GetKeyPEM())
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS certificate: %w", err)
	}

	clientCAPool := a.ca.CreateCertPool()

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAPool,
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		},
		PreferServerCipherSuites: true,
	}, nil
}

func (a *AuthService) CreateWebServerTLSConfig(hosts []string) (*tls.Config, error) {
	serverCert, err := a.ca.NewServerCert(hosts, 365*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("failed to create server certificate: %w", err)
	}

	cert, err := tls.X509KeyPair(serverCert.GetCertPEM(), serverCert.GetKeyPEM())
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS certificate: %w", err)
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.NoClientCert,
		MinVersion:   tls.VersionTLS12,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		},
		PreferServerCipherSuites: true,
	}, nil
}

func CreateClientTLSConfig(certPEM, keyPEM, caCertPEM []byte, serverName string) (*tls.Config, error) {
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to create client certificate: %w", err)
	}

	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to add CA certificate")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caPool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

func (a *AuthService) GetCA() *CA {
	return a.ca
}

func (a *AuthService) CleanupExpiredCerts() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	for workerID, certInfo := range a.clientCerts {
		if now.After(certInfo.expiresAt) {
			delete(a.clientCerts, workerID)
		}
	}
}

func (a *AuthService) StartCleanupWorker() {
	ticker := time.NewTicker(1 * time.Hour)
	go recovery.WithRecoverCallback("auth-cleanup-worker", func() {
		for range ticker.C {
			a.CleanupExpiredCerts()
		}
	}, func(err error) {
		a.logger.Error("Auth cleanup worker crashed - expired certificates may not be cleaned up",
			zap.Error(err))
	})()
}
