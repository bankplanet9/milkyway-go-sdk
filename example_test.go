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

	// The payer and the beneficiary. The service's PartyProfile from Discover says
	// which fields its recipient bank requires; identity never goes in Data.
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

	// Quote a payment.
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
	fmt.Printf("debit %s %s at rate %s\n", quote.AmountDebit, quote.CurrencyDebit, quote.Rate)

	// Initiate the payment (with an idempotency key so it is safely retryable).
	txID, err := client.Pay(ctx, milkyway.PayRequest{
		ThirdPartyIDDebit: "recipient-bank",
		ServiceID:         "card-payout",
		SenderID:          "sndr-1",
		RecipientID:       "rcpt-1",
		AmountCredit:      decimal.RequireFromString("100.00"),
		CurrencyCredit:    "USD",
		SenderInfo:        sender,
		RecipientInfo:     recipient,
	}, "order-42")
	if err != nil {
		var apiErr *milkyway.APIError
		switch {
		case errors.Is(err, milkyway.ErrExposureBlocked):
			log.Fatal("blocked by exposure limit")
		case errors.Is(err, milkyway.ErrPaymentRejected):
			log.Fatal("refused by the receiving bank; do not repeat")
		case errors.Is(err, milkyway.ErrRecipientBankError):
			log.Fatal("receiving bank connector failed; retry later or use another service")
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
	if result.Status == milkyway.StatusFailed && result.Error == milkyway.ReasonRecipientNotFound {
		log.Fatal("the receiving bank does not know the recipient")
	}
	fmt.Printf("transaction %d: %s\n", result.TransactionID, result.Status)
}
