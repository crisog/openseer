package helpers

import (
	"crypto/tls"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

func NewHTTP2Client(tlsConfig *tls.Config, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http2.Transport{TLSClientConfig: tlsConfig},
		Timeout:   timeout,
	}
}
