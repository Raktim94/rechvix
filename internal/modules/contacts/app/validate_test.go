package app

import (
	"errors"
	"testing"

	"rechvix/internal/modules/contacts/domain"
)

func TestValidateCreateParty(t *testing.T) {
	tests := []struct {
		name    string
		params  CreatePartyParams
		wantErr error
	}{
		{
			name:   "valid customer",
			params: CreatePartyParams{PartyType: domain.PartyCustomer, LegalName: "Acme Traders", CurrencyCode: "INR"},
		},
		{
			name:   "valid supplier",
			params: CreatePartyParams{PartyType: domain.PartySupplier, LegalName: "Acme Suppliers", CurrencyCode: "INR"},
		},
		{
			name:   "valid both",
			params: CreatePartyParams{PartyType: domain.PartyBoth, LegalName: "Acme Trading Co", CurrencyCode: "INR"},
		},
		{
			name:    "invalid party type",
			params:  CreatePartyParams{PartyType: "VENDOR", LegalName: "Acme", CurrencyCode: "INR"},
			wantErr: domain.ErrInvalidPartyType,
		},
		{
			name:    "empty party type",
			params:  CreatePartyParams{PartyType: "", LegalName: "Acme", CurrencyCode: "INR"},
			wantErr: domain.ErrInvalidPartyType,
		},
		{
			name:    "missing legal name",
			params:  CreatePartyParams{PartyType: domain.PartyCustomer, LegalName: "", CurrencyCode: "INR"},
			wantErr: domain.ErrLegalNameRequired,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateParty(tc.params)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected error %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidAddressType(t *testing.T) {
	valid := []domain.AddressType{domain.AddressBilling, domain.AddressShipping, domain.AddressWarehouse, domain.AddressRegisteredOffice}
	for _, v := range valid {
		if !domain.ValidAddressType(v) {
			t.Errorf("expected %q to be valid", v)
		}
	}
	if domain.ValidAddressType("HEADQUARTERS") {
		t.Error("expected an unrecognized address type to be invalid")
	}
}

func TestPhoneAcceptsInternationalNumbers(t *testing.T) {
	for _, p := range []string{"9876543210", "600111222", "34600111222", "442071838750"} {
		if err := ValidateCreateParty(CreatePartyParams{PartyType: domain.PartyCustomer, LegalName: "X", Phone: p}); err != nil {
			t.Errorf("%q should be valid: %v", p, err)
		}
	}
	for _, p := range []string{"12345", "1234567890123456", "abc1234567"} {
		if err := ValidateCreateParty(CreatePartyParams{PartyType: domain.PartyCustomer, LegalName: "X", Phone: p}); err == nil {
			t.Errorf("%q must be rejected", p)
		}
	}
	if got := NormalizePhone("+34 600-111 (222)"); got != "34600111222" {
		t.Errorf("normalized: %q", got)
	}
}
