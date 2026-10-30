package milkyway

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"net/http"
	"time"
)

// suppressRetryKey is a context flag marking a request that must not be auto-retried
// (a non-idempotent pay without an Idempotency-Key).
type suppressRetryKey struct{}

func withSuppressRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, suppressRetryKey{}, true)
}

func isRetrySuppressed(ctx context.Context) bool {
	v, _ := ctx.Value(suppressRetryKey{}).(bool)
	return v
}

// authTransport attaches a bearer token to every outgoing request. On a 401 it
// forces a single token refresh and replays the request once, covering a cached
// token that was revoked or expired early.
type authTransport struct {
	provider *keycloakTokenProvider
	next     http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer the body so the request can be replayed on a 401.
	body, err := drainBody(req)
	if err != nil {
		return nil, err
	}

	token, err := t.provider.getAccessToken(req.Context(), false)
	if err != nil {
		return nil, err
	}
	attempt := cloneWithBody(req, body)
	attempt.Header.Set("Authorization", "Bearer "+token)

	resp, err := t.next.RoundTrip(attempt)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}

	// Stale/revoked token: refresh once and replay on a fresh request clone.
	resp.Body.Close()
	token, err = t.provider.getAccessToken(req.Context(), true)
	if err != nil {
		return nil, err
	}
	replay := cloneWithBody(req, body)
	replay.Header.Set("Authorization", "Bearer "+token)
	return t.next.RoundTrip(replay)
}

// retryTransport applies a per-attempt timeout wrapped by exponential-backoff-with-
// jitter retries on transient failures (HTTP 5xx, 408, and network errors).
// Deterministic outcomes (400/401/402/404/422) are never retried, and a request flagged
// via withSuppressRetry bypasses retries entirely while keeping the per-attempt timeout.
type retryTransport struct {
	next           http.RoundTripper
	maxRetries     int
	baseDelay      time.Duration
	requestTimeout time.Duration
	rand           *rand.Rand
	// sleep is a seam for tests; defaults to time.Sleep honoring ctx.
	sleep func(ctx context.Context, d time.Duration) error
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := drainBody(req)
	if err != nil {
		return nil, err
	}

	suppressed := isRetrySuppressed(req.Context())
	maxAttempts := 1
	if !suppressed {
		maxAttempts = t.maxRetries + 1
	}

	var lastResp *http.Response
	var lastErr error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := t.sleepFor(req.Context(), attempt); err != nil {
				return nil, err
			}
		}

		ctx, cancel := context.WithTimeout(req.Context(), t.requestTimeout)
		resp, err := t.next.RoundTrip(cloneWithBody(req.WithContext(ctx), body))

		if err != nil {
			cancel()
			// Caller cancellation/deadline is not a transient failure.
			if req.Context().Err() != nil {
				return nil, req.Context().Err()
			}
			lastResp, lastErr = nil, err
			if attempt == maxAttempts-1 {
				return nil, err
			}
			continue
		}

		if !isTransientStatus(resp.StatusCode) || attempt == maxAttempts-1 {
			// Tie the per-attempt timeout context to the response body lifetime.
			resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}

		// Transient and more attempts remain: discard and retry.
		resp.Body.Close()
		cancel()
		lastResp, lastErr = resp, nil
	}

	return lastResp, lastErr
}

func (t *retryTransport) sleepFor(ctx context.Context, attempt int) error {
	// Exponential backoff with full jitter: base * 2^(attempt-1), then random in [0,d].
	backoff := t.baseDelay * time.Duration(1<<(attempt-1))
	jittered := time.Duration(t.rand.Int63n(int64(backoff) + 1))
	return t.sleep(ctx, jittered)
}

func isTransientStatus(status int) bool {
	return status >= 500 || status == http.StatusRequestTimeout
}

// cancelOnCloseBody cancels the per-attempt timeout context when the body is closed.
type cancelOnCloseBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b *cancelOnCloseBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

func drainBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(req.Body)
	req.Body.Close()
	if err != nil {
		return nil, err
	}
	return b, nil
}

func cloneWithBody(req *http.Request, body []byte) *http.Request {
	c := req.Clone(req.Context())
	if body != nil {
		c.Body = io.NopCloser(bytes.NewReader(body))
		c.ContentLength = int64(len(body))
		c.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	} else {
		c.Body = nil
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
