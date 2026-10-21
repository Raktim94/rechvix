// Package domain holds the pure VERI*FACTU building blocks: the record fingerprint (huella) and the
// invoice QR URL. Both follow AEAT's published specifications, checked in the tests against the
// examples in "Detalle de las especificaciones técnicas para generación de la huella o hash de los
// registros de facturación" v0.1.2 and "Detalle de las especificaciones técnicas del código QR" v0.5.0.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// InvoiceID identifies an invoice the way AEAT does: issuer NIF, series+number and issue date.
type InvoiceID struct {
	IssuerNIF string
	Number    string
	IssueDate time.Time
}

// AltaInput carries the fields the fingerprint of an invoice (alta) record covers, in AEAT's order.
type AltaInput struct {
	ID           InvoiceID
	InvoiceType  string // F1, F2, F3, R1..R5
	TaxTotal     decimal.Decimal
	InvoiceTotal decimal.Decimal
	PreviousHash string // empty for the first record of the chain
	GeneratedAt  time.Time
}

// CancelInput carries the fields the fingerprint of a cancellation (anulación) record covers.
type CancelInput struct {
	ID           InvoiceID
	PreviousHash string
	GeneratedAt  time.Time
}

// DateLayout is AEAT's DD-MM-YYYY.
const DateLayout = "02-01-2006"

// TimestampLayout is the ISO 8601 date, time and zone offset used by FechaHoraHusoGenRegistro.
const TimestampLayout = "2006-01-02T15:04:05-07:00"

// Amount renders a number as AEAT expects: a dot decimal separator and no thousands separators.
// The spec treats one or two decimals as equivalent, so trailing zeros are dropped for the hash only
// when the caller asks for hashing form; XML and QR use two decimals.
func Amount(d decimal.Decimal) string { return d.StringFixed(2) }

func hashAmount(d decimal.Decimal) string {
	// AEAT: "se tratarán indistintamente los valores con una o dos posiciones en los decimales".
	// 123.10 and 123.1 are both valid inputs, but a verifier recomputing from the XML must reach the
	// same digest, so the record is always written with two decimals and hashed with two decimals.
	return Amount(d)
}

func clean(s string) string { return strings.TrimSpace(s) }

// AltaString builds the exact text that is hashed for an invoice (alta) record.
func AltaString(in AltaInput) string {
	return "IDEmisorFactura=" + clean(in.ID.IssuerNIF) +
		"&NumSerieFactura=" + clean(in.ID.Number) +
		"&FechaExpedicionFactura=" + in.ID.IssueDate.Format(DateLayout) +
		"&TipoFactura=" + clean(in.InvoiceType) +
		"&CuotaTotal=" + hashAmount(in.TaxTotal) +
		"&ImporteTotal=" + hashAmount(in.InvoiceTotal) +
		"&Huella=" + clean(in.PreviousHash) +
		"&FechaHoraHusoGenRegistro=" + in.GeneratedAt.Format(TimestampLayout)
}

// CancelString builds the exact text that is hashed for a cancellation record.
func CancelString(in CancelInput) string {
	return "IDEmisorFacturaAnulada=" + clean(in.ID.IssuerNIF) +
		"&NumSerieFacturaAnulada=" + clean(in.ID.Number) +
		"&FechaExpedicionFacturaAnulada=" + in.ID.IssueDate.Format(DateLayout) +
		"&Huella=" + clean(in.PreviousHash) +
		"&FechaHoraHusoGenRegistro=" + in.GeneratedAt.Format(TimestampLayout)
}

// Fingerprint is SHA-256 of the UTF-8 text, as 64 upper-case hexadecimal characters.
func Fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// AltaHash returns the fingerprint of an invoice record.
func AltaHash(in AltaInput) string { return Fingerprint(AltaString(in)) }

// CancelHash returns the fingerprint of a cancellation record.
func CancelHash(in CancelInput) string { return Fingerprint(CancelString(in)) }

// ValidateInvoiceNumber enforces AEAT's limits on NumSerieFactura: 1..60 printable ASCII characters.
func ValidateInvoiceNumber(n string) error {
	n = clean(n)
	if n == "" || len(n) > 60 {
		return errors.New("verifactu: invoice number must be 1 to 60 characters")
	}
	for _, r := range n {
		if r < 32 || r > 126 {
			return fmt.Errorf("verifactu: invoice number %q has a character outside ASCII 32-126", n)
		}
	}
	return nil
}
