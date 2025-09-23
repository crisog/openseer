package helpers

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"
)

func MustGenerateCSR(t *testing.T, commonName string) (string, *rsa.PrivateKey) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	csr := MustGenerateCSRWithKey(t, key, commonName)
	return csr, key
}

func MustGenerateCSRWithKey(t *testing.T, key *rsa.PrivateKey, commonName string) string {
	csrTemplate := &x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: commonName},
		SignatureAlgorithm: x509.SHA256WithRSA,
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, key)
	require.NoError(t, err)

	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	return string(csrPEM)
}
