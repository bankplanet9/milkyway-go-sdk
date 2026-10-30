package milkyway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"
)

// countingServer answers every request with status and body, counting the hits.
func countingServer(t *testing.T, status int, body string) (*httptest.Server, *int32) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func precheckReq() PrecheckRequest {
	return PrecheckRequest{
		ThirdPartyIDDebit: "bankB",
		ServiceID:         "svc",
		RecipientID:       "r1",
		AmountCredit:      decimal.RequireFromString("100"),
		CurrencyCredit:    "USD",
	}
}

func TestPaymentRejectedNotRetried(t *testing.T) {
	calls := map[string]func(c *Client) error{
		"precheck": func(c *Client) error { _, err := c.Precheck(context.Background(), precheckReq()); return err },
		"pay with key": func(c *Client) error {
			_, err := c.Pay(context.Background(), PayRequest{ServiceID: "svc"}, "k")
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			srv, hits := countingServer(t, http.StatusUnprocessableEntity, `{"error":"ErrPaymentRejected"}`)
			err := call(newTestClient(t, srv.URL, nil))
			if !errors.Is(err, ErrPaymentRejected) {
				t.Fatalf("err = %v, want ErrPaymentRejected", err)
			}
			if errors.Is(err, ErrValidation) || errors.Is(err, ErrServiceUnavailable) {
				t.Fatalf("422 matched another sentinel: %v", err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("APIError = %v", err)
			}
			if apiErr.Code != ReasonPaymentRejected {
				t.Fatalf("Code = %q", apiErr.Code)
			}
			if n := atomic.LoadInt32(hits); n != 1 {
				t.Fatalf("422 sent %d times (want 1)", n)
			}
		})
	}
}

func TestRecipientBankErrorMatchesBothSentinels(t *testing.T) {
	calls := map[string]func(c *Client) error{
		"precheck": func(c *Client) error { _, err := c.Precheck(context.Background(), precheckReq()); return err },
		"pay with key": func(c *Client) error {
			_, err := c.Pay(context.Background(), PayRequest{ServiceID: "svc"}, "k")
			return err
		},
		"cancel": func(c *Client) error { _, err := c.Cancel(context.Background(), 42); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			srv, hits := countingServer(t, http.StatusBadGateway, `{"error":"ErrRecipientBankError"}`)
			err := call(newTestClient(t, srv.URL, nil, WithMaxRetries(2)))
			if !errors.Is(err, ErrRecipientBankError) {
				t.Fatalf("err = %v, want ErrRecipientBankError", err)
			}
			if !errors.Is(err, ErrServiceUnavailable) {
				t.Fatalf("502 must still match ErrServiceUnavailable: %v", err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "ErrRecipientBankError" {
				t.Fatalf("APIError = %#v", apiErr)
			}
			// 502 stays transient: 1 initial + 2 retries.
			if n := atomic.LoadInt32(hits); n != 3 {
				t.Fatalf("502 sent %d times (want 3)", n)
			}
		})
	}
}

func TestPayWithoutKeyNotRetriedOn502(t *testing.T) {
	srv, hits := countingServer(t, http.StatusBadGateway, `{"error":"ErrRecipientBankError"}`)
	_, err := newTestClient(t, srv.URL, nil).Pay(context.Background(), PayRequest{ServiceID: "svc"}, "")
	if !errors.Is(err, ErrRecipientBankError) {
		t.Fatalf("err = %v", err)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("pay without key sent %d times (want 1)", n)
	}
}

func TestOther5xxIsNotRecipientBankError(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		err := &APIError{StatusCode: status}
		if !errors.Is(err, ErrServiceUnavailable) {
			t.Errorf("%d: want ErrServiceUnavailable", status)
		}
		if errors.Is(err, ErrRecipientBankError) {
			t.Errorf("%d: must not match ErrRecipientBankError", status)
		}
	}
}

func TestCancelServiceUnavailableCarriesServiceAndThirdParty(t *testing.T) {
	srv, _ := countingServer(t, http.StatusServiceUnavailable,
		`{"error":"ErrServiceUnavailable","service_id":"wallet","third_party_id":"recipient-bank"}`)
	_, err := newTestClient(t, srv.URL, nil).Cancel(context.Background(), 42)
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, ErrRecipientBankError) {
		t.Fatalf("503 must not match ErrRecipientBankError")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.Code != ReasonServiceUnavailable || apiErr.ServiceID != "wallet" || apiErr.ThirdPartyID != "recipient-bank" {
		t.Fatalf("APIError = %#v", apiErr)
	}
}

func TestValidationErrorExposesFieldAndCode(t *testing.T) {
	cases := []struct {
		body, code, field string
	}{
		{`{"error":"ErrRecipientInvalid","field":"recipient_id"}`, ReasonRecipientInvalid, "recipient_id"},
		{`{"error":"ErrValidationFailed: currency_credit: not a settlement currency","field":"currency_credit"}`, "ErrValidationFailed", "currency_credit"},
		{`{"error":"sender_info.date_of_birth: is required by the receiving bank for this service","field":"sender_info.date_of_birth"}`, "", "sender_info.date_of_birth"},
	}
	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			srv, _ := countingServer(t, http.StatusBadRequest, tc.body)
			_, err := newTestClient(t, srv.URL, nil).Precheck(context.Background(), precheckReq())
			var apiErr *APIError
			if !errors.Is(err, ErrValidation) || !errors.As(err, &apiErr) {
				t.Fatalf("err = %v", err)
			}
			if apiErr.Code != tc.code || apiErr.Field != tc.field {
				t.Fatalf("Code = %q, Field = %q", apiErr.Code, apiErr.Field)
			}
		})
	}
}

func TestErrorCode(t *testing.T) {
	cases := map[string]string{
		"ErrPaymentRejected": "ErrPaymentRejected",
		"ErrRecipientRateUnavailable: receiving bank quoted no usable FX rate": "ErrRecipientRateUnavailable",
		"boom":               "",
		"":                   "",
		"Err with spaces: x": "",
		"request failed":     "",
	}
	for in, want := range cases {
		if got := errorCode(in); got != want {
			t.Errorf("errorCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPostcheckReasonCodes(t *testing.T) {
	reasons := []string{
		ReasonPaymentRejected,
		ReasonRecipientNotFound,
		ReasonRecipientInvalid,
		ReasonServiceUnavailable,
		ReasonPaymentCannotBeCanceled,
	}
	want := []string{
		"ErrPaymentRejected",
		"ErrRecipientNotFound",
		"ErrRecipientInvalid",
		"ErrServiceUnavailable",
		"ErrPaymentCannotBeCanceled",
	}
	for i, reason := range reasons {
		if reason != want[i] {
			t.Fatalf("reason %d = %q, want %q", i, reason, want[i])
		}
		srv, _ := countingServer(t, http.StatusOK, fmt.Sprintf(`{"transaction_id":7,"status":3,"error":%q}`, reason))
		res, err := newTestClient(t, srv.URL, nil).Postcheck(context.Background(), 7)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != StatusFailed || res.Error != reason {
			t.Fatalf("result = %+v", res)
		}
	}
}
