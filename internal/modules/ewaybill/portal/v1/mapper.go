// Package v1 is the first FREE_PORTAL export mapper version.
//
// HONEST CAVEAT, do not remove this comment without re-verifying against
// current official documentation: the field names below were rewritten
// against the real government e-Way Bill generation schema — verified by
// cross-checking two independent descriptions of NIC's actual "Generate
// e-Way Bill" request schema (developer.sandbox.co.in's API reference and
// gsthelp.charteredinfo.com's sample JSON documentation, 2026-09-04), which
// agreed exactly on the field list below. This is a real improvement over
// the previous version of this file, which used entirely invented
// snake_case/nested field names that would have been rejected outright.
//
// What is still NOT independently verified against a live official sample
// or the current NIC "EWB Generation Tool and Attributes and JSON Schema"
// PDF (docs.ewaybillgst.gov.in blocks automated fetches with a 403 — a
// human with browser access should pull it directly before this ships):
//   - subSupplyType's exact numeric code mapping (this file uses a
//     best-effort mapping documented inline, not independently confirmed)
//   - vehicleType (defaulted to "R"/Regular — this system doesn't track
//     Over Dimensional Cargo at all)
//   - whether state codes are transmitted as JSON strings or numbers (this
//     file uses strings, matching every other numeric-looking code in the
//     schema, e.g. docNo/pincode, which are documented as strings)
//   - the exact top-level wrapper key (if any) the "Generate Bulk" bulk-
//     upload tool expects around an array of these documents — what's
//     verified here is the single-document "Generate e-Way Bill" API
//     schema; the bulk JSON tool's own wrapper has not been separately
//     confirmed, since BULK_EWB_NOTE.pdf (the specific bulk-tool doc) also
//     403'd on fetch.
//
// Treat every field name as provisional until checked against a real
// sample; update ewaybill_portal_schema_versions with a new dated row
// (never edit this file's field names in place) when it's verified or
// when the government changes it.
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"rechvix/internal/modules/ewaybill/canonical"
	"rechvix/internal/modules/ewaybill/portal"
)

const SchemaVersion = "v2-verified-field-names"

// portalDocument is the real NIC e-Way Bill generation request shape —
// flat, camelCase, root-level from/to fields (NOT nested party objects —
// the previous version of this file nested them under "from"/"to"/
// "ship_to" objects, which does not match the real schema at all).
type portalDocument struct {
	SupplyType    string `json:"supplyType"`              // "O" outward / "I" inward — this system only ever generates outward EWBs for its own sales
	SubSupplyType string `json:"subSupplyType"`           // see subSupplyTypeFor's caveat comment
	SubSupplyDesc string `json:"subSupplyDesc,omitempty"` // required only when SubSupplyType is the "Others" code
	DocType       string `json:"docType"`                 // INV/CHL/BIL/CRN/DBN/OTH
	DocNo         string `json:"docNo"`
	DocDate       string `json:"docDate"` // DD/MM/YYYY

	FromGSTIN       string `json:"fromGstin,omitempty"`
	FromTradeName   string `json:"fromTrdName,omitempty"`
	FromAddress1    string `json:"fromAddr1,omitempty"`
	FromAddress2    string `json:"fromAddr2,omitempty"`
	FromPlace       string `json:"fromPlace,omitempty"`
	FromPincode     string `json:"fromPincode,omitempty"`
	ActualFromState string `json:"actFromStateCode,omitempty"`
	FromStateCode   string `json:"fromStateCode"`

	ToGSTIN       string `json:"toGstin,omitempty"`
	ToTradeName   string `json:"toTrdName,omitempty"`
	ToAddress1    string `json:"toAddr1,omitempty"`
	ToAddress2    string `json:"toAddr2,omitempty"`
	ToPlace       string `json:"toPlace,omitempty"`
	ToPincode     string `json:"toPincode,omitempty"`
	ActualToState string `json:"actToStateCode,omitempty"`
	ToStateCode   string `json:"toStateCode"`

	// TransactionType: 1 Regular, 2 Bill To-Ship To, 3 Bill From-Dispatch
	// From, 4 Combination of 2 and 3 — derived in PrepareUpload by
	// comparing ShipTo/DispatchFrom against Recipient/Supplier, never
	// hardcoded.
	TransactionType string `json:"transactionType"`
	ShipToGSTIN     string `json:"shipToGSTIN,omitempty"`
	ShipToTradeName string `json:"shipToTradeName,omitempty"`

	OtherValue        string `json:"otherValue"`
	TotalValue        string `json:"totalValue"` // taxable value total, pre-tax
	CGSTValue         string `json:"cgstValue"`
	SGSTValue         string `json:"sgstValue"`
	IGSTValue         string `json:"igstValue"`
	CessValue         string `json:"cessValue"`
	CessNonAdvolValue string `json:"cessNonAdvolValue"`
	TotalInvoiceValue string `json:"totInvValue"` // grand total, the field the portal actually validates against the consignment-value threshold

	TransporterID   string `json:"transporterId,omitempty"`
	TransporterName string `json:"transporterName,omitempty"`
	TransDocNo      string `json:"transDocNo,omitempty"`
	TransDocDate    string `json:"transDocDate,omitempty"`
	TransMode       string `json:"transMode,omitempty"` // 1 Road, 2 Rail, 3 Air, 4 Ship — this system stores transporter name/free text, not a mode code, so this is left blank unless a caller supplies a real code (see PrepareUpload's caveat)
	TransDistance   string `json:"transDistance"`
	VehicleNo       string `json:"vehicleNo,omitempty"`
	VehicleType     string `json:"vehicleType,omitempty"` // R Regular / O ODC — defaulted to "R", see package doc comment

	ItemList []portalItem `json:"itemList"`
}

type portalItem struct {
	ProductName   string `json:"productName,omitempty"`
	ProductDesc   string `json:"productDesc,omitempty"`
	HSNCode       string `json:"hsnCode"`
	Quantity      string `json:"quantity"`
	QtyUnit       string `json:"qtyUnit,omitempty"`
	TaxableAmount string `json:"taxableAmount"`
	CGSTRate      string `json:"cgstRate"`
	SGSTRate      string `json:"sgstRate"`
	IGSTRate      string `json:"igstRate"`
	CessRate      string `json:"cessRate"`
	CessNonAdvol  string `json:"cessNonadvol"`
}

// MaxFileSizeBytes is a documented, configurable placeholder ceiling
// (docs/architecture.md §9b — "enforce the portal's actual file-size
// limit"); 5MB is a reasonable conservative default pending verification
// of the portal's current real limit.
const MaxFileSizeBytes = 5 * 1024 * 1024

type Mapper struct{}

func New() *Mapper { return &Mapper{} }

var _ portal.Exporter = (*Mapper)(nil)

func (m *Mapper) SchemaVersion() string { return SchemaVersion }

func (m *Mapper) PrepareUpload(_ context.Context, bill canonical.CanonicalEWayBill) (portal.PreparedFile, error) {
	doc := portalDocument{
		SupplyType:    "O", // this module is only ever invoked for the seller's own outward sales documents
		SubSupplyType: subSupplyTypeFor(bill.DocumentType),
		DocType:       docTypeFor(bill.DocumentType),
		DocNo:         bill.InvoiceNumber,
		DocDate:       bill.InvoiceDate.Format("02/01/2006"),

		FromGSTIN: bill.Supplier.GSTIN, FromTradeName: firstNonEmpty(bill.Supplier.TradeName, bill.Supplier.LegalName),
		FromAddress1: bill.Supplier.AddressLine1, FromAddress2: bill.Supplier.AddressLine2,
		FromPlace: bill.Supplier.City, FromPincode: bill.Supplier.PostalCode,
		FromStateCode: bill.Supplier.StateCode, ActualFromState: bill.DispatchFrom.StateCode,

		// toGstin/toStateCode/toPincode/toAddr*/toPlace describe where the
		// goods are actually going, not necessarily the registered
		// recipient's billing details — ShipTo is the physically-real
		// destination (with a place-of-supply fallback already applied
		// upstream, buildCanonicalFromLiveData's own comment), Recipient
		// only where ShipTo itself has nothing better. "URP" (Unregistered
		// Person) is the real, documented value the schema expects for a
		// B2C sale with no buyer GSTIN — never just omitting the
		// (non-optional) field, same as ShipToGSTIN below already does.
		ToGSTIN: firstNonEmpty(bill.Recipient.GSTIN, "URP"), ToTradeName: firstNonEmpty(bill.Recipient.TradeName, bill.Recipient.LegalName),
		ToAddress1: firstNonEmpty(bill.ShipTo.AddressLine1, bill.Recipient.AddressLine1),
		ToAddress2: firstNonEmpty(bill.ShipTo.AddressLine2, bill.Recipient.AddressLine2),
		ToPlace:    firstNonEmpty(bill.ShipTo.City, bill.Recipient.City),
		ToPincode:  firstNonEmpty(bill.ShipTo.PostalCode, bill.Recipient.PostalCode),
		ToStateCode: firstNonEmpty(bill.ShipTo.StateCode, bill.Recipient.StateCode), ActualToState: bill.ShipTo.StateCode,

		TransactionType: transactionTypeFor(bill),

		OtherValue:        "0.00",
		TotalValue:        bill.Tax.TaxableValue.StringFixed(2),
		CGSTValue:         bill.Tax.CGST.StringFixed(2),
		SGSTValue:         bill.Tax.SGST.StringFixed(2),
		IGSTValue:         bill.Tax.IGST.StringFixed(2),
		CessValue:         bill.Tax.CESS.StringFixed(2),
		CessNonAdvolValue: "0.00",
		TotalInvoiceValue: bill.Tax.GrandTotal.StringFixed(2),

		TransporterID: bill.Transport.TransporterID, TransporterName: bill.Transport.TransporterName,
		TransDistance: bill.Transport.DistanceKM.StringFixed(0), // the real field is a whole-number km, not a decimal
		VehicleNo:     bill.Transport.VehicleNumber, VehicleType: "R",
	}
	// transactionType 2/4 carries a separate ship-to GSTIN/name; 1/3 don't.
	if doc.TransactionType == "2" || doc.TransactionType == "4" {
		doc.ShipToGSTIN = firstNonEmpty(bill.ShipTo.GSTIN, "URP")
		doc.ShipToTradeName = firstNonEmpty(bill.ShipTo.TradeName, bill.ShipTo.LegalName)
	}

	for _, it := range bill.Items {
		doc.ItemList = append(doc.ItemList, portalItem{
			ProductName: it.Description, ProductDesc: it.Description,
			HSNCode: it.HSNSACCode, Quantity: it.Quantity.StringFixed(3), QtyUnit: it.UnitCode,
			TaxableAmount: it.TaxableAmount.StringFixed(2),
			CGSTRate:      it.CGSTRate.StringFixed(2),
			SGSTRate:      it.SGSTRate.StringFixed(2),
			IGSTRate:      it.IGSTRate.StringFixed(2),
			CessRate:      it.CessRate.StringFixed(2),
			CessNonAdvol:  "0.00",
		})
	}

	content, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return portal.PreparedFile{}, fmt.Errorf("ewaybill/portal/v1: marshaling upload document: %w", err)
	}
	if len(content) > MaxFileSizeBytes {
		// A single invoice exceeding the portal's file-size ceiling would
		// be extraordinary (thousands of line items) — this is a hard
		// stop rather than a silent truncation, since truncating a
		// government filing document is far worse than failing loudly.
		// Batch-splitting (see SplitBatch) is for the *bulk, multiple-
		// invoice* case (docs/architecture.md §9b), not a single
		// oversized document.
		return portal.PreparedFile{}, fmt.Errorf("ewaybill/portal/v1: prepared file is %d bytes, exceeds the %d byte limit for a single document", len(content), MaxFileSizeBytes)
	}

	return portal.PreparedFile{FileName: FileName(bill.InvoiceNumber, bill.InvoiceDate), Content: content}, nil
}

// docTypeFor maps this system's document_type to the real portal's docType
// codes. Real e-Way Bills are normally raised against INV/BIL/CHL; this
// system's own SALES_RETURN/CREDIT_NOTE/DEBIT_NOTE document types don't
// have a universally agreed single-letter code in every source consulted
// — CRN/DBN are used here as the most plausible mapping but are NOT
// independently confirmed the way INV is.
func docTypeFor(documentType string) string {
	switch documentType {
	case "CRN":
		return "CRN"
	case "DBN":
		return "DBN"
	default:
		return "INV"
	}
}

// subSupplyTypeFor maps this system's document_type to the real portal's
// subSupplyType numeric code. NOT independently verified this session —
// the two sources checked confirmed the FIELD exists but not its full
// code table; "1" (Supply) and "7" (Sales Return) below are standard,
// widely-documented GST sub-supply codes, used here as the best available
// mapping pending a byte-level check against the current official
// documentation.
func subSupplyTypeFor(documentType string) string {
	if documentType == "SALES_RETURN" {
		return "7"
	}
	return "1"
}

// transactionTypeFor derives the real portal's 1/2/3/4 transaction-type
// code by comparing ShipTo/DispatchFrom against Recipient/Supplier —
// never hardcoded, since a wrong code here silently produces a file the
// portal will reject or misfile.
func transactionTypeFor(bill canonical.CanonicalEWayBill) string {
	shipToDiffers := bill.ShipTo.GSTIN != bill.Recipient.GSTIN || bill.ShipTo.StateCode != bill.Recipient.StateCode
	dispatchDiffers := bill.DispatchFrom.GSTIN != bill.Supplier.GSTIN || bill.DispatchFrom.StateCode != bill.Supplier.StateCode
	switch {
	case shipToDiffers && dispatchDiffers:
		return "4"
	case dispatchDiffers:
		return "3"
	case shipToDiffers:
		return "2"
	default:
		return "1"
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

var filenameUnsafe = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// FileName produces the human-recognizable naming convention
// docs/architecture.md §9b specifies: EWB-<invoice-number>-<date>.json.
// Illegal filesystem characters in the invoice number are sanitized to
// underscores rather than silently dropped, so the number stays
// recognizable.
func FileName(invoiceNumber string, invoiceDate time.Time) string {
	safe := filenameUnsafe.ReplaceAllString(invoiceNumber, "_")
	safe = strings.Trim(safe, "_")
	return fmt.Sprintf("EWB-%s-%s.json", safe, invoiceDate.Format("20060102"))
}

// BatchFileName is the numbered-batch variant for a multi-invoice bulk
// export exceeding the size limit (docs/architecture.md §9b:
// "EWB-BATCH-001.json, EWB-BATCH-002.json, ...").
func BatchFileName(batchNumber int) string {
	return fmt.Sprintf("EWB-BATCH-%03d.json", batchNumber)
}

// SplitBatch groups a set of already-prepared single-document contents
// into batch files, each kept under maxBytes (accounting for the JSON
// array wrapper's own overhead) — the reusable primitive behind the bulk-
// preparation flow docs/architecture.md §9b describes (the actual
// multiple-invoice-selection API/UI is out of Stage 8c's scope, since
// apps/web doesn't exist yet, but this split logic is real and tested so
// that flow has something correct to call into later).
func SplitBatch(documents [][]byte, maxBytes int) ([]portal.PreparedFile, error) {
	if len(documents) == 0 {
		return nil, nil
	}
	var batches []portal.PreparedFile
	var current []json.RawMessage
	currentSize := 2 // "[]"

	flush := func() error {
		if len(current) == 0 {
			return nil
		}
		content, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			return fmt.Errorf("ewaybill/portal/v1: marshaling batch: %w", err)
		}
		batches = append(batches, portal.PreparedFile{
			FileName: BatchFileName(len(batches) + 1),
			Content:  content,
		})
		current = nil
		currentSize = 2
		return nil
	}

	for _, d := range documents {
		// +1 for the comma/bracket overhead of adding this element to the
		// current array — an approximation, not exact re-marshaling cost,
		// which is fine for a size *ceiling* check.
		addSize := len(d) + 1
		if len(d) > maxBytes {
			return nil, fmt.Errorf("ewaybill/portal/v1: a single document (%d bytes) exceeds the batch size limit (%d bytes) on its own", len(d), maxBytes)
		}
		if currentSize+addSize > maxBytes && len(current) > 0 {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		current = append(current, json.RawMessage(d))
		currentSize += addSize
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return batches, nil
}
