package milkyway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// tokenResponse is the subset of the OAuth2 token endpoint response we consume.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// keycloakTokenProvider acquires access tokens via the Keycloak client-credentials
// grant and caches them until shortly before expiry. Concurrent callers share a
// single in-flight acquisition (single-flight) via a mutex so a burst of requests
// triggers at most one token fetch.
type keycloakTokenProvider struct {
	httpClient *http.Client
	opts       *Options

	// now is a clock seam for tests; defaults to time.Now.
	now func() time.Time

	mu          sync.Mutex
	cachedToken string
	expiresAt   time.Time
}

func newKeycloakTokenProvider(httpClient *http.Client, opts *Options) *keycloakTokenProvider {
	return &keycloakTokenProvider{
		httpClient: httpClient,
		opts:       opts,
		now:        time.Now,
	}
}

// getAccessToken returns a cached token when valid, otherwise acquires a fresh one.
// forceRefresh bypasses the cache (used by the 401 refresh-replay path). The whole
// acquisition runs under the mutex so concurrent callers coalesce onto one fetch.
func (p *keycloakTokenProvider) getAccessToken(ctx context.Context, forceRefresh bool) (string, error) {
	if !forceRefresh {
		if tok, ok := p.tryCached(); ok {
			return tok, nil
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Re-check under the lock: another caller may have refreshed while we waited.
	if !forceRefresh && p.validLocked() {
		return p.cachedToken, nil
	}
	return p.acquireLocked(ctx)
}

func (p *keycloakTokenProvider) tryCached() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.validLocked() {
		return p.cachedToken, true
	}
	return "", false
}

// validLocked reports whether the cached token is present and not within the
// refresh-skew window of expiry. Caller must hold p.mu.
func (p *keycloakTokenProvider) validLocked() bool {
	return p.cachedToken != "" && p.now().Before(p.expiresAt.Add(-p.opts.TokenRefreshSkew))
}

// acquireLocked fetches a new token from Keycloak. Caller must hold p.mu.
func (p *keycloakTokenProvider) acquireLocked(ctx context.Context) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "client_credentials")
	form.Set("client_id", p.opts.ClientID)
	form.Set("client_secret", p.opts.ClientSecret)
	if strings.TrimSpace(p.opts.Scope) != "" {
		form.Set("scope", p.opts.Scope)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.opts.TokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", &APIError{StatusCode: 401, Message: "failed to build token request", Err: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &APIError{StatusCode: 401, Message: "failed to reach the Keycloak token endpoint", Err: err}
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &APIError{
			StatusCode:   401,
			Message:      fmt.Sprintf("token acquisition failed with status %d", resp.StatusCode),
			ResponseBody: string(body),
		}
	}

	var parsed tokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", &APIError{StatusCode: 401, Message: "token endpoint returned a malformed response",
			ResponseBody: string(body), Err: err}
	}
	if parsed.AccessToken == "" {
		return "", &APIError{StatusCode: 401, Message: "token endpoint response contained no access_token",
			ResponseBody: string(body)}
	}

	p.cachedToken = parsed.AccessToken
	p.expiresAt = p.now().Add(time.Duration(parsed.ExpiresIn) * time.Second)
	return parsed.AccessToken, nil
}
