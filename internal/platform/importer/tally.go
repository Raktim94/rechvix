package importer

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf16"
)

// TallyKind selects which master a Tally export is read for.
type TallyKind string

const (
	TallyParties TallyKind = "parties" // Sundry Debtors / Creditors ledgers
	TallyItems   TallyKind = "items"   // stock items

	// Day-book vouchers: one past invoice per Sales or Purchase voucher.
	TallySalesVouchers    TallyKind = "sales-vouchers"
	TallyPurchaseVouchers TallyKind = "purchase-vouchers"
)

// node is a loosely-typed XML element: Tally's exports nest the same tag
// names at several depths, so collecting by element name is more robust
// than a rigid struct.
type node struct {
	name     string
	attrs    map[string]string
	text     strings.Builder
	children []*node
}

func (n *node) first(name string) *node {
	for _, c := range n.children {
		if c.name == name {
			return c
		}
		if d := c.first(name); d != nil {
			return d
		}
	}
	return nil
}

func (n *node) val(name string) string {
	if c := n.first(name); c != nil {
		return strings.TrimSpace(c.text.String())
	}
	return ""
}

var badEntity = regexp.MustCompile(`&#(?:[0-8]|1[0-9]|2[0-9]|3[01]|11|12);`)

// decodeTally turns Tally's usual UTF-16 (or UTF-8) bytes into clean
// UTF-8 and drops the control-character entities (&#4;) Tally writes,
// which encoding/xml refuses.
func decodeTally(raw []byte) []byte {
	if len(raw) >= 2 && ((raw[0] == 0xFF && raw[1] == 0xFE) || (raw[0] == 0xFE && raw[1] == 0xFF)) {
		le := raw[0] == 0xFF
		u := make([]uint16, 0, len(raw)/2)
		for i := 2; i+1 < len(raw); i += 2 {
			if le {
				u = append(u, uint16(raw[i])|uint16(raw[i+1])<<8)
			} else {
				u = append(u, uint16(raw[i+1])|uint16(raw[i])<<8)
			}
		}
		raw = []byte(string(utf16.Decode(u)))
		raw = regexp.MustCompile(`^\s*<\?xml[^>]*\?>`).ReplaceAll(raw, nil)
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	return badEntity.ReplaceAll(raw, nil)
}

// ParseTally reads a Tally Prime/ERP 9 master export (XML) and returns
// rows keyed with Rechvix import field names, ready for the same
// dry-run/commit pipeline as a spreadsheet. Ledgers outside Sundry
// Debtors/Creditors (banks, expenses, taxes) are not parties and are
// skipped; the caller's report shows how many rows were read.
func ParseTally(r io.Reader, kind TallyKind) ([]Row, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("importer: reading Tally XML: %w", err)
	}
	dec := xml.NewDecoder(bytes.NewReader(decodeTally(raw)))
	dec.Strict = false

	want := "LEDGER"
	switch kind {
	case TallyItems:
		want = "STOCKITEM"
	case TallySalesVouchers, TallyPurchaseVouchers:
		want = "VOUCHER"
	}
	var rows []Row
	var stack []*node
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("importer: parsing Tally XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			sawRoot = true
			n := &node{name: strings.ToUpper(t.Name.Local), attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[strings.ToUpper(a.Name.Local)] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.children = append(p.children, n)
			}
			stack = append(stack, n)
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].text.Write(t)
			}
		case xml.EndElement:
			n := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if n.name != want {
				continue
			}
			var fields map[string]string
			switch kind {
			case TallyItems:
				fields = tallyItem(n)
			case TallySalesVouchers:
				fields = tallyVoucher(n, "sales")
			case TallyPurchaseVouchers:
				fields = tallyVoucher(n, "purchase")
			default:
				fields = tallyParty(n)
			}
			if fields != nil {
				rows = append(rows, Row{Number: len(rows) + 1, Fields: fields})
			}
		}
	}
	if !sawRoot {
		return nil, fmt.Errorf("importer: the file is empty or not Tally XML")
	}
	return rows, nil
}

func tallyName(n *node) string {
	if v := strings.TrimSpace(n.attrs["NAME"]); v != "" {
		return v
	}
	return n.val("NAME")
}

func tallyParty(n *node) map[string]string {
	name := tallyName(n)
	var pt string
	switch strings.ToLower(n.val("PARENT")) {
	case "sundry debtors":
		pt = "CUSTOMER"
	case "sundry creditors":
		pt = "SUPPLIER"
	default:
		return nil
	}
	phone := n.val("LEDGERMOBILE")
	if phone == "" {
		phone = n.val("LEDGERPHONE")
	}
	return map[string]string{
		"legal_name": name, "party_type": pt, "currency_code": "INR",
		"phone": phone, "email": n.val("EMAIL"),
	}
}

func tallyItem(n *node) map[string]string {
	name := tallyName(n)
	rate := ""
	// GST is split into duty heads per rate block; the IGST head carries
	// the combined rate Rechvix stores.
	var walk func(*node)
	walk = func(x *node) {
		if x.name == "RATEDETAILS.LIST" && strings.EqualFold(x.val("GSTRATEDUTYHEAD"), "IGST") {
			rate = x.val("GSTRATE")
		}
		for _, c := range x.children {
			walk(c)
		}
	}
	walk(n)
	qty := ""
	if f := strings.Fields(n.val("OPENINGBALANCE")); len(f) > 0 {
		qty = CleanNumber(f[0])
	}
	return map[string]string{
		"name": name, "base_uom_code": n.val("BASEUNITS"), "hsn_sac_code": n.val("HSNCODE"),
		"gst_rate": CleanNumber(rate), "opening_qty": qty,
	}
}

// tallyVoucher reads one Sales or Purchase voucher as a past invoice. The
// total is the party ledger's own entry (what the party owes or is owed,
// tax included). Settlement vouchers (Receipt/Payment) are separate
// vouchers in Tally and are not read, so paid is left empty and the
// invoice arrives fully due; cancelled and optional vouchers are skipped.
func tallyVoucher(n *node, want string) map[string]string {
	if !strings.EqualFold(strings.TrimSpace(n.attrs["VCHTYPE"]), want) && !strings.EqualFold(n.val("VOUCHERTYPENAME"), want) {
		return nil
	}
	if strings.EqualFold(n.val("ISCANCELLED"), "Yes") || strings.EqualFold(n.val("ISOPTIONAL"), "Yes") {
		return nil
	}
	party := n.val("PARTYLEDGERNAME")
	if party == "" {
		party = n.val("PARTYNAME")
	}
	total := ""
	var walk func(*node)
	walk = func(x *node) {
		if (x.name == "ALLLEDGERENTRIES.LIST" || x.name == "LEDGERENTRIES.LIST") && total == "" && strings.EqualFold(x.val("LEDGERNAME"), party) {
			total = strings.TrimPrefix(CleanNumber(x.val("AMOUNT")), "-")
		}
		for _, c := range x.children {
			walk(c)
		}
	}
	walk(n)
	date := n.val("DATE")
	if len(date) == 8 {
		date = date[:4] + "-" + date[4:6] + "-" + date[6:]
	}
	return map[string]string{
		"party_name": party, "document_number": n.val("VOUCHERNUMBER"), "document_date": date,
		"total": total, "notes": n.val("NARRATION"),
	}
}
