package worker

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"time"

	openseerv1 "github.com/crisog/openseer/gen/openseer/v1"
	"github.com/crisog/openseer/internal/pkg/recovery"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (w *Worker) executeCheck(ctx context.Context, job *openseerv1.MonitorJob) {
	jobCtx, cancel := context.WithTimeout(ctx, time.Duration(job.TimeoutMs)*time.Millisecond)
	defer cancel()

	w.mu.Lock()
	w.activeJobs[job.RunId] = cancel
	w.mu.Unlock()

	if job.TimeoutMs > 20000 {
		renewalCtx, renewalCancel := context.WithCancel(ctx)
		go recovery.WithRecover(
			fmt.Sprintf("lease-renewal-%s", job.RunId),
			func() { w.renewLease(renewalCtx, job.RunId) },
		)()
		defer renewalCancel()
	}

	start := time.Now()

	result := &openseerv1.MonitorResult{
		RunId:     job.RunId,
		MonitorId: job.MonitorId,
		Region:    w.region,
		EventAt:   timestamppb.New(time.Now()),
		Status:    "ERROR",
	}

	req, err := http.NewRequestWithContext(jobCtx, job.Method, job.Url, nil)
	if err != nil {
		msg := err.Error()
		result.ErrorMessage = &msg
		w.sendResult(result)
		return
	}

	for k, v := range job.Headers {
		req.Header.Set(k, v)
	}

	var dnsStart, dnsDone time.Time
	var connectStart, connectDone time.Time
	var tlsStart, tlsDone time.Time
	var firstByte time.Time

	trace := &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { dnsDone = time.Now() },
		ConnectStart:         func(string, string) { connectStart = time.Now() },
		ConnectDone:          func(string, string, error) { connectDone = time.Now() },
		TLSHandshakeStart:    func() { tlsStart = time.Now() },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { tlsDone = time.Now() },
		GotFirstResponseByte: func() { firstByte = time.Now() },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	resp, err := w.httpClient.Do(req)
	if err != nil {
		msg := err.Error()
		result.ErrorMessage = &msg
		total := int32(time.Since(start).Milliseconds())
		result.TotalMs = &total
		w.sendResult(result)
		return
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	httpCode := int32(resp.StatusCode)
	result.HttpCode = &httpCode

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		result.Status = "OK"
	} else {
		result.Status = "FAIL"
	}

	var dnsMs, connectMs, tlsMs, ttfbMs int64
	if !dnsStart.IsZero() && !dnsDone.IsZero() {
		dnsMs = dnsDone.Sub(dnsStart).Milliseconds()
	}
	if !connectStart.IsZero() && !connectDone.IsZero() {
		connectMs = connectDone.Sub(connectStart).Milliseconds()
	}
	if !tlsStart.IsZero() && !tlsDone.IsZero() {
		tlsMs = tlsDone.Sub(tlsStart).Milliseconds()
	}
	if !firstByte.IsZero() {
		ttfbMs = firstByte.Sub(start).Milliseconds()
	}

	readStart := time.Now()
	const maxResponseSize = 10 * 1024 * 1024
	limitedReader := &io.LimitedReader{R: resp.Body, N: maxResponseSize}
	size, _ := io.Copy(io.Discard, limitedReader)
	downloadMs := time.Since(readStart).Milliseconds()
	totalMs := time.Since(start).Milliseconds()

	if dnsMs > 0 {
		v := int32(dnsMs)
		result.DnsMs = &v
	}
	if connectMs > 0 {
		v := int32(connectMs)
		result.ConnectMs = &v
	}
	if tlsMs > 0 {
		v := int32(tlsMs)
		result.TlsMs = &v
	}
	if ttfbMs > 0 {
		v := int32(ttfbMs)
		result.TtfbMs = &v
	}
	vDownload := int32(downloadMs)
	result.DownloadMs = &vDownload
	vTotal := int32(totalMs)
	result.TotalMs = &vTotal
	if size > 0 {
		vSize := int64(size)
		result.SizeBytes = &vSize
	}

	w.sendResult(result)
}
