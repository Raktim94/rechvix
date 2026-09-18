package app

// gstStateNames mirrors migrations/0015_gstindia.up.sql's gst_state_codes
// seed data (the same list apps/web/src/lib/gstStateCodes.ts already
// duplicates on the frontend, for the same reason: BuildInvoiceData has no
// business pulling in the gstindia module just to label one printed
// field). If the government adds/changes a code, update this file, that
// migration, and gstStateCodes.ts together — they must stay in sync.
var gstStateNames = map[string]string{
	"01": "Jammu and Kashmir",
	"02": "Himachal Pradesh",
	"03": "Punjab",
	"04": "Chandigarh",
	"05": "Uttarakhand",
	"06": "Haryana",
	"07": "Delhi",
	"08": "Rajasthan",
	"09": "Uttar Pradesh",
	"10": "Bihar",
	"11": "Sikkim",
	"12": "Arunachal Pradesh",
	"13": "Nagaland",
	"14": "Manipur",
	"15": "Mizoram",
	"16": "Tripura",
	"17": "Meghalaya",
	"18": "Assam",
	"19": "West Bengal",
	"20": "Jharkhand",
	"21": "Odisha",
	"22": "Chhattisgarh",
	"23": "Madhya Pradesh",
	"24": "Gujarat",
	"25": "Daman and Diu",
	"26": "Dadra and Nagar Haveli and Daman and Diu",
	"27": "Maharashtra",
	"28": "Andhra Pradesh (Before Division)",
	"29": "Karnataka",
	"30": "Goa",
	"31": "Lakshadweep",
	"32": "Kerala",
	"33": "Tamil Nadu",
	"34": "Puducherry",
	"35": "Andaman and Nicobar Islands",
	"36": "Telangana",
	"37": "Andhra Pradesh",
	"38": "Ladakh",
	"97": "Other Territory",
	"99": "Centre Jurisdiction",
}

// placeOfSupplyLabel turns a bare gst_state_codes.code (what
// sales_documents.place_of_supply_state_code actually stores) into the
// "21 - Odisha" form a printed invoice should show — printing.InvoiceData
// only ever renders pre-formatted strings (see its own doc comment), so
// this resolution happens here, not in the printing package. An unknown
// or empty code falls back to the bare code (or "" ) rather than hiding
// the field.
func placeOfSupplyLabel(code string) string {
	if code == "" {
		return ""
	}
	if name, ok := gstStateNames[code]; ok {
		return code + " - " + name
	}
	return code
}
