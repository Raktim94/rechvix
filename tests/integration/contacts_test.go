//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	contactsapp "rechvix/internal/modules/contacts/app"
	contactsdomain "rechvix/internal/modules/contacts/domain"
	contactspg "rechvix/internal/modules/contacts/pg"
	"rechvix/internal/platform/audit"
	"rechvix/internal/platform/permissions"
)

func newTestContactsService(t *testing.T) *contactsapp.Service {
	t.Helper()
	return contactsapp.NewService(
		sharedPool,
		contactspg.NewPartyRepo(sharedPool),
		contactspg.NewAddressRepo(sharedPool),
		contactspg.NewTaxRegistrationRepo(sharedPool),
		permissions.NewChecker(permissions.NewPGStore(sharedPool), sharedPool),
		audit.NewPGRecorder(sharedPool),
	)
}

func TestContacts_Party_Address_TaxRegistration_CRUD(t *testing.T) {
	ctx := context.Background()
	svc := newTestContactsService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	party, err := svc.CreateParty(ctx, principal, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartyBoth, LegalName: "Integration Test Traders", CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateParty: %v", err)
	}
	if party.PartyType != contactsdomain.PartyBoth {
		t.Fatalf("PartyType = %s, want BOTH", party.PartyType)
	}

	addr, err := svc.AddAddress(ctx, principal, contactsapp.AddAddressParams{
		PartyID: party.ID, AddressType: contactsdomain.AddressBilling, Line1: "1 Test Street", CountryCode: "IN", IsDefault: true,
	})
	if err != nil {
		t.Fatalf("AddAddress: %v", err)
	}
	addrs, err := svc.ListAddresses(ctx, principal, party.ID)
	if err != nil {
		t.Fatalf("ListAddresses: %v", err)
	}
	if len(addrs) != 1 || addrs[0].ID != addr.ID {
		t.Fatalf("ListAddresses = %+v, want exactly the one just added", addrs)
	}

	gstin := "29ABCDE1234F1Z" + uuid.NewString()[:1]
	reg, err := svc.AddTaxRegistration(ctx, principal, contactsapp.AddTaxRegistrationParams{
		PartyID: party.ID, CountryCode: "IN", RegistrationNumber: gstin, StateCode: "29", IsPrimary: true,
	})
	if err != nil {
		t.Fatalf("AddTaxRegistration: %v", err)
	}

	looked, err := svc.LookupByRegistrationNumber(ctx, principal, gstin)
	if err != nil {
		t.Fatalf("LookupByRegistrationNumber: %v", err)
	}
	if looked.ID != reg.ID {
		t.Fatalf("LookupByRegistrationNumber returned %s, want %s", looked.ID, reg.ID)
	}

	// Duplicate registration number for the same party must be rejected,
	// not silently accepted as a second row (migrations/0009_contacts.up.sql
	// UNIQUE(organisation_id, party_id, registration_number)).
	if _, err := svc.AddTaxRegistration(ctx, principal, contactsapp.AddTaxRegistrationParams{
		PartyID: party.ID, CountryCode: "IN", RegistrationNumber: gstin,
	}); !errors.Is(err, contactsdomain.ErrDuplicateRegistration) {
		t.Fatalf("expected ErrDuplicateRegistration on a repeated registration number, got %v", err)
	}
}

func TestContacts_CreateParty_RejectsInvalidPartyType(t *testing.T) {
	ctx := context.Background()
	svc := newTestContactsService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	_, err := svc.CreateParty(ctx, principal, contactsapp.CreatePartyParams{
		PartyType: "VENDOR", LegalName: "Should Not Be Created", CurrencyCode: "INR",
	})
	if !errors.Is(err, contactsdomain.ErrInvalidPartyType) {
		t.Fatalf("expected ErrInvalidPartyType, got %v", err)
	}
}

// TestContacts_RLS_BlocksCrossOrganisationPartyRead is the contacts
// module's Scenario G building block: Organisation B must not be able to
// read Organisation A's customer/supplier record.
func TestContacts_RLS_BlocksCrossOrganisationPartyRead(t *testing.T) {
	ctx := context.Background()
	svc := newTestContactsService(t)
	principalA := bootstrapOwnerPrincipal(t, ctx)
	principalB := bootstrapOwnerPrincipal(t, ctx)

	party, err := svc.CreateParty(ctx, principalA, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartyCustomer, LegalName: "Org A Private Customer", CurrencyCode: "INR",
	})
	if err != nil {
		t.Fatalf("CreateParty as A: %v", err)
	}

	if _, err := svc.GetParty(ctx, principalB, party.ID); !errors.Is(err, contactsdomain.ErrNotFound) {
		t.Fatalf("GetParty as B for A's party: got err=%v, want ErrNotFound", err)
	}
	if _, err := svc.GetParty(ctx, principalA, party.ID); err != nil {
		t.Fatalf("GetParty as A for its own party: %v", err)
	}
}

// Regression: typing the first word (or any fragment) of a party's name must
// find it. The search used trigram similarity alone, which scores "Gupta"
// against "Gupta Engineering Works" below its threshold, so the sale-counter
// customer box offered only "+ New customer" for an existing customer.
func TestContacts_SearchParties_MatchesNameFragmentsPhoneAndLiteralWildcards(t *testing.T) {
	ctx := context.Background()
	svc := newTestContactsService(t)
	principal := bootstrapOwnerPrincipal(t, ctx)

	suffix := uuid.NewString()[:8]
	phone := fmt.Sprintf("9%09d", time.Now().UnixNano()%1_000_000_000)
	gupta, err := svc.CreateParty(ctx, principal, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartyCustomer, LegalName: "Gupta" + suffix + " Engineering Works", CurrencyCode: "INR", Phone: phone,
	})
	if err != nil {
		t.Fatalf("CreateParty: %v", err)
	}
	if _, err := svc.CreateParty(ctx, principal, contactsapp.CreatePartyParams{
		PartyType: contactsdomain.PartyCustomer, LegalName: "Other" + suffix + " Traders", CurrencyCode: "INR",
	}); err != nil {
		t.Fatalf("CreateParty: %v", err)
	}

	cases := map[string]string{
		"first word":     "Gupta" + suffix,
		"lower-case":     "gupta" + suffix,
		"middle word":    "engineering works",
		"phone fragment": phone[2:8],
	}
	for name, q := range cases {
		got, err := svc.SearchParties(ctx, principal, q, 20)
		if err != nil {
			t.Fatalf("%s: SearchParties(%q): %v", name, q, err)
		}
		found := false
		for _, p := range got {
			found = found || p.ID == gupta.ID
		}
		if !found {
			t.Errorf("%s: SearchParties(%q) did not return the party; got %d result(s)", name, q, len(got))
		}
	}

	// A literal % must not behave as a match-everything wildcard.
	got, err := svc.SearchParties(ctx, principal, "%%", 20)
	if err != nil {
		t.Fatalf("SearchParties(%%%%): %v", err)
	}
	for _, p := range got {
		if p.ID == gupta.ID {
			t.Errorf("SearchParties(%%%%) matched %q; wildcards must be escaped", p.LegalName)
		}
	}
}
