package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Madrid must resolve in minimal container images

	"github.com/shopspring/decimal"
)

// Record types.
const (
	RecordAlta      = "ALTA"
	RecordAnulacion = "ANULACION"
)

// Submission states.
const (
	StatusPending            = "PENDING"
	StatusAccepted           = "ACCEPTED"
	StatusAcceptedWithErrors = "ACCEPTED_WITH_ERRORS"
	StatusRejected           = "REJECTED"
	StatusError              = "ERROR"
)

var (
	ErrNotConfigured   = errors.New("verifactu: not enabled for this company")
	ErrUnsupportedType = errors.New("verifactu: this document type cannot be issued under VERI*FACTU yet")
	ErrNeedEUR         = errors.New("verifactu: invoices must be issued in euros")
	ErrNoCertificate   = errors.New("verifactu: no certificate uploaded for this company")
)

// MadridLocation is the zone AEAT expects record timestamps in. The time package needs tzdata, which
// the server binary embeds (see apps/server).
func MadridLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Madrid")
	if err != nil {
		return time.FixedZone("CET", 3600)
	}
	return loc
}

// Party is a recipient (customer) of an invoice: a Spanish NIF, or another identifier with its country.
type Party struct {
	Name    string
	NIF     string // Spanish NIF/NIE/CIF; empty when IDOther is used
	IDOther *IDOther
}

// IDOther identifies a recipient without a Spanish NIF.
type IDOther struct {
	CountryCode string // ISO 3166-1 alpha-2
	IDType      string // 02 VAT number, 03 passport, 04 official ID of country of residence, 05 residence certificate, 06 other, 07 not registered
	ID          string
}

// Breakdown is one DetalleDesglose line: operations sharing the same tax treatment and rate.
type Breakdown struct {
	Tax            string // 01 IVA, 02 IPSI, 03 IGIC, 05 other
	Regime         string // ClaveRegimen, "01" general, "18" recargo de equivalencia
	Qualification  string // S1, S2, N1, N2 (taxable); empty when Exempt is set
	Exempt         string // E1..E8; empty when Qualification is set
	Rate           *decimal.Decimal
	Base           decimal.Decimal
	Quota          *decimal.Decimal
	SurchargeRate  *decimal.Decimal
	SurchargeQuota *decimal.Decimal
}

// ChainRef points at the previous record in the issuer's chain.
type ChainRef struct {
	IssuerNIF string
	Number    string
	IssueDate time.Time
	Hash      string
}

// System describes the invoicing software producing the record (SistemaInformatico).
type System struct {
	ProducerName       string
	ProducerNIF        string   // set for a Spanish producer
	ProducerOther      *IDOther // set for a foreign producer
	SystemName         string   // at most 30 characters
	SystemID           string   // two characters assigned by the producer
	Version            string
	InstallationNumber string
	OnlyVeriFactu      bool
	MultiTaxpayer      bool
	MultipleTaxpayers  bool
}

// Alta is an invoice (registro de alta).
type Alta struct {
	ID               InvoiceID
	IssuerName       string
	InvoiceType      string // F1, F2, F3, R1..R5
	RectType         string // "I" difference or "S" substitution, for R types
	Rectified        []InvoiceID
	Description      string
	Recipients       []Party
	Breakdown        []Breakdown
	TaxTotal         decimal.Decimal
	InvoiceTotal     decimal.Decimal
	Simplified       bool // F2 issued as a simplified invoice
	WithoutRecipient bool
	Previous         *ChainRef
	System           System
	GeneratedAt      time.Time
}

// Cancel is a cancellation (registro de anulación).
type Cancel struct {
	ID          InvoiceID
	Previous    *ChainRef
	System      System
	GeneratedAt time.Time
}

func (a Alta) hashInput() AltaInput {
	in := AltaInput{ID: a.ID, InvoiceType: a.InvoiceType, TaxTotal: a.TaxTotal, InvoiceTotal: a.InvoiceTotal, GeneratedAt: a.GeneratedAt}
	if a.Previous != nil {
		in.PreviousHash = a.Previous.Hash
	}
	return in
}

// Hash returns the fingerprint to store in the record.
func (a Alta) Hash() string { return AltaHash(a.hashInput()) }

func (c Cancel) hashInput() CancelInput {
	in := CancelInput{ID: c.ID, GeneratedAt: c.GeneratedAt}
	if c.Previous != nil {
		in.PreviousHash = c.Previous.Hash
	}
	return in
}

// Hash returns the fingerprint to store in the record.
func (c Cancel) Hash() string { return CancelHash(c.hashInput()) }

// Validate applies AEAT's field rules that the schema alone does not catch cleanly.
func (a Alta) Validate() error {
	if err := ValidateInvoiceNumber(a.ID.Number); err != nil {
		return err
	}
	if len(strings.TrimSpace(a.ID.IssuerNIF)) != 9 {
		return errors.New("verifactu: the company's tax ID (NIF) must be 9 characters; set it in company settings")
	}
	if strings.TrimSpace(a.IssuerName) == "" || len(a.IssuerName) > 120 {
		return errors.New("verifactu: company legal name is required and at most 120 characters")
	}
	switch a.InvoiceType {
	case "F1", "F2", "F3", "R1", "R2", "R3", "R4", "R5":
	default:
		return fmt.Errorf("verifactu: unsupported invoice type %q", a.InvoiceType)
	}
	if d := strings.TrimSpace(a.Description); d == "" || len(d) > 500 {
		return errors.New("verifactu: operation description is required and at most 500 characters")
	}
	if len(a.Breakdown) == 0 {
		return errors.New("verifactu: the invoice has no tax breakdown")
	}
	if len(a.Breakdown) > 12 {
		return errors.New("verifactu: an invoice can have at most 12 tax breakdown lines")
	}
	if a.InvoiceType == "F1" && len(a.Recipients) == 0 {
		return errors.New("verifactu: a complete invoice (F1) needs the customer's name and tax ID")
	}
	for _, r := range a.Recipients {
		if strings.TrimSpace(r.Name) == "" || len(r.Name) > 120 {
			return errors.New("verifactu: recipient name is required and at most 120 characters")
		}
		if r.NIF == "" && r.IDOther == nil {
			return errors.New("verifactu: recipient needs a NIF or another identifier")
		}
		if r.NIF != "" && len(r.NIF) != 9 {
			return errors.New("verifactu: recipient NIF must be 9 characters")
		}
	}
	// The record's totals must agree with its breakdown to the cent.
	var base, quota decimal.Decimal
	for _, b := range a.Breakdown {
		base = base.Add(b.Base)
		if b.Quota != nil {
			quota = quota.Add(*b.Quota)
		}
		if b.SurchargeQuota != nil {
			quota = quota.Add(*b.SurchargeQuota)
		}
	}
	if !quota.Round(2).Equal(a.TaxTotal.Round(2)) {
		return fmt.Errorf("verifactu: tax total %s does not match the breakdown (%s)", Amount(a.TaxTotal), Amount(quota))
	}
	if !base.Add(quota).Round(2).Equal(a.InvoiceTotal.Round(2)) {
		return fmt.Errorf("verifactu: invoice total %s does not match base plus tax (%s)", Amount(a.InvoiceTotal), Amount(base.Add(quota)))
	}
	return nil
}
