# MilkyWay Payments Go SDK

A production-ready Go client for the [MilkyWay](https://planet9.ae) Payments API
(`/payments/v1`). It handles Keycloak client-credentials auth, transient-failure
retries, decimal-exact money, typed errors, and completion polling.

- **Auth** — Keycloak client-credentials with token caching, single-flight acquisition, and 401 refresh-replay.
- **Resilience** — exponential backoff + jitter on transient failures, with a per-attempt timeout.
- **Exact money** — [`shopspring/decimal`](https://github.com/shopspring/decimal), serialized as JSON numbers (never binary floats).
- **Typed errors** — `*APIError` plus `errors.Is`-friendly sentinels for 400/401/402/404/5xx.
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

	quote, err := client.Precheck(ctx, milkyway.PrecheckRequest{
		ThirdPartyIDDebit: "recipient-bank",
		ServiceID:         "card-payout",
		RecipientID:       "rcpt-1",
		AmountCredit:      decimal.RequireFromString("100.00"),
		CurrencyCredit:    "USD",
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

## Errors

Every non-2xx response becomes an `*APIError` carrying `StatusCode`, `Message`
(the server's `error` field), and the raw `ResponseBody`. Classify it with
`errors.Is` against the sentinels, or unwrap with `errors.As`:

```go
_, err := client.Pay(ctx, req, "")
switch {
case errors.Is(err, milkyway.ErrValidation):        // 400
case errors.Is(err, milkyway.ErrAuth):               // 401
case errors.Is(err, milkyway.ErrExposureBlocked):    // 402
case errors.Is(err, milkyway.ErrNotFound):           // 404
case errors.Is(err, milkyway.ErrServiceUnavailable): // 5xx
}

var apiErr *milkyway.APIError
if errors.As(err, &apiErr) {
	log.Printf("status %d: %s", apiErr.StatusCode, apiErr.Message)
}
```

## Retries & idempotency

Transient failures — HTTP `5xx`, `408`, and network/timeout errors — are retried
with exponential backoff + jitter, up to `MaxRetries` (default 3). Deterministic
outcomes (`400/401/402/404`) are **never** retried.

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
