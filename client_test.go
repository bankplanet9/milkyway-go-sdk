package milkyway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// newTestClient builds a Client pointed at the given API server and a stub token
// endpoint that always returns a token, with retry sleeps elided for speed.
func newTestClient(t *testing.T, apiURL string, tokenHandler http.HandlerFunc, optFns ...Option) *Client {
	t.Helper()
	if tokenHandler == nil {
		tokenHandler = func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "tok", TokenType: "Bearer", ExpiresIn: 3600})
		}
	}
	tokenSrv := httptest.NewServer(tokenHandler)
	t.Cleanup(tokenSrv.Close)

	opts := Options{
		BaseURL:      apiURL,
		TokenURL:     tokenSrv.URL,
		ClientID:     "cid",
		ClientSecret: "secret",
	}
	c, err := NewClient(opts, optFns...)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	// Elide real backoff sleeps in tests.
	if rt, ok := c.http.Transport.(*authTransport); ok {
		if retry, ok := rt.next.(*retryTransport); ok {
			retry.sleep = func(ctx context.Context, d time.Duration) error { return nil }
		}
	}
	return c
}

func TestNewClientValidation(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"missing base", Options{TokenURL: "t", ClientID: "c", ClientSecret: "s"}},
		{"missing token", Options{BaseURL: "b", ClientID: "c", ClientSecret: "s"}},
		{"missing client id", Options{BaseURL: "b", TokenURL: "t", ClientSecret: "s"}},
		{"missing secret", Options{BaseURL: "b", TokenURL: "t", ClientID: "c"}},
		{"negative retries", Options{BaseURL: "b", TokenURL: "t", ClientID: "c", ClientSecret: "s", MaxRetries: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewClient(tc.opts); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestHealthcheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("third_party_id"); got != "bankA" {
			t.Errorf("third_party_id = %q", got)
		}
		if got := r.URL.Query().Get("service_id"); got != "svc" {
			t.Errorf("service_id = %q", got)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing bearer: %q", r.Header.Get("Authorization"))
		}
		fmt.Fprint(w, "OK")
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	got, err := c.Healthcheck(context.Background(), "bankA", "svc")
	if err != nil {
		t.Fatal(err)
	}
	if got != "OK" {
		t.Fatalf("status = %q", got)
	}
}

func TestPrecheckDecimalPrecision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Echo a body with a precise rate that float64 would corrupt.
		body, _ := io.ReadAll(r.Body)
		var req PrecheckRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("unmarshal request: %v", err)
		}
		if !req.AmountCredit.Equal(decimal.RequireFromString("100.55")) {
			t.Errorf("amount_credit = %s", req.AmountCredit)
		}
		// Ensure money serialized as a JSON number, not a string.
		if !strings.Contains(string(body), `"amount_credit":100.55`) {
			t.Errorf("amount not a JSON number: %s", body)
		}
		fmt.Fprint(w, `{"third_party_id":"bankA","amount_credit":100.55,"amount_debit":369.0123456789,"rate":3.6701234567,"commission":1.5075,"currency_credit":"USD","currency_debit":"AED"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	res, err := c.Precheck(context.Background(), PrecheckRequest{
		ThirdPartyIDDebit: "bankA",
		ServiceID:         "svc",
		RecipientID:       "r1",
		AmountCredit:      decimal.RequireFromString("100.55"),
		CurrencyCredit:    "USD",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Rate.Equal(decimal.RequireFromString("3.6701234567")) {
		t.Errorf("rate lost precision: %s", res.Rate)
	}
	if !res.AmountDebit.Equal(decimal.RequireFromString("369.0123456789")) {
		t.Errorf("amount_debit lost precision: %s", res.AmountDebit)
	}
	if !res.Commission.Equal(decimal.RequireFromString("1.5075")) {
		t.Errorf("commission lost precision: %s", res.Commission)
	}
}

func TestPrecheckOmitsNilData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "data") {
			t.Errorf("nil data should be omitted: %s", body)
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, nil)
	if _, err := c.Precheck(context.Background(), PrecheckRequest{ServiceID: "svc"}); err != nil {
		t.Fatal(err)
	}
}

func TestPayWithIdempotencyKey(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		if r.Header.Get("Idempotency-Key") != "idem-123" {
			t.Errorf("missing idempotency key: %q", r.Header.Get("Idempotency-Key"))
		}
		fmt.Fprint(w, `{"transaction_id":987}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	id, err := c.Pay(context.Background(), PayRequest{ServiceID: "svc", AmountCredit: decimal.NewFromInt(10)}, "idem-123")
	if err != nil {
		t.Fatal(err)
	}
	if id != 987 {
		t.Fatalf("transaction id = %d", id)
	}
}

func TestPayWithoutKeyNotRetried(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	_, err := c.Pay(context.Background(), PayRequest{ServiceID: "svc"}, "")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("expected ErrServiceUnavailable, got %v", err)
	}
	if n := atomic.LoadInt32(&attempts); n != 1 {
		t.Fatalf("pay without key retried %d times (want 1)", n)
	}
}

func TestPayWithKeyRetriedOn5xx(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"transaction_id":55}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	id, err := c.Pay(context.Background(), PayRequest{ServiceID: "svc"}, "k")
	if err != nil {
		t.Fatal(err)
	}
	if id != 55 {
		t.Fatalf("id = %d", id)
	}
	if n := atomic.LoadInt32(&attempts); n != 3 {
		t.Fatalf("attempts = %d (want 3)", n)
	}
}

func TestRetryOn5xxThenSuccess(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `{"transaction_id":1,"status":1}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	res, err := c.Postcheck(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusDone {
		t.Fatalf("status = %v", res.Status)
	}
	if n := atomic.LoadInt32(&attempts); n != 2 {
		t.Fatalf("attempts = %d (want 2)", n)
	}
}

func TestRetryExhaustedReturns5xx(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"down"}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil, WithMaxRetries(2))
	_, err := c.Postcheck(context.Background(), 1)
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("err = %v", err)
	}
	// 1 initial + 2 retries.
	if n := atomic.LoadInt32(&attempts); n != 3 {
		t.Fatalf("attempts = %d (want 3)", n)
	}
}

func TestNoRetryOnDeterministic(t *testing.T) {
	cases := []struct {
		status   int
		sentinel error
		// wantCalls is the server hits expected: 401 triggers one auth refresh-replay
		// (still no retry-transport retries); the rest are sent exactly once.
		wantCalls int32
	}{
		{http.StatusBadRequest, ErrValidation, 1},
		{http.StatusUnauthorized, ErrAuth, 2},
		{http.StatusPaymentRequired, ErrExposureBlocked, 1},
		{http.StatusNotFound, ErrNotFound, 1},
	}
	for _, tc := range cases {
		t.Run(tc.sentinel.Error(), func(t *testing.T) {
			var attempts int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&attempts, 1)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"error":"nope"}`)
			}))
			defer srv.Close()

			c := newTestClient(t, srv.URL, nil)
			_, err := c.Postcheck(context.Background(), 1)
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("err = %v, want %v", err, tc.sentinel)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
				t.Fatalf("APIError status = %v", err)
			}
			if apiErr.Message != "nope" {
				t.Fatalf("message = %q", apiErr.Message)
			}
			if n := atomic.LoadInt32(&attempts); n != tc.wantCalls {
				t.Fatalf("%d: server hit %d times (want %d)", tc.status, n, tc.wantCalls)
			}
		})
	}
}

func TestExposureBlockedNotRetried(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusPaymentRequired)
		fmt.Fprint(w, `{"error":"limit"}`)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, nil)
	_, err := c.Pay(context.Background(), PayRequest{ServiceID: "svc"}, "k")
	if !errors.Is(err, ErrExposureBlocked) {
		t.Fatalf("err = %v", err)
	}
	if n := atomic.LoadInt32(&attempts); n != 1 {
		t.Fatalf("402 retried %d times", n)
	}
}

func TestStatusEnumMappingAndTerminal(t *testing.T) {
	cases := []struct {
		status   TransactionStatus
		terminal bool
		name     string
	}{
		{StatusPending, false, "Pending"},
		{StatusDone, true, "Done"},
		{StatusCancelled, true, "Cancelled"},
		{StatusFailed, true, "Failed"},
		{StatusCancelPending, false, "CancelPending"},
		{StatusStuck, true, "Stuck"},
	}
	for _, tc := range cases {
		if tc.status.IsTerminal() != tc.terminal {
			t.Errorf("%v IsTerminal = %v", tc.status, tc.status.IsTerminal())
		}
		if tc.status.String() != tc.name {
			t.Errorf("%d String = %q", tc.status, tc.status.String())
		}
	}
}

func TestCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"transaction_id":42`) {
			t.Errorf("body = %s", body)
		}
		fmt.Fprint(w, `{"status":2}`)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, nil)
	st, err := c.Cancel(context.Background(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if st != StatusCancelled {
		t.Fatalf("status = %v", st)
	}
}

func TestParseError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `not json`)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, nil)
	_, err := c.Postcheck(context.Background(), 1)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.Err == nil {
		t.Fatal("expected wrapped parse error")
	}
}
