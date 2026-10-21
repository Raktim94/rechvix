package domain

import (
	"errors"
	"net/url"
	"strings"

	"github.com/shopspring/decimal"
)

// Environment selects AEAT's test (Portal de Pruebas Externas) or production QR service.
type Environment string

const (
	EnvTest       Environment = "test"
	EnvProduction Environment = "production"
)

const (
	qrBaseTest       = "https://prewww2.aeat.es/wlpl/TIKE-CONT/ValidarQR"
	qrBaseProduction = "https://www2.agenciatributaria.gob.es/wlpl/TIKE-CONT/ValidarQR"
)

// QRLegend is the line AEAT requires above the code; the verifiable-invoice phrase goes below it.
const (
	QRHeading    = "QR tributario:"
	QRVerifiable = "Factura verificable en la sede electrónica de la AEAT"
	QRShort      = "VERI*FACTU"
)

// QRURL builds the verification URL for a VERI*FACTU invoice. Parameter values are URL-encoded as
// AEAT requires (an "&" or "/" in the invoice number must not break the query).
func QRURL(env Environment, id InvoiceID, total decimal.Decimal) (string, error) {
	if err := ValidateInvoiceNumber(id.Number); err != nil {
		return "", err
	}
	nif := clean(id.IssuerNIF)
	if len(nif) != 9 {
		return "", errors.New("verifactu: issuer NIF must be 9 characters")
	}
	base := qrBaseProduction
	if env != EnvProduction {
		base = qrBaseTest
	}
	// url.QueryEscape encodes spaces as "+"; AEAT's own example uses %20-style percent encoding, so
	// normalise to that to stay inside the published format.
	enc := func(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }
	return base + "?nif=" + enc(nif) + "&numserie=" + enc(clean(id.Number)) +
		"&fecha=" + id.IssueDate.Format(DateLayout) + "&importe=" + Amount(total), nil
}
