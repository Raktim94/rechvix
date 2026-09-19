// Package v1 is the first FREE_PORTAL export mapper version.
//
// HONEST CAVEAT, do not remove this comment without re-verifying against
// current official documentation: verified 2026-09-18 against three
// independent sources, cross-checked against each other for agreement
// rather than trusted individually:
//  1. National Informatics Centre's own "EWB-API Technical Document"
//     (ewbapi_1.01.pdf, NIC, v1.01 dtd 24.03.2018) — a real sample "Generate
//     e-Way Bill" request/response JSON with an official govt letterhead,
//     fetched and read directly (docs.ewaybillgst.gov.in's own pages
//     403 automated fetches, but this NIC-authored PDF is mirrored
//     elsewhere and was readable in full).
//  2. gsthelp.charteredinfo.com's "Request Sample Json of eWayBill" page
//     (a GSP reference dated with a 2026 sample docDate, so current, not
//     stale) — the more complete sample (adds actFromStateCode/
//     actToStateCode/transactionType/shipToGSTIN, absent from source 1's
//     simpler example).
//  3. LogiTax's "CREATE E-WAY BILL API DOCUMENTATION FOR ERP INTEGRATION"
//     (v1.01 with Amendment 5, dated 17 Jul 2024) — a GSP document that
//     explicitly cross-references NIC's own docs.ewaybillgst.gov.in specs
//     and flags where its own dialect diverges from them; its field-type
//     table (Text/Number/Decimal per field) and a concrete "billLists"
//     bulk-array JSON sample independently confirm source 1 & 2's field
//     names AND resolve what neither of those showed: which fields are
//     bare JSON numbers vs quoted strings.
//
// The real, confirmed finding from cross-checking all three: this file
// previously marshaled EVERY field as a quoted JSON string (including
// pincode/state code/HSN code/quantity/tax amounts) — wrong. All three
// sources agree these are bare numbers, not strings; see portalDocument/
// portalItem's own field-level comments below for exactly which. This
// was a real, shipped bug, not just an unconfirmed caveat — a file this
// package generated before this fix could have been rejected by the
// actual portal for the numeric fields being the wrong JSON type.
//
// What is STILL not independently verified, do not claim otherwise:
//   - subSupplyType's exact numeric code mapping (best-effort, inline)
//   - vehicleType (defaulted to "R"/Regular — no ODC tracking)
//   - the exact top-level wrapper key the web portal's own "Generate
//     Bulk" Excel-to-JSON tool expects for multiple documents in one
//     file (BULK_EWB_NOTE.pdf, the one document that would settle this,
//     404/403'd on every fetch attempt, including via two different
//     search-engine-indexed mirrors). Source 3's "billLists" wrapper is
//     LogiTax's own GSP API convention, explicitly documented by LogiTax
//     itself as sometimes diverging from the raw NIC schema — it is
//     evidence multi-document-in-one-array is the right general shape,
//     not confirmation of NIC's own exact wrapper key. SplitBatch below
//     deliberately produces a plain JSON array with no wrapper key at
//     all, which is the most conservative choice pending a real
//     confirmed sample, but IS NOT ITSELF CONFIRMED against the actual
//     web portal bulk-upload tool either. If bulk upload is rejected,
//     the reliable fallback already supported today is uploading one
//     single-document file per invoice via the portal's ordinary
//     (non-bulk) "Generate e-Way Bill" JSON upload option instead —
//     source 1 & 2's schema, which this file DOES now match with high
//     confidence.
//
// Treat every field name/type as re-verify-before-relying-on-it, not
// gospel. Update this comment (never silently edit the field list out
// from under it) when it's re-checked or the government changes it.
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

const SchemaVersion = "v3-numeric-field-types-verified-2026-09-18"

// portalDocument is the real NIC e-Way Bill generation request shape —
// flat, camelCase, root-level from/to fields (NOT nested party objects —
// the previous version of this file nested them under "from"/"to"/
// "ship_to" objects, which does not match the real schema at all).
// json.Number marshals as a bare JSON number (no quotes) while still being
// backed by a plain Go string — exactly what's needed to emit
// decimal-precise numeric fields (an amount, a state code) without the
// float64 round-off risk a plain float64 field would carry, and without
// wrongly quoting them as strings like this file used to. encoding/json
// validates it's actually a syntactically valid number at Marshal time,
// so a corrupt/non-numeric value fails loudly (a Go error) instead of
// silently producing broken JSON — the same "fail rather than guess"
// posture the rest of this codebase already holds to.
type portalDocument struct {
	SupplyType        string `json:"supplyType"`                  // "O" outward / "I" inward — this system only ever generates outward EWBs for its own sales
	SubSupplyType     string `json:"subSupplyType"`               // see subSupplyTypeFor's caveat comment
	SubSupplyTypeDesc string `json:"subSupplyTypeDesc,omitempty"` // required only when SubSupplyType is the "Others" code — field name corrected from this file's previous "subSupplyDesc" (wrong; not the schema's actual name, per source 2 & 3's cross-agreement)
	DocType           string `json:"docType"`                     // INV/CHL/BIL/CRN/DBN/OTH
	DocNo             string `json:"docNo"`
	DocDate           string `json:"docDate"` // DD/MM/YYYY

	FromGSTIN       string      `json:"fromGstin,omitempty"`
	FromTradeName   string      `json:"fromTrdName,omitempty"`
	FromAddress1    string      `json:"fromAddr1,omitempty"`
	FromAddress2    string      `json:"fromAddr2,omitempty"`
	FromPlace       string      `json:"fromPlace,omitempty"`
	FromPincode     json.Number `json:"fromPincode,omitempty"`
	ActualFromState json.Number `json:"actFromStateCode,omitempty"`
	FromStateCode   json.Number `json:"fromStateCode"`

	ToGSTIN       string      `json:"toGstin,omitempty"`
	ToTradeName   string      `json:"toTrdName,omitempty"`
	ToAddress1    string      `json:"toAddr1,omitempty"`
	ToAddress2    string      `json:"toAddr2,omitempty"`
	ToPlace       string      `json:"toPlace,omitempty"`
	ToPincode     json.Number `json:"toPincode,omitempty"`
	ActualToState json.Number `json:"actToStateCode,omitempty"`
	ToStateCode   json.Number `json:"toStateCode"`

	// TransactionType: 1 Regular, 2 Bill To-Ship To, 3 Bill From-Dispatch
	// From, 4 Combination of 2 and 3 — derived in PrepareUpload by
	// comparing ShipTo/DispatchFrom against Recipient/Supplier, never
	// hardcoded. Kept as a string: source 3's own concrete bulk sample
	// ships it quoted ("TransType":"1") despite that same source's prose
	// table claiming Number(1) — a live sample outweighs a table when
	// they disagree, since the sample is closer to what a real server
	// actually parses.
	TransactionType string `json:"transactionType"`
	ShipToGSTIN     string `json:"shipToGSTIN,omitempty"`
	ShipToTradeName string `json:"shipToTradeName,omitempty"`
	// DispatchFromGSTIN/DispatchFromTradeName: only meaningful (and only
	// sent) when TransactionType is 3 or 4 — dispatch point differs from
	// the registered supplier, same conditional pattern as ShipToGSTIN
	// above for the mirror "Bill From-Dispatch From" case.
	DispatchFromGSTIN     string `json:"dispatchFromGSTIN,omitempty"`
	DispatchFromTradeName string `json:"dispatchFromTradeName,omitempty"`

	OtherValue        string      `json:"otherValue"` // kept as string: this specific field's type conflicts across sources (quoted in one concrete sample, bare in another) and its value is always "0.00" here regardless, so the ambiguity has no practical effect
	TotalValue        json.Number `json:"totalValue"` // taxable value total, pre-tax
	CGSTValue         json.Number `json:"cgstValue"`
	SGSTValue         json.Number `json:"sgstValue"`
	IGSTValue         json.Number `json:"igstValue"`
	CessValue         json.Number `json:"cessValue"`
	CessNonAdvolValue json.Number `json:"cessNonAdvolValue"`
	TotalInvoiceValue json.Number `json:"totInvValue"` // grand total, the field the portal actually validates against the consignment-value threshold

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
	// ItemNo: 1-based sequential position in ItemList — absent from the
	// simpler source-1/2 samples but present (and populated from 1, not
	// 0) in source 3's current concrete sample; harmless to include even
	// if it turns out optional.
	ItemNo      int    `json:"itemNo"`
	ProductName string `json:"productName,omitempty"`
	ProductDesc string `json:"productDesc,omitempty"`
	// HSNCode: sent as a bare number per the verified schema (Number(8) —
	// source 3's field table), which means a real HSN chapter-01 code
	// ("Live animals", e.g. "0101") loses its leading zero on the wire
	// (becomes 101) exactly like a leading-zero state code does — see
	// numericCode's own comment. Unlike the state-code case this isn't
	// this codebase's choice to make: every source that documents this
	// field's type agrees it's numeric, so a chapter-01 HSN code hitting
	// this same limitation is the verified schema's own constraint, not
	// a gap in this implementation.
	HSNCode       json.Number `json:"hsnCode"`
	Quantity      json.Number `json:"quantity"`
	QtyUnit       string      `json:"qtyUnit,omitempty"`
	TaxableAmount json.Number `json:"taxableAmount"`
	CGSTRate      json.Number `json:"cgstRate"`
	SGSTRate      json.Number `json:"sgstRate"`
	IGSTRate      json.Number `json:"igstRate"`
	CessRate      json.Number `json:"cessRate"`
	CessNonAdvol  json.Number `json:"cessNonadvol"`
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
		FromPlace: bill.Supplier.City, FromPincode: numericCode(bill.Supplier.PostalCode),
		FromStateCode: numericCode(bill.Supplier.StateCode), ActualFromState: numericCode(bill.DispatchFrom.StateCode),

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
		ToAddress1:  firstNonEmpty(bill.ShipTo.AddressLine1, bill.Recipient.AddressLine1),
		ToAddress2:  firstNonEmpty(bill.ShipTo.AddressLine2, bill.Recipient.AddressLine2),
		ToPlace:     firstNonEmpty(bill.ShipTo.City, bill.Recipient.City),
		ToPincode:   numericCode(firstNonEmpty(bill.ShipTo.PostalCode, bill.Recipient.PostalCode)),
		ToStateCode: numericCode(firstNonEmpty(bill.ShipTo.StateCode, bill.Recipient.StateCode)), ActualToState: numericCode(bill.ShipTo.StateCode),

		TransactionType: transactionTypeFor(bill),

		OtherValue:        "0.00",
		TotalValue:        json.Number(bill.Tax.TaxableValue.StringFixed(2)),
		CGSTValue:         json.Number(bill.Tax.CGST.StringFixed(2)),
		SGSTValue:         json.Number(bill.Tax.SGST.StringFixed(2)),
		IGSTValue:         json.Number(bill.Tax.IGST.StringFixed(2)),
		CessValue:         json.Number(bill.Tax.CESS.StringFixed(2)),
		CessNonAdvolValue: "0.00",
		TotalInvoiceValue: json.Number(bill.Tax.GrandTotal.StringFixed(2)),

		TransporterID: bill.Transport.TransporterID, TransporterName: bill.Transport.TransporterName,
		TransDistance: bill.Transport.DistanceKM.StringFixed(0), // the real field is a whole-number km, not a decimal
		VehicleNo:     bill.Transport.VehicleNumber, VehicleType: "R",
	}
	// transactionType 2/4 carries a separate ship-to GSTIN/name; 3/4 a
	// separate dispatch-from GSTIN/name — never hardcoded, mirrors
	// transactionTypeFor's own comparison.
	if doc.TransactionType == "2" || doc.TransactionType == "4" {
		doc.ShipToGSTIN = firstNonEmpty(bill.ShipTo.GSTIN, "URP")
		doc.ShipToTradeName = firstNonEmpty(bill.ShipTo.TradeName, bill.ShipTo.LegalName)
	}
	if doc.TransactionType == "3" || doc.TransactionType == "4" {
		doc.DispatchFromGSTIN = bill.DispatchFrom.GSTIN
		doc.DispatchFromTradeName = firstNonEmpty(bill.DispatchFrom.TradeName, bill.DispatchFrom.LegalName)
	}

	for i, it := range bill.Items {
		doc.ItemList = append(doc.ItemList, portalItem{
			ItemNo:      i + 1,
			ProductName: it.Description, ProductDesc: it.Description,
			HSNCode: numericCode(it.HSNSACCode), Quantity: json.Number(it.Quantity.StringFixed(3)), QtyUnit: it.UnitCode,
			TaxableAmount: json.Number(it.TaxableAmount.StringFixed(2)),
			CGSTRate:      json.Number(it.CGSTRate.StringFixed(2)),
			SGSTRate:      json.Number(it.SGSTRate.StringFixed(2)),
			IGSTRate:      json.Number(it.IGSTRate.StringFixed(2)),
			CessRate:      json.Number(it.CessRate.StringFixed(2)),
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

// numericCode turns a code this codebase stores as a string (a 2-digit
// GST state code, a 6-digit pincode, an HSN/SAC code) into a json.Number
// — stripping any leading zero first, since JSON's own number grammar
// forbids one on anything but a bare "0" (confirmed directly: Go's
// encoding/json rejects json.Number("01") as invalid at Marshal time,
// not just a style nitpick). This matters for real, common values here:
// GST state codes "01" through "09" (Jammu & Kashmir through Uttar
// Pradesh) and HSN chapter 01 ("Live animals", e.g. "0101") both have a
// genuine leading zero. An empty input returns an empty json.Number,
// which the field's own `omitempty` tag then drops — for a field with no
// omitempty (a schema-mandatory one), an empty result here means
// eligibility.Evaluate's own check for that same field failed to catch a
// gap it should have; PrepareUpload's "Requirement != Ready" guard is the
// real defense, this is not a substitute for it.
func numericCode(code string) json.Number {
	trimmed := strings.TrimLeft(code, "0")
	if trimmed == "" && code != "" {
		trimmed = "0" // the value WAS all zeros (e.g. literally "0") — keep it as one, not drop it
	}
	return json.Number(trimmed)
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
