package milkyway

import "github.com/shopspring/decimal"

func init() {
	// The Payments API encodes money as JSON numbers (never strings). shopspring's
	// default marshals decimals as quoted strings; flip the package-global flag so
	// PrecheckRequest/PayRequest amounts serialize as numbers on the wire, matching
	// the backend and the reference C# SDK.
	decimal.MarshalJSONWithoutQuotes = true
}

// TransactionStatus is the lifecycle status of a payment transaction, mirroring the
// integer codes returned by the Payments API (postcheck / cancel).
type TransactionStatus int

const (
	// StatusPending: accepted, not yet completed. Keep polling.
	StatusPending TransactionStatus = 0
	// StatusDone: completed successfully (terminal).
	StatusDone TransactionStatus = 1
	// StatusCancelled: cancelled (terminal).
	StatusCancelled TransactionStatus = 2
	// StatusFailed: failed (terminal). Inspect the accompanying error message.
	StatusFailed TransactionStatus = 3
	// StatusCancelPending: cancellation requested, awaiting confirmation.
	StatusCancelPending TransactionStatus = 4
	// StatusStuck: timed out / stuck (terminal); requires operator attention.
	StatusStuck TransactionStatus = 5
)

// IsTerminal reports whether the status will not change without external action —
// Done, Cancelled, Failed, or Stuck. Polling should stop once it returns true.
func (s TransactionStatus) IsTerminal() bool {
	switch s {
	case StatusDone, StatusCancelled, StatusFailed, StatusStuck:
		return true
	default:
		return false
	}
}

// String returns a human-readable name for the status.
func (s TransactionStatus) String() string {
	switch s {
	case StatusPending:
		return "Pending"
	case StatusDone:
		return "Done"
	case StatusCancelled:
		return "Cancelled"
	case StatusFailed:
		return "Failed"
	case StatusCancelPending:
		return "CancelPending"
	case StatusStuck:
		return "Stuck"
	default:
		return "Unknown"
	}
}

// PrecheckRequest asks for a payment quote (rate + commission) without creating a
// payment. The credit party (your institution) is resolved from the access token's
// azp claim and must not be sent here.
type PrecheckRequest struct {
	// ThirdPartyIDDebit is the recipient bank id.
	ThirdPartyIDDebit string `json:"third_party_id_debit"`
	// ServiceID is the service id (e.g. card-payout).
	ServiceID string `json:"service_id"`
	// RecipientID is the recipient identifier within the chosen service.
	RecipientID string `json:"recipient_id"`
	// AmountCredit is the amount in the credit currency (must be > 0).
	AmountCredit decimal.Decimal `json:"amount_credit"`
	// CurrencyCredit is the credit currency (ISO-4217), e.g. USD.
	CurrencyCredit string `json:"currency_credit"`
	// Data is a service-specific payload, validated server-side against the service's
	// JSON Schema. Omitted from the wire when nil.
	Data map[string]any `json:"data,omitempty"`
}

// PayRequest initiates a cross-bank payment. The credit party (your institution) is
// resolved from the access token's azp claim — do not send third_party_id_credit.
type PayRequest struct {
	// ThirdPartyIDDebit is the recipient bank id.
	ThirdPartyIDDebit string `json:"third_party_id_debit"`
	// ServiceID is the service id (e.g. card-payout).
	ServiceID string `json:"service_id"`
	// SenderID is the sender identifier within the chosen service.
	SenderID string `json:"sender_id"`
	// RecipientID is the recipient identifier within the chosen service.
	RecipientID string `json:"recipient_id"`
	// AmountCredit is the amount in the credit currency (must be > 0).
	AmountCredit decimal.Decimal `json:"amount_credit"`
	// CurrencyCredit is the credit currency (ISO-4217), e.g. USD.
	CurrencyCredit string `json:"currency_credit"`
	// Data is a service-specific payload, validated server-side against the service's
	// JSON Schema. Omitted from the wire when nil.
	Data map[string]any `json:"data,omitempty"`
}

// PrecheckResult is a quote for a prospective payment. The FX markup is already
// applied and is locked onto the transaction if you proceed to Pay.
type PrecheckResult struct {
	ThirdPartyID   string          `json:"third_party_id"`
	ServiceID      string          `json:"service_id"`
	RecipientID    string          `json:"recipient_id"`
	AmountCredit   decimal.Decimal `json:"amount_credit"`
	CurrencyCredit string          `json:"currency_credit"`
	// AmountDebit is the amount the recipient bank is debited, in the debit currency.
	AmountDebit   decimal.Decimal `json:"amount_debit"`
	CurrencyDebit string          `json:"currency_debit"`
	// Rate is the quoted exchange rate: 1 CREDIT = Rate DEBIT.
	Rate decimal.Decimal `json:"rate"`
	// Commission charged, in the credit currency.
	Commission decimal.Decimal `json:"commission"`
}

// PostcheckResult is the current status of a payment transaction.
type PostcheckResult struct {
	TransactionID int64             `json:"transaction_id"`
	Status        TransactionStatus `json:"status"`
	// Error is failure detail; present only when the payment failed.
	Error string `json:"error,omitempty"`
}

// payResponse is the wire response of a successful Pay call.
type payResponse struct {
	TransactionID int64 `json:"transaction_id"`
}

// cancelResult is the wire response of a Cancel call.
type cancelResult struct {
	Status TransactionStatus `json:"status"`
}

type cancelRequest struct {
	TransactionID int64 `json:"transaction_id"`
}
