package lists

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const maxListSize = 4 << 20

type downloader struct {
	attempts int
	client   *http.Client
}

func newDownloader(timeout time.Duration, attempts int) (*downloader, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	const entwareBundle = "/opt/etc/ssl/certs/ca-certificates.crt"
	if pem, err := os.ReadFile(entwareBundle); err == nil {
		pool, _ := x509.SystemCertPool()
		if pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s: no valid CA certificates", entwareBundle)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || len(via) >= 10 {
				return fmt.Errorf("refusing redirect to %s", req.URL.Redacted())
			}
			return nil
		},
	}
	return &downloader{attempts: attempts, client: client}, nil
}

func (s *downloader) load(ctx context.Context, endpoint string) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= s.attempts; attempt++ {
		body, retry, err := s.get(ctx, endpoint)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry || attempt == s.attempts {
			break
		}

		timer := time.NewTimer(time.Duration(min(attempt, 6)) * 500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("download %s: %w", endpoint, lastErr)
}

func (s *downloader) get(ctx context.Context, endpoint string) ([]byte, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", "keenetic-geosite-sync")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500,
			fmt.Errorf("HTTP %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListSize+1))
	if err != nil {
		return nil, ctx.Err() == nil, err
	}
	if len(body) > maxListSize {
		return nil, false, fmt.Errorf("list exceeds %d bytes", maxListSize)
	}
	return body, false, nil
}
