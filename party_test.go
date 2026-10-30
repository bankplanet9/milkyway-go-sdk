package milkyway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

// requestParties captures the raw party objects of a request body, so tests assert
// the exact wire keys rather than what the SDK's own types decode back.
type requestParties struct {
	SenderInfo    json.RawMessage `json:"sender_info"`
	RecipientInfo json.RawMessage `json:"recipient_info"`
}

func TestPrecheckSerializesPartyInfo(t *testing.T) {
	const wantSender = `{"party_type":"natural","name":{"first":"Ivan","last":"Petrov","full":"Petrov Ivan Sergeevich"},` +
		`"date_of_birth":"1990-04-18","identity_document_type":"passport","identity_document_number":"AB1234567",` +
		`"identity_document_country":"TJ","country_of_residence":"AE",` +
		`"address":{"country":"AE","city":"Dubai","street":"Sheikh Zayed Rd","building":"12","postal_code":"00000","line":["Office 4"]}}`
	const wantRecipient = `{"party_type":"natural","name":{"full":"Karimov Ali"}}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got requestParties
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("unmarshal request: %v", err)
			return
		}
		if string(got.SenderInfo) != wantSender {
			t.Errorf("sender_info =\n%s\nwant\n%s", got.SenderInfo, wantSender)
		}
		if string(got.RecipientInfo) != wantRecipient {
			t.Errorf("recipient_info =\n%s\nwant\n%s", got.RecipientInfo, wantRecipient)
		}
		if strings.Contains(string(body), `"data"`) {
			t.Errorf("nil data should be omitted: %s", body)
		}
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	_, err := c.Precheck(context.Background(), PrecheckRequest{
		ThirdPartyIDDebit: "bankA",
		ServiceID:         "card",
		RecipientID:       "r1",
		AmountCredit:      decimal.RequireFromString("100"),
		CurrencyCredit:    "USD",
		SenderInfo: &PartyInfo{
			PartyType:               PartyTypeNatural,
			Name:                    PartyName{First: "Ivan", Last: "Petrov", Full: "Petrov Ivan Sergeevich"},
			DateOfBirth:             "1990-04-18",
			IdentityDocumentType:    IdentityDocumentPassport,
			IdentityDocumentNumber:  "AB1234567",
			IdentityDocumentCountry: "TJ",
			CountryOfResidence:      "AE",
			Address: PartyAddress{
				Country: "AE", City: "Dubai", Street: "Sheikh Zayed Rd", Building: "12",
				PostalCode: "00000", Line: []string{"Office 4"},
			},
		},
		RecipientInfo: &PartyInfo{PartyType: PartyTypeNatural, Name: PartyName{Full: "Karimov Ali"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPartyInfoOmitsEmptyFields(t *testing.T) {
	cases := []struct {
		name  string
		party PartyInfo
		want  string
	}{
		{"only party type", PartyInfo{PartyType: PartyTypeNatural}, `{"party_type":"natural"}`},
		{"empty line slice", PartyInfo{PartyType: PartyTypeNatural, Address: PartyAddress{Line: []string{}}}, `{"party_type":"natural"}`},
		{"partial name", PartyInfo{PartyType: PartyTypeNatural, Name: PartyName{Last: "Petrov"}}, `{"party_type":"natural","name":{"last":"Petrov"}}`},
		{"address country only", PartyInfo{PartyType: PartyTypeNatural, Address: PartyAddress{Country: "TJ"}}, `{"party_type":"natural","address":{"country":"TJ"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.party)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPayOmitsNilPartyInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "sender_info") || strings.Contains(string(body), "recipient_info") {
			t.Errorf("nil party info should be omitted: %s", body)
		}
		_, _ = fmt.Fprint(w, `{"transaction_id":1}`)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, nil)
	if _, err := c.Pay(context.Background(), PayRequest{ServiceID: "svc"}, "k"); err != nil {
		t.Fatal(err)
	}
}

func TestPaySerializesPartyInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var got requestParties
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("unmarshal request: %v", err)
			return
		}
		if want := `{"party_type":"natural","identity_document_type":"national_id","identity_document_number":"123"}`; string(got.SenderInfo) != want {
			t.Errorf("sender_info = %s, want %s", got.SenderInfo, want)
		}
		if want := `{"party_type":"natural","country_of_residence":"TJ"}`; string(got.RecipientInfo) != want {
			t.Errorf("recipient_info = %s, want %s", got.RecipientInfo, want)
		}
		_, _ = fmt.Fprint(w, `{"transaction_id":7}`)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, nil)
	_, err := c.Pay(context.Background(), PayRequest{
		ServiceID:     "svc",
		SenderInfo:    &PartyInfo{PartyType: PartyTypeNatural, IdentityDocumentType: IdentityDocumentNationalID, IdentityDocumentNumber: "123"},
		RecipientInfo: &PartyInfo{PartyType: PartyTypeNatural, CountryOfResidence: "TJ"},
	}, "k")
	if err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverDecodesPartyProfile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"destinations":[{"third_party_id":"bankA","services":[`+
			`{"service_id":"card","payout_method":"card","party_profile":{`+
			`"sender_info":{"required":["name.first","name.last","date_of_birth","identity_document_type","identity_document_number"],"accepted":["name.full"],"identity_document_types":["passport"]},`+
			`"recipient_info":{"accepted":["address.line"]}}},`+
			`{"service_id":"wallet","payout_method":"wallet"}]}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL, nil)
	dests, err := c.Discover(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	card := dests[0].Services[0]
	if card.PartyProfile == nil || card.PartyProfile.SenderInfo == nil || card.PartyProfile.RecipientInfo == nil {
		t.Fatalf("party_profile = %+v", card.PartyProfile)
	}
	sender := card.PartyProfile.SenderInfo
	wantRequired := []PartyFieldPath{
		PartyFieldNameFirst, PartyFieldNameLast, PartyFieldDateOfBirth,
		PartyFieldIdentityDocumentType, PartyFieldIdentityDocumentNumber,
	}
	if !reflect.DeepEqual(sender.Required, wantRequired) {
		t.Errorf("sender required = %v", sender.Required)
	}
	if !reflect.DeepEqual(sender.Accepted, []PartyFieldPath{PartyFieldNameFull}) {
		t.Errorf("sender accepted = %v", sender.Accepted)
	}
	if !reflect.DeepEqual(sender.IdentityDocumentTypes, []IdentityDocumentType{IdentityDocumentPassport}) {
		t.Errorf("sender identity_document_types = %v", sender.IdentityDocumentTypes)
	}
	recipient := card.PartyProfile.RecipientInfo
	if len(recipient.Required) != 0 || !reflect.DeepEqual(recipient.Accepted, []PartyFieldPath{PartyFieldAddressLine}) {
		t.Errorf("recipient = %+v", recipient)
	}
	if wallet := dests[0].Services[1]; wallet.PartyProfile != nil {
		t.Errorf("service without party_profile decoded as %+v", wallet.PartyProfile)
	}
}

func TestValidationErrorCarriesField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"sender_info.date_of_birth: is required by the receiving bank for this service","field":"sender_info.date_of_birth"}`)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL, nil)
	_, err := c.Precheck(context.Background(), PrecheckRequest{ServiceID: "svc"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !errors.Is(err, ErrValidation) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.Field != "sender_info.date_of_birth" {
		t.Errorf("field = %q", apiErr.Field)
	}
	if !strings.HasPrefix(apiErr.Message, "sender_info.date_of_birth: is required") {
		t.Errorf("message = %q", apiErr.Message)
	}
}
