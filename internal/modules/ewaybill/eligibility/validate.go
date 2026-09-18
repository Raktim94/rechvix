package eligibility

import (
	"regexp"
	"strings"
)

// gstinPattern is the public, statutory GSTIN format — not a NIC- or
// this-codebase-specific invention, the same 15-character structure the
// GST Act's own registration numbering has used unchanged since GST's
// 2017 introduction: 2-digit state code, 10-character PAN, 1-digit
// entity/registration number, a literal "Z", 1 checksum character.
var gstinPattern = regexp.MustCompile(`^[0-9]{2}[A-Z]{5}[0-9]{4}[A-Z][1-9A-Z]Z[0-9A-Z]$`)

func isValidGSTIN(s string) bool {
	return gstinPattern.MatchString(s)
}

// pincodePattern: an Indian PIN code is exactly 6 digits; the first digit
// is never 0 — no postal circle is numbered 0.
var pincodePattern = regexp.MustCompile(`^[1-9][0-9]{5}$`)

func isValidPincode(s string) bool {
	return pincodePattern.MatchString(s)
}

// vehicleNumberPattern: the current Indian vehicle-registration format —
// 2-letter state code, 1-2 digit RTO code, 0-3 letter series, 4-digit
// number. Deliberately permissive on the series-letter count (0 is
// allowed) rather than one rigid shape, since real plates genuinely vary
// (very old-format plates, BH-series) — rejecting a genuinely valid
// plate here is worse than accepting a slightly-too-loose one; the
// government portal itself remains the final authority on whether a
// specific plate is actually valid.
var vehicleNumberPattern = regexp.MustCompile(`^[A-Z]{2}[0-9]{1,2}[A-Z]{0,3}[0-9]{4}$`)

func isValidVehicleNumber(s string) bool {
	normalized := strings.ToUpper(strings.ReplaceAll(s, " ", ""))
	return vehicleNumberPattern.MatchString(normalized)
}

// hsnPattern: HSN/SAC codes used on an e-Way Bill are 4, 6, or 8 numeric
// digits — services (SAC) share the same numeric-digit-count convention
// as goods (HSN) for this purpose.
var hsnPattern = regexp.MustCompile(`^[0-9]{4,8}$`)

func isValidHSN(s string) bool {
	return hsnPattern.MatchString(s)
}
