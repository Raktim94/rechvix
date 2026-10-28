package importer

import "testing"

var partyFields = []Field{
	{Name: "legal_name", Label: "Name", Required: true, Synonyms: []string{"Party Name", "Customer Name"}},
	{Name: "phone", Label: "Phone", Synonyms: []string{"Phone No.", "Mobile"}},
	{Name: "opening", Label: "Opening", Numeric: true, Synonyms: []string{"Opening Balance"}},
	{Name: "party_type", Label: "Type"},
}

func TestApplyMapping_VyaparHeadersAutoMap(t *testing.T) {
	rows := []Row{{Number: 1, Fields: map[string]string{"Party Name": "Asha Stores", "Phone No.": "98765 43210", "Opening Balance": "₹1,23,456.50", "Extra": "x"}}}
	got, err := ApplyMapping(rows, partyFields, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := got[0].Fields
	if f["legal_name"] != "Asha Stores" || f["phone"] != "98765 43210" || f["opening"] != "123456.50" {
		t.Fatalf("fields = %v", f)
	}
	if _, ok := f["Extra"]; ok {
		t.Fatal("unmapped column leaked through")
	}
}

func TestApplyMapping_ExplicitAndConstant(t *testing.T) {
	rows := []Row{{Number: 3, Fields: map[string]string{"A": "Bob"}}}
	got, err := ApplyMapping(rows, partyFields, map[string]string{"legal_name": "A", "party_type": "=CUSTOMER"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Number != 3 || got[0].Fields["legal_name"] != "Bob" || got[0].Fields["party_type"] != "CUSTOMER" {
		t.Fatalf("got %+v", got[0])
	}
}

func TestApplyMapping_RejectsUnknownFieldAndColumn(t *testing.T) {
	rows := []Row{{Number: 1, Fields: map[string]string{"A": "x"}}}
	if _, err := ApplyMapping(rows, partyFields, map[string]string{"nope": "A"}); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := ApplyMapping(rows, partyFields, map[string]string{"legal_name": "Missing"}); err == nil {
		t.Fatal("missing column accepted")
	}
}

func TestInspect_SuggestsEachHeaderOnce(t *testing.T) {
	rows := []Row{{Number: 1, Fields: map[string]string{"Mobile": "1", "Phone No.": "2"}}}
	ins := Inspect(rows, partyFields)
	if ins.Suggested["phone"] == "" || ins.Total != 1 || len(ins.Sample) != 1 {
		t.Fatalf("inspection = %+v", ins)
	}
}

func TestParseMapping(t *testing.T) {
	if m, err := ParseMapping(""); m != nil || err != nil {
		t.Fatalf("empty -> %v, %v", m, err)
	}
	if _, err := ParseMapping("[1]"); err == nil {
		t.Fatal("non-object accepted")
	}
}
