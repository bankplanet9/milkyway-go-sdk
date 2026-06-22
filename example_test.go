package milkyway_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/bankplanet9/milkyway-go-sdk"
	"github.com/shopspring/decimal"
)

func Example() {
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

	// Quote a payment.
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
	fmt.Printf("debit %s %s at rate %s\n", quote.AmountDebit, quote.CurrencyDebit, quote.Rate)

	// Initiate the payment (with an idempotency key so it is safely retryable).
	txID, err := client.Pay(ctx, milkyway.PayRequest{
		ThirdPartyIDDebit: "recipient-bank",
		ServiceID:         "card-payout",
		SenderID:          "sndr-1",
		RecipientID:       "rcpt-1",
		AmountCredit:      decimal.RequireFromString("100.00"),
		CurrencyCredit:    "USD",
	}, "order-42")
	if err != nil {
		var apiErr *milkyway.APIError
		switch {
		case errors.Is(err, milkyway.ErrExposureBlocked):
			log.Fatal("blocked by exposure limit")
		case errors.As(err, &apiErr):
			log.Fatalf("API error %d: %s", apiErr.StatusCode, apiErr.Message)
		default:
			log.Fatal(err)
		}
	}

	// Poll until terminal.
	result, err := client.WaitForCompletion(ctx, txID, &milkyway.PollOptions{Timeout: 2 * time.Minute})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("transaction %d: %s\n", result.TransactionID, result.Status)
}
