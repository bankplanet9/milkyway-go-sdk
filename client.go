// Package milkyway is a Go client for the MilkyWay Payments API (/payments/v1).
//
// It handles Keycloak client-credentials auth (token caching, single-flight, and
// 401 refresh-replay), transient-failure retries with exponential backoff and
// jitter, decimal-exact money, typed errors, and a WaitForCompletion polling helper.
package milkyway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const paymentsPath = "payments/v1"

// Client is a MilkyWay Payments API client. Construct it with NewClient and share a
// single instance across goroutines — it is safe for concurrent use.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient builds a fully self-contained client: it assembles the auth + retry
// transport pipeline over the configured (or a default) http.Client and uses a
// separate, un-wrapped client for the Keycloak token endpoint to avoid recursion.
func NewClient(opts Options, optFns ...Option) (*Client, error) {
	for _, fn := range optFns {
		fn(&opts)
	}
	opts.applyDefaults()
	if err := opts.validate(); err != nil {
		return nil, err
	}

	// Plain client for the token endpoint — never routed through auth/retry.
	tokenHTTP := &http.Client{Timeout: opts.RequestTimeout}
	provider := newKeycloakTokenProvider(tokenHTTP, &opts)

	base := http.DefaultTransport
	apiClient := opts.HTTPClient
	if apiClient == nil {
		apiClient = &http.Client{}
	}
	if apiClient.Transport != nil {
		base = apiClient.Transport
	}

	retry := &retryTransport{
		next:           base,
		maxRetries:     opts.MaxRetries,
		baseDelay:      opts.RetryBaseDelay,
		requestTimeout: opts.RequestTimeout,
		rand:           rand.New(rand.NewSource(time.Now().UnixNano())),
		sleep:          sleepCtx,
	}
	auth := &authTransport{provider: provider, next: retry}

	apiClient.Transport = auth
	// Timeouts are enforced per-attempt by retryTransport; disable the client-wide one.
	apiClient.Timeout = 0

	return &Client{
		http:    apiClient,
		baseURL: strings.TrimRight(opts.BaseURL, "/") + "/",
	}, nil
}

// Healthcheck returns the recipient service status string. A 5xx maps to
// ErrServiceUnavailable when the recipient is down.
func (c *Client) Healthcheck(ctx context.Context, thirdPartyID, serviceID string) (string, error) {
	if thirdPartyID == "" {
		return "", errors.New("milkyway: thirdPartyID is required")
	}
	if serviceID == "" {
		return "", errors.New("milkyway: serviceID is required")
	}
	q := url.Values{}
	q.Set("third_party_id", thirdPartyID)
	q.Set("service_id", serviceID)
	uri := paymentsPath + "/healthcheck?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+uri, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", toError(resp.StatusCode, body)
	}
	return string(body), nil
}

// Discover lists the destination banks you can pay to, each with its payout
// services. Pass a non-empty country (ISO 3166-1 alpha-2, case-insensitive) to
// filter by destination country; pass "" to list all destinations.
func (c *Client) Discover(ctx context.Context, country string) ([]Destination, error) {
	uri := paymentsPath + "/discovery"
	if country != "" {
		q := url.Values{}
		q.Set("country", country)
		uri += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+uri, nil)
	if err != nil {
		return nil, err
	}
	var out discoveryResponse
	if err := c.sendForJSON(req, &out); err != nil {
		return nil, err
	}
	return out.Destinations, nil
}

// ServiceSchema returns the raw Draft-2020 JSON Schema that a payment's Data field
// must satisfy for the given destination bank and service.
func (c *Client) ServiceSchema(ctx context.Context, thirdPartyID, serviceID string) (json.RawMessage, error) {
	if thirdPartyID == "" {
		return nil, errors.New("milkyway: thirdPartyID is required")
	}
	if serviceID == "" {
		return nil, errors.New("milkyway: serviceID is required")
	}
	uri := paymentsPath + "/discovery/" + url.PathEscape(thirdPartyID) + "/" + url.PathEscape(serviceID) + "/schema"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+uri, nil)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	if err := c.sendForJSON(req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Precheck returns a payment quote (rate + commission) without creating a payment.
func (c *Client) Precheck(ctx context.Context, req PrecheckRequest) (*PrecheckResult, error) {
	httpReq, err := c.buildJSONPost(ctx, paymentsPath+"/precheck", req)
	if err != nil {
		return nil, err
	}
	var out PrecheckResult
	if err := c.sendForJSON(httpReq, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Pay initiates a cross-bank payment and returns the new transaction id. When
// idempotencyKey is empty the request is NOT auto-retried (duplicate-payment safety);
// passing a key both sets the Idempotency-Key header and makes the call retryable.
func (c *Client) Pay(ctx context.Context, req PayRequest, idempotencyKey string) (int64, error) {
	if idempotencyKey == "" {
		ctx = withSuppressRetry(ctx)
	}
	httpReq, err := c.buildJSONPost(ctx, paymentsPath, req)
	if err != nil {
		return 0, err
	}
	if idempotencyKey != "" {
		httpReq.Header.Set("Idempotency-Key", idempotencyKey)
	}
	var out payResponse
	if err := c.sendForJSON(httpReq, &out); err != nil {
		return 0, err
	}
	return out.TransactionID, nil
}

// Postcheck returns the current status of a transaction.
func (c *Client) Postcheck(ctx context.Context, transactionID int64) (*PostcheckResult, error) {
	uri := paymentsPath + "/postcheck?transaction_id=" + strconv.FormatInt(transactionID, 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+uri, nil)
	if err != nil {
		return nil, err
	}
	var out PostcheckResult
	if err := c.sendForJSON(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Cancel requests cancellation of a transaction and returns the resulting status.
func (c *Client) Cancel(ctx context.Context, transactionID int64) (TransactionStatus, error) {
	httpReq, err := c.buildJSONPost(ctx, paymentsPath+"/cancel", cancelRequest{TransactionID: transactionID})
	if err != nil {
		return 0, err
	}
	var out cancelResult
	if err := c.sendForJSON(httpReq, &out); err != nil {
		return 0, err
	}
	return out.Status, nil
}

// PollOptions controls WaitForCompletion polling.
type PollOptions struct {
	// InitialDelay is the delay before the first poll. Default 1s.
	InitialDelay time.Duration
	// MaxDelay caps the backoff between polls. Default 30s.
	MaxDelay time.Duration
	// BackoffMultiplier is applied to the delay after each poll. Default 2.0.
	BackoffMultiplier float64
	// Timeout is the overall polling budget; when exceeded the last observed status
	// is returned even if non-terminal. Default 5 minutes.
	Timeout time.Duration
}

func (o *PollOptions) withDefaults() PollOptions {
	out := PollOptions{
		InitialDelay:      time.Second,
		MaxDelay:          30 * time.Second,
		BackoffMultiplier: 2.0,
		Timeout:           5 * time.Minute,
	}
	if o == nil {
		return out
	}
	if o.InitialDelay > 0 {
		out.InitialDelay = o.InitialDelay
	}
	if o.MaxDelay > 0 {
		out.MaxDelay = o.MaxDelay
	}
	if o.BackoffMultiplier > 0 {
		out.BackoffMultiplier = o.BackoffMultiplier
	}
	if o.Timeout > 0 {
		out.Timeout = o.Timeout
	}
	return out
}

// WaitForCompletion polls Postcheck with exponential backoff until the transaction
// reaches a terminal status or the poll-timeout budget is exhausted (in which case
// the last observed result is returned, non-terminal). Pass nil for defaults.
func (c *Client) WaitForCompletion(ctx context.Context, transactionID int64, pollOpts *PollOptions) (*PostcheckResult, error) {
	opts := pollOpts.withDefaults()
	start := time.Now()
	delay := opts.InitialDelay

	result, err := c.Postcheck(ctx, transactionID)
	if err != nil {
		return nil, err
	}
	for !result.Status.IsTerminal() {
		if time.Since(start)+delay > opts.Timeout {
			return result, nil
		}
		if err := sleepCtx(ctx, delay); err != nil {
			return nil, err
		}
		next := float64(delay) * opts.BackoffMultiplier
		if next > float64(opts.MaxDelay) {
			next = float64(opts.MaxDelay)
		}
		delay = time.Duration(next)

		result, err = c.Postcheck(ctx, transactionID)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (c *Client) buildJSONPost(ctx context.Context, path string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *Client) sendForJSON(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return toError(resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return newAPIError(resp.StatusCode, "failed to parse the API response", string(body), err)
	}
	return nil
}

// toError maps a non-2xx response to a classified *APIError.
func toError(status int, body []byte) error {
	msg, field := extractErrorMessage(body)
	if msg == "" {
		msg = "request failed with status " + strconv.Itoa(status)
	}
	apiErr := newAPIError(status, msg, string(body), nil)
	apiErr.Field = field
	return apiErr
}

// extractErrorMessage pulls the JSON {"error": "...", "field": "..."} message and
// field, falling back to a trimmed non-JSON body (e.g. a plain-text 500 from
// healthcheck) as the message.
func extractErrorMessage(body []byte) (message, field string) {
	if len(bytes.TrimSpace(body)) == 0 {
		return "", ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return strings.TrimSpace(string(body)), ""
	}
	if raw, ok := obj["error"]; ok {
		_ = json.Unmarshal(raw, &message)
	}
	if raw, ok := obj["field"]; ok {
		_ = json.Unmarshal(raw, &field)
	}
	return message, field
}
