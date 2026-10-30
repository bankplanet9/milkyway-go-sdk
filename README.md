# MilkyWay Payments Go SDK

[![Go Reference](https://pkg.go.dev/badge/github.com/bankplanet9/milkyway-go-sdk.svg)](https://pkg.go.dev/github.com/bankplanet9/milkyway-go-sdk)
[![CI](https://github.com/bankplanet9/milkyway-go-sdk/actions/workflows/ci.yml/badge.svg)](https://github.com/bankplanet9/milkyway-go-sdk/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

A production-ready Go client for the [MilkyWay](https://planet9.ae) Payments API
(`/payments/v1`). It handles Keycloak client-credentials auth, transient-failure
retries, decimal-exact money, typed errors, and completion polling.

- **Auth** — Keycloak client-credentials with token caching, single-flight acquisition, and 401 refresh-replay.
- **Resilience** — exponential backoff + jitter on transient failures, with a per-attempt timeout.
- **Exact money** — [`shopspring/decimal`](https://github.com/shopspring/decimal), serialized as JSON numbers (never binary floats).
- **Typed errors** — `*APIError` plus `errors.Is`-friendly sentinels for 400/401/402/404/422/502/5xx, plus reason-code constants.
- **Context-first** — every call takes `context.Context` for cancellation and deadlines.

## Install

```sh
go get github.com/bankplanet9/milkyway-go-sdk
```

Requires Go 1.25+.

## Quick start

```go
package main

import (
	"context"
	"log"

	"github.com/bankplanet9/milkyway-go-sdk"
	"github.com/shopspring/decimal"
)

func main() {
	client, err := milkyway.NewClient(milkyway.Options{
		BaseURL:      "https://milkyway.stage.planet9.ae",
		TokenURL:     "https://keycloak.ac8o.planet9.ae/realms/planet9-stage/protocol/openid-connect/token",
		ClientID:     "my-bank",
		ClientSecret: "••••••",
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// The payer and the beneficiary, in the canonical model. Discovery's
	// PartyProfile says which fields the recipient bank requires.
	sender := &milkyway.PartyInfo{
		PartyType:              milkyway.PartyTypeNatural,
		Name:                   milkyway.PartyName{First: "Ivan", Last: "Petrov", Full: "Petrov Ivan Sergeevich"},
		DateOfBirth:            "1990-04-18",
		IdentityDocumentType:   milkyway.IdentityDocumentPassport,
		IdentityDocumentNumber: "AB1234567",
	}
	recipient := &milkyway.PartyInfo{
		PartyType: milkyway.PartyTypeNatural,
		Name:      milkyway.PartyName{Full: "Karimov Ali"},
	}

	quote, err := client.Precheck(ctx, milkyway.PrecheckRequest{
		ThirdPartyIDDebit: "recipient-bank",
		ServiceID:         "card-payout",
		RecipientID:       "rcpt-1",
		AmountCredit:      decimal.RequireFromString("100.00"),
		CurrencyCredit:    "USD",
		SenderInfo:        sender,
		RecipientInfo:     recipient,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("debit %s %s at rate %s", quote.AmountDebit, quote.CurrencyDebit, quote.Rate)

	txID, err := client.Pay(ctx, milkyway.PayRequest{
		ThirdPartyIDDebit: "recipient-bank",
		ServiceID:         "card-payout",
		SenderID:          "sndr-1",
		RecipientID:       "rcpt-1",
		AmountCredit:      decimal.RequireFromString("100.00"),
		CurrencyCredit:    "USD",
		SenderInfo:        sender,
		RecipientInfo:     recipient,
	}, "order-42") // idempotency key (optional)
	if err != nil {
		log.Fatal(err)
	}

	result, err := client.WaitForCompletion(ctx, txID, nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("transaction %d: %s", result.TransactionID, result.Status)
}
```

## API

| Method | Description |
| --- | --- |
| `Discover(ctx, country)` | List destination banks and their payout services; empty `country` lists all. |
| `ServiceSchema(ctx, thirdPartyID, serviceID)` | Raw Draft-2020 JSON Schema for a service's non-identity `Data` extras. |
| `Healthcheck(ctx, thirdPartyID, serviceID)` | Recipient service status string. |
| `Precheck(ctx, PrecheckRequest)` | Quote a payment (rate + commission), no side effects. |
| `Pay(ctx, PayRequest, idempotencyKey)` | Initiate a payment; returns the transaction id. |
| `Postcheck(ctx, transactionID)` | Current transaction status. |
| `Cancel(ctx, transactionID)` | Request cancellation; returns the resulting status. |
| `WaitForCompletion(ctx, transactionID, *PollOptions)` | Poll `Postcheck` with backoff until terminal or budget exhausted. |

`TransactionStatus` is an `int` enum (`StatusPending`=0, `StatusDone`=1,
`StatusCancelled`=2, `StatusFailed`=3, `StatusCancelPending`=4, `StatusStuck`=5).
Use `status.IsTerminal()` to decide whether to stop polling.

Money fields use `decimal.Decimal`. The SDK sets
`decimal.MarshalJSONWithoutQuotes = true` in its `init()` so amounts serialize as
JSON numbers, matching the API wire format.

## Discovery

Before quoting a payment you can discover which destination banks are reachable
and what each one supports. `Discover` returns the catalog (optionally filtered by
ISO 3166-1 alpha-2 country). Each service's `PartyProfile` says which `SenderInfo`
/ `RecipientInfo` fields its recipient bank requires and accepts (see
[Payer and beneficiary](#payer-and-beneficiary)). `ServiceSchema` returns the raw
JSON Schema of a service's non-identity `Data` extras — most services take none.

```go
dests, err := client.Discover(ctx, "TJ") // "" lists all countries
if err != nil {
	log.Fatal(err)
}
for _, d := range dests {
	log.Printf("%s (%s): %d services", d.PartnerName, d.ThirdPartyID, len(d.Services))
	for _, s := range d.Services {
		log.Printf("  %s via %s, currencies %v", s.ServiceID, s.PayoutMethod, s.PayoutCurrencies)
		if p := s.PartyProfile; p != nil && p.SenderInfo != nil {
			log.Printf("  sender needs %v (documents: %v)", p.SenderInfo.Required, p.SenderInfo.IdentityDocumentTypes)
		}
	}
}

schema, err := client.ServiceSchema(ctx, "recipient-bank", "card-payout")
if err != nil {
	log.Fatal(err)
}
log.Printf("data schema: %s", schema) // raw json.RawMessage (Draft-2020 JSON Schema)
```

## Payer and beneficiary

The identity of your customer (the payer) and of the beneficiary travels in
`SenderInfo` and `RecipientInfo` on `PrecheckRequest` and `PayRequest` — two
`*PartyInfo` values in one canonical model (FATF Travel Rule / ISO 20022 /
IVMS101), the same for every recipient bank and service. You map your customer
data to it once; each bank's connector converts it to that bank's own format. The
account, card or phone stays in `SenderID` / `RecipientID`.

| Field | Format |
| --- | --- |
| `PartyType` | `PartyTypeNatural` (required). `PartyTypeLegal` exists in the model but the API refuses it for now. |
| `Name.First`, `Name.Last`, `Name.Full` | Text. `Full` is the name as in the document, family name first, with a patronymic if any. |
| `DateOfBirth` | ISO 8601 date, `YYYY-MM-DD`. |
| `IdentityDocumentType` | `IdentityDocumentPassport`, `...NationalID`, `...DriverLicense`, `...ResidencePermit`, `...TaxID`, `...SocialSecurity`, `...Other`. Required with `IdentityDocumentNumber`. |
| `IdentityDocumentNumber` | Series and number together, as in the document. |
| `IdentityDocumentCountry`, `CountryOfResidence` | ISO 3166-1 alpha-2, upper case (`TJ`). |
| `Address` | `Country` (alpha-2, required when an address is sent), `City`, `Street`, `Building`, `PostalCode`, and `Line` — up to 7 free-text lines. |

Every field except `PartyType` is optional; empty fields (and an empty `Name` or
`Address`) are left out of the request. Text values are at most 256 characters.

Which fields a service needs is set by its recipient bank and published as
`ServiceCapability.PartyProfile` by `Discover`: `Required` and `Accepted` field
paths (`PartyFieldNameFirst`, `PartyFieldDateOfBirth`, …) for each side, and, when
the bank takes only some document kinds, `IdentityDocumentTypes`. Send the fields
you have; fields the bank does not use are ignored. A missing required field, an
invalid value or an unknown key is refused with `400`, and `APIError.Field` names
it by its path (e.g. `sender_info.date_of_birth`).

**Never put identity in `Data`** (no sender name, birthday, passport or document
number there) — the API does not read it from `Data`. `Data` carries only a
service's non-identity extras; `ServiceSchema` lists them. The browsable registry
of banks, services, party profiles and `Data` fields is the
[MilkyWay Handbook](https://docs.planet9.ae/site/milkyway-handbook/en/) (section
**Services**).

## Errors

Every non-2xx response becomes an `*APIError` carrying `StatusCode`, `Message`
(the server's `error` field), `Code` (the machine-readable code leading that
message, e.g. `ErrPaymentRejected`), `Field` (the request field at fault, when the
server names one), `ServiceID` / `ThirdPartyID` (the downstream service that failed,
on a `503`), and the raw `ResponseBody`. Classify it with `errors.Is` against the
sentinels, or unwrap with `errors.As`:

```go
_, err := client.Pay(ctx, req, "")
switch {
case errors.Is(err, milkyway.ErrValidation):         // 400
case errors.Is(err, milkyway.ErrAuth):                // 401
case errors.Is(err, milkyway.ErrExposureBlocked):     // 402
case errors.Is(err, milkyway.ErrNotFound):            // 404
case errors.Is(err, milkyway.ErrPaymentRejected):     // 422 — receiving bank refused; do not repeat
case errors.Is(err, milkyway.ErrRecipientBankError):  // 502 — receiving bank's connector failed
case errors.Is(err, milkyway.ErrServiceUnavailable):  // 5xx (502 included)
}

var apiErr *milkyway.APIError
if errors.As(err, &apiErr) {
	log.Printf("status %d (%s): %s", apiErr.StatusCode, apiErr.Code, apiErr.Message)
}
```

- **`422` `ErrPaymentRejected`** (Precheck, Pay) — the receiving bank refused the
  payment on its merits (a limit, a blocked account). Nothing was created or
  debited, and repeating the request will not change the answer; the SDK never
  retries it.
- **`502` `ErrRecipientBankError`** (Precheck, Pay, Cancel) — the receiving bank's
  connector failed in a way MilkyWay could not classify. Nothing was created. A
  `502` matches **both** `ErrRecipientBankError` and `ErrServiceUnavailable`, so
  existing 5xx handling keeps working; check `ErrRecipientBankError` first if you
  want to tell it apart.
- **`503` `ErrServiceUnavailable`** (Precheck, Pay, Cancel) — the receiving bank's
  service is unreachable; `APIError.ServiceID` and `APIError.ThirdPartyID` name it.
  Safe to retry once it recovers.

On a field-level `400`, `APIError.Field` names the request field at fault —
`amount_credit`, `currency_credit`, `recipient_id` (the receiving bank reports the
recipient invalid, `Code` = `ErrRecipientInvalid`), or a party field by its path
such as `sender_info.date_of_birth`.

### Reason codes on a terminal status

`PostcheckResult.Error` (also returned by `WaitForCompletion`) carries the code of
the receiving bank's reason for a terminal outcome — never free text. Compare it
with the exported constants:

```go
res, err := client.WaitForCompletion(ctx, txID, nil)
if err == nil && res.Status == milkyway.StatusFailed {
	switch res.Error {
	case milkyway.ReasonRecipientNotFound, milkyway.ReasonRecipientInvalid:
		// fix the recipient details
	case milkyway.ReasonServiceUnavailable:
		// try again later
	case milkyway.ReasonPaymentRejected:
		// declined by the receiving bank (also any reason it gave without a code)
	}
}
```

`ReasonPaymentCannotBeCanceled` reports a refused cancellation. The set may grow;
treat an unknown code like `ReasonPaymentRejected`.

## Retries & idempotency

Transient failures — HTTP `5xx`, `408`, and network/timeout errors — are retried
with exponential backoff + jitter, up to `MaxRetries` (default 3). Deterministic
outcomes (`400/401/402/404/422`) are **never** retried. A `502` is transient and
is retried like any other `5xx`.

`Pay` is special: **without an `Idempotency-Key` it is not auto-retried**, to avoid
creating duplicate payments. Pass a non-empty key to make it both idempotent on the
server and safely retryable by the SDK.

A `401` from the API triggers exactly one token refresh-and-replay (independent of
the retry budget). The token endpoint uses a separate HTTP client that never routes
through the auth/retry pipeline.

Tuning lives on `Options` (or via functional `With...` options):
`MaxRetries`, `RetryBaseDelay` (500ms), `RequestTimeout` (per-attempt, 30s),
`TokenRefreshSkew` (30s). `WaitForCompletion` is tuned via `PollOptions`
(`InitialDelay` 1s, `MaxDelay` 30s, `BackoffMultiplier` 2.0, `Timeout` 5m).

## Releases

Versioning is automated with [semantic-release](https://semantic-release.gitbook.io/)
driven by [Conventional Commits](https://www.conventionalcommits.org/). On every
push to `main`, CI computes the next version and creates a `vX.Y.Z` git tag and
GitHub release.

For Go there is **no registry to publish to** — the git tag *is* the release.
[pkg.go.dev](https://pkg.go.dev/github.com/bankplanet9/milkyway-go-sdk) indexes the
module on first fetch, and `go get` resolves the new version directly from the tag.
No secrets or one-time registry setup are required.

## License

MIT — see [LICENSE](LICENSE).
