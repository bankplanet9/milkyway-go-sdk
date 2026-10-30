package milkyway

import (
	"errors"
	"fmt"
	"strings"
)

// APIError is the base error for every failure returned by the MilkyWay Payments
// API. It carries the HTTP status code and the server's "error" message when
// present. The typed sentinels (ErrValidation, ErrAuth, ErrExposureBlocked,
// ErrNotFound, ErrPaymentRejected, ErrRecipientBankError, ErrServiceUnavailable)
// classify it for errors.Is.
type APIError struct {
	// StatusCode is the HTTP status returned by the API (0 if the request never completed).
	StatusCode int
	// Message is the parsed server "error" message, or a synthesized description.
	Message string
	// Code is the machine-readable error code leading the server's "error" message
	// (e.g. "ErrPaymentRejected", "ErrRecipientBankError", "ErrValidationFailed"),
	// or empty when the message does not start with one.
	Code string
	// Field is the request field at fault on a field-level 400, by its JSON name or
	// path (e.g. "amount_credit", "sender_info.date_of_birth", or "recipient_id" when
	// the receiving bank reports the recipient invalid); empty otherwise.
	Field string
	// ServiceID is the downstream service that failed, carried by a 503 body; empty otherwise.
	ServiceID string
	// ThirdPartyID is the downstream institution that failed, carried by a 503 body;
	// empty otherwise.
	ThirdPartyID string
	// ResponseBody is the raw response body, useful for diagnostics.
	ResponseBody string
	// Err is an optional wrapped cause (e.g. a JSON parse or network error).
	Err error
}

func (e *APIError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("milkyway: API error (status %d): %s", e.StatusCode, e.Message)
	}
	return "milkyway: API error: " + e.Message
}

func (e *APIError) Unwrap() error { return e.Err }

// Sentinel errors for classifying an *APIError via errors.Is.
var (
	// ErrValidation indicates HTTP 400 — request validation failed.
	ErrValidation = errors.New("milkyway: validation failed")
	// ErrAuth indicates HTTP 401 — missing, malformed, or invalid access token
	// (including a failed token acquisition).
	ErrAuth = errors.New("milkyway: authentication failed")
	// ErrExposureBlocked indicates HTTP 402 — the payment would breach a block-action
	// exposure limit.
	ErrExposureBlocked = errors.New("milkyway: payment blocked by exposure limit")
	// ErrNotFound indicates HTTP 404 — transaction not found or not owned by your institution.
	ErrNotFound = errors.New("milkyway: not found")
	// ErrPaymentRejected indicates HTTP 422 — the receiving bank refused the payment on
	// its merits (e.g. a limit or a blocked account). Nothing was created or debited,
	// and repeating the request will not change the answer, so it is never retried.
	ErrPaymentRejected = errors.New("milkyway: payment rejected by the receiving bank")
	// ErrRecipientBankError indicates HTTP 502 — the receiving bank's connector failed
	// in a way MilkyWay could not classify. Nothing was created or debited. A 502 also
	// matches ErrServiceUnavailable, so existing 5xx handling keeps working.
	ErrRecipientBankError = errors.New("milkyway: receiving bank error")
	// ErrServiceUnavailable indicates HTTP 5xx — the API or a downstream service is unavailable.
	ErrServiceUnavailable = errors.New("milkyway: service unavailable")
)

// Reason codes the API reports in PostcheckResult.Error for a terminal outcome, and
// in APIError.Code for the matching error responses. The set may grow; treat an
// unknown code like ReasonPaymentRejected.
const (
	// ReasonPaymentRejected: the receiving bank declined the payment (also used for
	// any reason it gave without a code).
	ReasonPaymentRejected = "ErrPaymentRejected"
	// ReasonRecipientNotFound: the receiving bank does not know the recipient.
	ReasonRecipientNotFound = "ErrRecipientNotFound"
	// ReasonRecipientInvalid: the receiving bank reports the recipient invalid.
	ReasonRecipientInvalid = "ErrRecipientInvalid"
	// ReasonServiceUnavailable: the receiving bank's service was unavailable.
	ReasonServiceUnavailable = "ErrServiceUnavailable"
	// ReasonPaymentCannotBeCanceled: the payment can no longer be cancelled.
	ReasonPaymentCannotBeCanceled = "ErrPaymentCannotBeCanceled"
)

// Is reports whether the error matches one of the SDK sentinels based on its
// status code, so callers can write errors.Is(err, milkyway.ErrNotFound). A 502
// matches both ErrRecipientBankError and ErrServiceUnavailable.
func (e *APIError) Is(target error) bool {
	switch e.StatusCode {
	case 400:
		return target == ErrValidation
	case 401:
		return target == ErrAuth
	case 402:
		return target == ErrExposureBlocked
	case 404:
		return target == ErrNotFound
	case 422:
		return target == ErrPaymentRejected
	case 502:
		return target == ErrRecipientBankError || target == ErrServiceUnavailable
	}
	return e.StatusCode >= 500 && target == ErrServiceUnavailable
}

// errorCode returns the leading "Err..." identifier of a server error message
// ("ErrValidationFailed: amount_credit: ..." yields "ErrValidationFailed"), or ""
// when the message does not start with one.
func errorCode(message string) string {
	code, _, _ := strings.Cut(message, ":")
	code = strings.TrimSpace(code)
	if !strings.HasPrefix(code, "Err") {
		return ""
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return ""
		}
	}
	return code
}

func newAPIError(status int, message, body string, cause error) *APIError {
	return &APIError{StatusCode: status, Message: message, ResponseBody: body, Err: cause}
}
