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

// PartyType is the kind of payment party in a PartyInfo.
type PartyType string

const (
	// PartyTypeNatural is a person. It is the only party type the API accepts today.
	PartyTypeNatural PartyType = "natural"
	// PartyTypeLegal is an organization. It is part of the model, but the API refuses
	// it (400) for now.
	PartyTypeLegal PartyType = "legal"
)

// IdentityDocumentType is the kind of identity document in a PartyInfo. The
// vocabulary is closed and maps one-to-one to IVMS101 nationalIdentifierType.
type IdentityDocumentType string

const (
	// IdentityDocumentPassport is a passport (IVMS101 CCPT).
	IdentityDocumentPassport IdentityDocumentType = "passport"
	// IdentityDocumentNationalID is a national identity card (IVMS101 IDCD).
	IdentityDocumentNationalID IdentityDocumentType = "national_id"
	// IdentityDocumentDriverLicense is a driver's license (IVMS101 DRLC).
	IdentityDocumentDriverLicense IdentityDocumentType = "driver_license"
	// IdentityDocumentResidencePermit is a residence permit (IVMS101 ARNU).
	IdentityDocumentResidencePermit IdentityDocumentType = "residence_permit"
	// IdentityDocumentTaxID is a tax identification number (IVMS101 TXID).
	IdentityDocumentTaxID IdentityDocumentType = "tax_id"
	// IdentityDocumentSocialSecurity is a social security number (IVMS101 SOCS).
	IdentityDocumentSocialSecurity IdentityDocumentType = "social_security"
	// IdentityDocumentOther is any other document (IVMS101 MISC).
	IdentityDocumentOther IdentityDocumentType = "other"
)

// PartyFieldPath names one field of a PartyInfo, as listed in a PartyRequirements.
type PartyFieldPath string

// The PartyInfo field paths, in wire form.
const (
	PartyFieldNameFirst               PartyFieldPath = "name.first"
	PartyFieldNameLast                PartyFieldPath = "name.last"
	PartyFieldNameFull                PartyFieldPath = "name.full"
	PartyFieldDateOfBirth             PartyFieldPath = "date_of_birth"
	PartyFieldIdentityDocumentType    PartyFieldPath = "identity_document_type"
	PartyFieldIdentityDocumentNumber  PartyFieldPath = "identity_document_number"
	PartyFieldIdentityDocumentCountry PartyFieldPath = "identity_document_country"
	PartyFieldCountryOfResidence      PartyFieldPath = "country_of_residence"
	PartyFieldAddressCountry          PartyFieldPath = "address.country"
	PartyFieldAddressCity             PartyFieldPath = "address.city"
	PartyFieldAddressStreet           PartyFieldPath = "address.street"
	PartyFieldAddressBuilding         PartyFieldPath = "address.building"
	PartyFieldAddressPostalCode       PartyFieldPath = "address.postal_code"
	PartyFieldAddressLine             PartyFieldPath = "address.line"
)

// PartyInfo is the canonical identity of a payment party — the payer (SenderInfo)
// or the beneficiary (RecipientInfo). The model is IVMS101-anchored and the same
// for every recipient bank and service; each bank's connector converts it to the
// bank's own format. The account, card or phone stays in SenderID / RecipientID.
//
// Only PartyType is required. Empty fields, and an empty Name or Address, are
// omitted from the wire. Which fields a service needs is published in its
// ServiceCapability.PartyProfile; a missing required field or an invalid value is
// refused with 400, and APIError.Field names it (e.g. "sender_info.date_of_birth").
// Text values are at most 256 characters.
type PartyInfo struct {
	// PartyType is the kind of party. Use PartyTypeNatural.
	PartyType PartyType `json:"party_type"`
	// Name is the party's name.
	Name PartyName `json:"name,omitzero"`
	// DateOfBirth is an ISO 8601 date, YYYY-MM-DD (e.g. "1990-04-18").
	DateOfBirth string `json:"date_of_birth,omitempty"`
	// IdentityDocumentType is the kind of identity document. Required with
	// IdentityDocumentNumber.
	IdentityDocumentType IdentityDocumentType `json:"identity_document_type,omitempty"`
	// IdentityDocumentNumber is the document's series and number together, as written
	// in the document.
	IdentityDocumentNumber string `json:"identity_document_number,omitempty"`
	// IdentityDocumentCountry is the issuing country, ISO 3166-1 alpha-2 upper case (e.g. TJ).
	IdentityDocumentCountry string `json:"identity_document_country,omitempty"`
	// CountryOfResidence is ISO 3166-1 alpha-2 upper case (e.g. AE).
	CountryOfResidence string `json:"country_of_residence,omitempty"`
	// Address is the party's address.
	Address PartyAddress `json:"address,omitzero"`
}

// PartyName is the name of a payment party.
type PartyName struct {
	// First is the given name.
	First string `json:"first,omitempty"`
	// Last is the family name.
	Last string `json:"last,omitempty"`
	// Full is the name as written in the identity document: family name first, with
	// a patronymic if any (e.g. "Petrov Ivan Sergeevich").
	Full string `json:"full,omitempty"`
}

// PartyAddress is the address of a payment party. Country is required when an
// address is sent.
type PartyAddress struct {
	// Country is ISO 3166-1 alpha-2 upper case (e.g. AE).
	Country string `json:"country"`
	// City is the city or town.
	City string `json:"city,omitempty"`
	// Street is the street name.
	Street string `json:"street,omitempty"`
	// Building is the building number or name.
	Building string `json:"building,omitempty"`
	// PostalCode is the postal code.
	PostalCode string `json:"postal_code,omitempty"`
	// Line holds an address kept as free text, one line per element (at most 7).
	Line []string `json:"line,omitempty"`
}

// IsZero reports whether the address carries no value, so an empty Address is
// omitted from the wire.
func (a PartyAddress) IsZero() bool {
	return a.Country == "" && a.City == "" && a.Street == "" && a.Building == "" &&
		a.PostalCode == "" && len(a.Line) == 0
}

// PartyProfile says which PartyInfo fields a service's recipient bank requires and
// accepts — the same profile the bank enforces at Precheck and Pay. A nil side
// needs no fields.
type PartyProfile struct {
	// SenderInfo is the requirement for PrecheckRequest/PayRequest.SenderInfo.
	SenderInfo *PartyRequirements `json:"sender_info,omitempty"`
	// RecipientInfo is the requirement for PrecheckRequest/PayRequest.RecipientInfo.
	RecipientInfo *PartyRequirements `json:"recipient_info,omitempty"`
}

// PartyRequirements lists the field paths a recipient bank uses within one party
// object. A field in neither list is not used by the bank.
type PartyRequirements struct {
	// Required are the fields a Precheck or Pay must carry; a missing one is refused
	// with 400 naming it.
	Required []PartyFieldPath `json:"required,omitempty"`
	// Accepted are the fields the bank uses when present.
	Accepted []PartyFieldPath `json:"accepted,omitempty"`
	// IdentityDocumentTypes are the document kinds the bank takes. Empty means any;
	// another kind is refused with 400 naming identity_document_type.
	IdentityDocumentTypes []IdentityDocumentType `json:"identity_document_types,omitempty"`
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
	// SenderInfo is your customer, the payer. The fields the service requires are in
	// its ServiceCapability.PartyProfile. Omitted from the wire when nil.
	SenderInfo *PartyInfo `json:"sender_info,omitempty"`
	// RecipientInfo is the beneficiary. The fields the service requires are in its
	// ServiceCapability.PartyProfile. Omitted from the wire when nil.
	RecipientInfo *PartyInfo `json:"recipient_info,omitempty"`
	// Data carries a service's non-identity extras, validated by the recipient bank
	// against the service's JSON Schema (see Client.ServiceSchema). Most services take
	// none. Never put the payer's or beneficiary's identity here — use SenderInfo and
	// RecipientInfo. Omitted from the wire when nil.
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
	// SenderInfo is your customer, the payer. The fields the service requires are in
	// its ServiceCapability.PartyProfile. Omitted from the wire when nil.
	SenderInfo *PartyInfo `json:"sender_info,omitempty"`
	// RecipientInfo is the beneficiary. The fields the service requires are in its
	// ServiceCapability.PartyProfile. Omitted from the wire when nil.
	RecipientInfo *PartyInfo `json:"recipient_info,omitempty"`
	// Data carries a service's non-identity extras, forwarded to the recipient bank
	// (see Client.ServiceSchema). Most services take none. Never put the payer's or
	// beneficiary's identity here — use SenderInfo and RecipientInfo. Omitted from the
	// wire when nil.
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
	// Error is the code of the receiving bank's reason for a terminal outcome, when it
	// gave one: one of the Reason* constants (ReasonPaymentRejected,
	// ReasonRecipientNotFound, ReasonRecipientInvalid, ReasonServiceUnavailable,
	// ReasonPaymentCannotBeCanceled) — never the bank's own text. Empty when no
	// reason was given.
	Error string `json:"error,omitempty"`
}

// ServiceCapability describes one payout service a destination bank offers. The
// JSON Schema of the service's non-identity Data extras is exposed separately via
// Client.ServiceSchema and is omitted from the discovery listing.
type ServiceCapability struct {
	// ServiceID is the service id to pass to Precheck/Pay (e.g. card-payout).
	ServiceID string `json:"service_id"`
	// PayoutMethod is the payout channel (e.g. card, wallet, bank-account).
	PayoutMethod string `json:"payout_method"`
	// Recipient is a human-readable recipient descriptor for the service, when provided.
	Recipient string `json:"recipient,omitempty"`
	// PayoutCurrencies are the currencies (ISO-4217) the service can pay out in, when provided.
	PayoutCurrencies []string `json:"payout_currencies,omitempty"`
	// PartyProfile says which SenderInfo / RecipientInfo fields the recipient bank
	// requires and accepts for this service. Nil when the bank has published none.
	PartyProfile *PartyProfile `json:"party_profile,omitempty"`
}

// Destination is a partner bank you can pay to, together with the services it offers.
type Destination struct {
	// ThirdPartyID is the recipient bank id — pass it as ThirdPartyIDDebit to Precheck/Pay.
	ThirdPartyID string `json:"third_party_id"`
	// PartnerName is the bank's display name, when provided.
	PartnerName string `json:"partner_name,omitempty"`
	// Country is the bank's ISO 3166-1 alpha-2 country code, when provided.
	Country string `json:"country,omitempty"`
	// Services are the payout services this destination offers.
	Services []ServiceCapability `json:"services"`
}

// discoveryResponse is the wire envelope of a Discover call.
type discoveryResponse struct {
	Destinations []Destination `json:"destinations"`
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
