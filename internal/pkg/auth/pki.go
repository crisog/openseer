package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"time"
)

type CA struct {
	cert    *x509.Certificate
	key     *rsa.PrivateKey
	certPEM []byte
	keyPEM  []byte
}

type ClientCert struct {
	cert    *x509.Certificate
	key     *rsa.PrivateKey
	certPEM []byte
	keyPEM  []byte
}

func generateSubjectKeyID(pubKey *rsa.PublicKey) ([]byte, error) {
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(pubKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}
	hash := sha256.Sum256(pubKeyBytes)
	return hash[:], nil
}

func generateSecureSerialNumber() (*big.Int, error) {
	serialNumberBytes := make([]byte, 20)
	if _, err := rand.Read(serialNumberBytes); err != nil {
		return nil, fmt.Errorf("failed to generate random serial number: %w", err)
	}

	serialNumberBytes[0] &= 0x7F

	serialNumber := new(big.Int).SetBytes(serialNumberBytes)

	if serialNumber.Cmp(big.NewInt(0)) == 0 {
		serialNumber.SetInt64(1)
	}

	return serialNumber, nil
}

func NewCA(organization string, validFor time.Duration) (*CA, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA key: %w", err)
	}

	caSerialNumber, err := generateSecureSerialNumber()
	if err != nil {
		return nil, fmt.Errorf("failed to generate CA serial number: %w", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber: caSerialNumber,
		Subject: pkix.Name{
			Organization:  []string{organization},
			Country:       []string{"US"},
			Province:      []string{""},
			Locality:      []string{""},
			StreetAddress: []string{""},
			PostalCode:    []string{""},
			CommonName:    fmt.Sprintf("%s Root CA", organization),
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(validFor),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create CA certificate: %w", err)
	}

	caCert, err := x509.ParseCertificate(caBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: caBytes,
	})

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(caKey),
	})

	return &CA{
		cert:    caCert,
		key:     caKey,
		certPEM: certPEM,
		keyPEM:  keyPEM,
	}, nil
}

func (ca *CA) NewServerCert(hosts []string, validFor time.Duration) (*ClientCert, error) {
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server key: %w", err)
	}

	subjectKeyId, err := generateSubjectKeyID(&serverKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate subject key ID: %w", err)
	}

	serialNumber, err := generateSecureSerialNumber()
	if err != nil {
		return nil, fmt.Errorf("failed to generate server certificate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization:  ca.cert.Subject.Organization,
			Country:       ca.cert.Subject.Country,
			Province:      ca.cert.Subject.Province,
			Locality:      ca.cert.Subject.Locality,
			StreetAddress: ca.cert.Subject.StreetAddress,
			PostalCode:    ca.cert.Subject.PostalCode,
			CommonName:    "openseer-control-plane",
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(validFor),
		SubjectKeyId: subjectKeyId,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}

	serverBytes, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &serverKey.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create server certificate: %w", err)
	}

	serverCert, err := x509.ParseCertificate(serverBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse server certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: serverBytes,
	})

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(serverKey),
	})

	return &ClientCert{
		cert:    serverCert,
		key:     serverKey,
		certPEM: certPEM,
		keyPEM:  keyPEM,
	}, nil
}

func (ca *CA) NewClientCert(workerID string, validFor time.Duration) (*ClientCert, error) {
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate client key: %w", err)
	}

	subjectKeyId, err := generateSubjectKeyID(&clientKey.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to generate subject key ID: %w", err)
	}

	serialNumber, err := generateSecureSerialNumber()
	if err != nil {
		return nil, fmt.Errorf("failed to generate client certificate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization:  ca.cert.Subject.Organization,
			Country:       ca.cert.Subject.Country,
			Province:      ca.cert.Subject.Province,
			Locality:      ca.cert.Subject.Locality,
			StreetAddress: ca.cert.Subject.StreetAddress,
			PostalCode:    ca.cert.Subject.PostalCode,
			CommonName:    workerID,
		},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(validFor),
		SubjectKeyId: subjectKeyId,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	clientBytes, err := x509.CreateCertificate(rand.Reader, template, ca.cert, &clientKey.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create client certificate: %w", err)
	}

	clientCert, err := x509.ParseCertificate(clientBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse client certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: clientBytes,
	})

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(clientKey),
	})

	return &ClientCert{
		cert:    clientCert,
		key:     clientKey,
		certPEM: certPEM,
		keyPEM:  keyPEM,
	}, nil
}

func (ca *CA) GetCertPEM() []byte {
	return ca.certPEM
}

func (ca *CA) GetKeyPEM() []byte {
	return ca.keyPEM
}

func (cc *ClientCert) GetCertPEM() []byte {
	return cc.certPEM
}

func (cc *ClientCert) GetKeyPEM() []byte {
	return cc.keyPEM
}

func (cc *ClientCert) GetCert() *x509.Certificate {
	return cc.cert
}

func (cc *ClientCert) GetKey() *rsa.PrivateKey {
	return cc.key
}

func (ca *CA) CreateCertPool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	return pool
}

func (ca *CA) SignCSR(csrPEM []byte, workerID string, validFor time.Duration) ([]byte, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("failed to decode CSR PEM")
	}

	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CSR: %w", err)
	}

	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("CSR signature verification failed: %w", err)
	}

	serialNumber, err := generateSecureSerialNumber()
	if err != nil {
		return nil, fmt.Errorf("failed to generate CSR certificate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization:  ca.cert.Subject.Organization,
			Country:       ca.cert.Subject.Country,
			Province:      ca.cert.Subject.Province,
			Locality:      ca.cert.Subject.Locality,
			StreetAddress: ca.cert.Subject.StreetAddress,
			PostalCode:    ca.cert.Subject.PostalCode,
			CommonName:    workerID,
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(validFor),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, template, ca.cert, csr.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certBytes,
	})

	return certPEM, nil
}

func (p *PKI) GetCACertPEM() []byte {
	return p.ca.GetCertPEM()
}

func (p *PKI) SignWorkerCSR(csrPEM []byte, workerID string) ([]byte, error) {
	return p.ca.SignCSR(csrPEM, workerID, 30*24*time.Hour)
}

func (p *PKI) GenerateWorkerCertificate(workerID string) ([]byte, []byte, error) {
	cert, err := p.ca.NewClientCert(workerID, 30*24*time.Hour)
	if err != nil {
		return nil, nil, err
	}
	return cert.GetCertPEM(), cert.GetKeyPEM(), nil
}

type PKI struct {
	ca *CA
}

func NewPKI() (*PKI, error) {
	ca, err := loadOrCreateCA()
	if err != nil {
		return nil, fmt.Errorf("failed to load or create CA: %w", err)
	}

	return &PKI{
		ca: ca,
	}, nil
}

func loadOrCreateCA() (*CA, error) {
	caCertEnv := os.Getenv("CA_CERT")
	caKeyEnv := os.Getenv("CA_KEY")

	if caCertEnv != "" && caKeyEnv != "" {
		log.Printf("Loading CA certificate from environment variables")
		return loadCAFromEnv(caCertEnv, caKeyEnv)
	}

	dataDir := os.Getenv("OPENSEER_DATA_DIR")
	if dataDir == "" {
		if os.Getuid() == 0 {
			dataDir = "/var/lib/openseer"
		} else {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("failed to get home directory: %w", err)
			}
			dataDir = fmt.Sprintf("%s/.openseer", homeDir)
		}
	}

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	caPath := fmt.Sprintf("%s/ca.pem", dataDir)
	keyPath := fmt.Sprintf("%s/ca-key.pem", dataDir)

	if fileExists(caPath) && fileExists(keyPath) {
		log.Printf("Loading existing CA certificate from %s", caPath)
		return loadCA(caPath, keyPath)
	}

	log.Printf("Creating new CA certificate at %s", caPath)
	ca, err := NewCA("OpenSeer", 10*365*24*time.Hour)
	if err != nil {
		return nil, err
	}

	if err := saveCA(ca, caPath, keyPath); err != nil {
		return nil, fmt.Errorf("failed to save CA: %w", err)
	}

	return ca, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadCAFromEnv(certPEM, keyPEM string) (*CA, error) {
	certBlock, _ := pem.Decode([]byte(certPEM))
	if certBlock == nil {
		return nil, fmt.Errorf("failed to decode CA certificate PEM from environment")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate from environment: %w", err)
	}

	keyBlock, _ := pem.Decode([]byte(keyPEM))
	if keyBlock == nil {
		return nil, fmt.Errorf("failed to decode CA key PEM from environment")
	}

	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA key from environment: %w", err)
	}

	return &CA{
		cert:    cert,
		key:     key,
		certPEM: []byte(certPEM),
		keyPEM:  []byte(keyPEM),
	}, nil
}

func loadCA(certPath, keyPath string) (*CA, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA certificate: %w", err)
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA key: %w", err)
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("failed to decode CA certificate PEM")
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("failed to decode CA key PEM")
	}

	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA key: %w", err)
	}

	return &CA{
		cert:    cert,
		key:     key,
		certPEM: certPEM,
		keyPEM:  keyPEM,
	}, nil
}

func saveCA(ca *CA, certPath, keyPath string) error {
	if err := os.WriteFile(certPath, ca.certPEM, 0600); err != nil {
		return fmt.Errorf("failed to write CA certificate: %w", err)
	}

	if err := os.WriteFile(keyPath, ca.keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write CA key: %w", err)
	}

	return nil
}
