package importer

import (
	"strings"
	"testing"
)

const tallyMasters = `<?xml version="1.0" encoding="UTF-8"?>
<ENVELOPE><BODY><IMPORTDATA><REQUESTDATA>
<TALLYMESSAGE><LEDGER NAME="Asha Stores"><PARENT>Sundry Debtors</PARENT><LEDGERMOBILE>98765 43210</LEDGERMOBILE><EMAIL>a@x.in</EMAIL></LEDGER></TALLYMESSAGE>
<TALLYMESSAGE><LEDGER NAME="Bulk &amp; Co&#4;"><PARENT>Sundry Creditors</PARENT></LEDGER></TALLYMESSAGE>
<TALLYMESSAGE><LEDGER NAME="HDFC Bank"><PARENT>Bank Accounts</PARENT></LEDGER></TALLYMESSAGE>
<TALLYMESSAGE><STOCKITEM NAME="Rice 5kg"><BASEUNITS>Nos</BASEUNITS><OPENINGBALANCE> 10 Nos</OPENINGBALANCE>
<GSTDETAILS.LIST><HSNCODE>1006</HSNCODE><STATEWISEDETAILS.LIST><RATEDETAILS.LIST><GSTRATEDUTYHEAD>CGST</GSTRATEDUTYHEAD><GSTRATE> 2.5</GSTRATE></RATEDETAILS.LIST><RATEDETAILS.LIST><GSTRATEDUTYHEAD>IGST</GSTRATEDUTYHEAD><GSTRATE> 5</GSTRATE></RATEDETAILS.LIST></STATEWISEDETAILS.LIST></GSTDETAILS.LIST>
</STOCKITEM></TALLYMESSAGE>
</REQUESTDATA></IMPORTDATA></BODY></ENVELOPE>`

func TestParseTally_PartiesSkipNonParties(t *testing.T) {
	rows, err := ParseTally(strings.NewReader(tallyMasters), TallyParties)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (bank ledger skipped): %+v", len(rows), rows)
	}
	if f := rows[0].Fields; f["legal_name"] != "Asha Stores" || f["party_type"] != "CUSTOMER" || f["phone"] != "98765 43210" || f["currency_code"] != "INR" {
		t.Fatalf("row 1 = %v", f)
	}
	if rows[1].Fields["legal_name"] != "Bulk & Co" || rows[1].Fields["party_type"] != "SUPPLIER" || rows[1].Number != 2 {
		t.Fatalf("row 2 = %+v", rows[1])
	}
}

func TestParseTally_Items(t *testing.T) {
	rows, err := ParseTally(strings.NewReader(tallyMasters), TallyItems)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	f := rows[0].Fields
	if f["name"] != "Rice 5kg" || f["hsn_sac_code"] != "1006" || f["gst_rate"] != "5" || f["opening_qty"] != "10" || f["base_uom_code"] != "Nos" {
		t.Fatalf("item = %v", f)
	}
}

func TestParseTally_UTF16(t *testing.T) {
	u := []byte{0xFF, 0xFE}
	for _, r := range `<ENVELOPE><LEDGER NAME="Zed"><PARENT>Sundry Debtors</PARENT></LEDGER></ENVELOPE>` {
		u = append(u, byte(r), byte(r>>8))
	}
	rows, err := ParseTally(strings.NewReader(string(u)), TallyParties)
	if err != nil || len(rows) != 1 || rows[0].Fields["legal_name"] != "Zed" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestParseTally_RejectsNonXML(t *testing.T) {
	if _, err := ParseTally(strings.NewReader(""), TallyParties); err == nil {
		t.Fatal("empty accepted")
	}
}
