package milkyway

import (
	"errors"
	"fmt"
)

// APIError is the base error for every failure returned by the MilkyWay Payments
// API. It carries the HTTP status code and the server's "error" message when
// present. The typed sentinels (ErrValidation, ErrAuth, ErrExposureBlocked,
// ErrNotFound, ErrServiceUnavailable) classify it for errors.Is.
type APIError struct {
	// StatusCode is the HTTP status returned by the API (0 if the request never completed).
	StatusCode int
	// Message is the parsed server "error" message, or a synthesized description.
	Message string
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
	// ErrServiceUnavailable indicates HTTP 5xx — the API or a downstream service is unavailable.
	ErrServiceUnavailable = errors.New("milkyway: service unavailable")
)

// kind returns the sentinel that classifies this status, or nil for an unmapped one.
func sentinelFor(status int) error {
	switch status {
	case 400:
		return ErrValidation
	case 401:
		return ErrAuth
	case 402:
		return ErrExposureBlocked
	case 404:
		return ErrNotFound
	default:
		if status >= 500 {
			return ErrServiceUnavailable
		}
		return nil
	}
}

// Is reports whether the error matches one of the SDK sentinels based on its
// status code, so callers can write errors.Is(err, milkyway.ErrNotFound).
func (e *APIError) Is(target error) bool {
	s := sentinelFor(e.StatusCode)
	return s != nil && target == s
}

func newAPIError(status int, message, body string, cause error) *APIError {
	return &APIError{StatusCode: status, Message: message, ResponseBody: body, Err: cause}
}
