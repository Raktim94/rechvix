package taxpack

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/xuri/excelize/v2"
	"path"
	"regexp"
	"sort"
	"strings"
)

var decimalCell = regexp.MustCompile(`^-?[0-9]+(?:\.[0-9]+)?$`)

func safeCSV(s string) string {
	if decimalCell.MatchString(s) {
		return s
	}
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed != "" && strings.ContainsAny(trimmed[:1], "=+-@") {
		return "'" + s
	}
	return s
}
func csvFile(t Table) ([]byte, error) {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	w.UseCRLF = true
	if err := w.Write(t.Headers); err != nil {
		return nil, err
	}
	for _, row := range t.Rows {
		safe := make([]string, len(row))
		for i, v := range row {
			safe[i] = safeCSV(v)
		}
		if err := w.Write(safe); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return b.Bytes(), w.Error()
}
func (p *Package) Workpaper() Table {
	t := Table{"Return workpaper", []string{"Box / field", "Description", "Calculated from records", "CA adjustment or verified input", "Final for CA review", "Status", "Mapping / review note"}, nil}
	for _, b := range p.Boxes {
		t.Rows = append(t.Rows, []string{b.Code, b.Label, b.Amount, "", "", b.Status, b.Note})
	}
	return t
}
func (p *Package) CheckTable() Table {
	t := Table{"Checks", []string{"Severity", "Code", "Document", "Action"}, nil}
	for _, c := range p.Checks {
		t.Rows = append(t.Rows, []string{c.Severity, c.Code, c.Document, c.Message})
	}
	return t
}
func (p *Package) readme() string {
	return fmt.Sprintf(`RECHVIX ACCOUNTANT HANDOFF — %s
Company: %s (%s), tax ID: %s
Period: %s through %s (inclusive issue/document dates)
Book currency: %s. Snapshot: %s
Status: %s. No return has been submitted.

1. Open accountant.xlsx. Review Checks and Documents, including drafts/cancellations.
2. Return workpaper has recorded calculations and editable CA adjustment/input cells.
   An empty statutory field means unknown, NEVER verified zero. Enter a verified zero
   only after confirming it. Final cells are formulas. Keep the source sheets unchanged.
3. Resolve every ERROR and review scheme, tax points, external transactions, eligibility,
   prior-period balances and registration details. Candidate input tax is not deductible
   tax until the CA verifies it. This export does not certify completeness or compliance.
4. raw-records.json preserves exact decimal source values and stable document IDs.
   CSVs are spreadsheet-safe; text starting with formula characters is apostrophe-escaped.
   The workbook uses literal string cells for text. Monetary values use exact decimal
   computations on the server; Excel's normal numeric precision applies in its workbook.
5. manifest.json lists SHA-256 for each payload file. Retain the snapshot as audit evidence.
   Regenerate after corrections. This package is not immutable accounting period closure.

COUNTRY WORKFLOW
India: gstr1/*.csv (when present) match headers from the official offline utility archive
retrieved 2026-10-21. Import into the current GST Returns Offline Tool, validate, complete
missing sections, generate JSON there, then the CA files through their normal GST portal
account. This app needs no GST API/GSP credentials for these exports. If ANY ERROR is
present, native import CSVs are withheld together: there is no silently partial import.
GSTR-3B worksheet and purchase tax candidates still require GSTR-2B/ITC eligibility review.
No GST portal/Windows utility validation has been performed by Rechvix.
UK: final workbook cells can be digitally linked/mapped by the CA's chosen MTD bridge.
Finish every box, particularly 2/8/9, deductions and external adjustments. Box 5 is absolute;
its payment/reclaim direction is a separate cell. Rechvix is not MTD submission software.
Spain: Modelo 303 2026 general-regime workpaper. IVA and recargo rectifications are separate
from ordinary rate buckets. Allocate purchase candidates to current/capital and deductible
categories, then complete carry-forwards and settlement. This is not an AEAT fixed-width
import file and does not satisfy invoice-system/VERI*FACTU requirements by itself.
US: state-by-state source schedules, not a universal tax return. Local tax, nexus, use tax,
marketplace sales and state form/jurisdiction mappings must be completed by the CA.

Sources (mapping version %s; reviewed 2026-10-21):
%s
`, p.Version, p.Company.Name, p.Company.Country, p.Company.TaxID, p.From, p.To, p.Company.Currency, p.SnapshotAt.Format("2006-01-02T15:04:05Z07:00"), p.Status, p.Version, strings.Join(p.Sources, "\n"))
}

func (p *Package) Workbook() ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	_ = f.DeleteSheet("Sheet1")
	readmeRows := [][]string{}
	for _, line := range strings.Split(p.readme(), "\n") {
		readmeRows = append(readmeRows, []string{line})
	}
	tables := []Table{{"Read me", []string{"Accountant handoff"}, readmeRows}, p.Workpaper(), p.CheckTable()}
	for _, table := range p.Tables {
		if strings.HasPrefix(table.Name, "gstr1/") && p.Status == "BLOCKED_ITEMS_PRESENT" {
			continue
		}
		tables = append(tables, table)
	}
	titleStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true, Color: "FFFFFF"}, Fill: excelize.Fill{Type: "pattern", Color: []string{"086B64"}, Pattern: 1}, Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
	editable, _ := f.NewStyle(&excelize.Style{Fill: excelize.Fill{Type: "pattern", Color: []string{"E8F2FF"}, Pattern: 1}, NumFmt: 2})
	moneyStyle, _ := f.NewStyle(&excelize.Style{NumFmt: 2})
	for i, t := range tables {
		name := strings.NewReplacer("/", " ", "[", "(", "]", ")", ":", " ", "*", " ", "?", " ", "\\", " ").Replace(t.Name)
		if len(name) > 31 {
			name = name[:31]
		}
		if _, err := f.NewSheet(name); err != nil {
			return nil, err
		}
		for col, h := range t.Headers {
			cell, _ := excelize.CoordinatesToCellName(col+1, 1)
			if err := f.SetCellStr(name, cell, h); err != nil {
				return nil, err
			}
		}
		end, _ := excelize.CoordinatesToCellName(len(t.Headers), 1)
		_ = f.SetCellStyle(name, "A1", end, titleStyle)
		_ = f.SetColWidth(name, "A", strings.TrimRight(end, "0123456789"), 20)
		_ = f.SetPanes(name, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
		for row, values := range t.Rows {
			for col, v := range values {
				cell, _ := excelize.CoordinatesToCellName(col+1, row+2)
				if err := f.SetCellStr(name, cell, v); err != nil {
					return nil, err
				}
			}
		}
		if i == 0 {
			_ = f.SetColWidth(name, "A", "A", 110)
			style, _ := f.NewStyle(&excelize.Style{Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"}})
			last, _ := excelize.CoordinatesToCellName(1, len(t.Rows)+1)
			_ = f.SetCellStyle(name, "A2", last, style)
			for row := range t.Rows {
				_ = f.SetRowHeight(name, row+2, 30)
			}
		}
		if i == 1 {
			for row, b := range p.Boxes {
				n := row + 2
				c := fmt.Sprintf("C%d", n)
				d := fmt.Sprintf("D%d", n)
				e := fmt.Sprintf("E%d", n)
				if b.Amount != "" {
					if err := f.SetCellDefault(name, c, b.Amount); err != nil {
						return nil, err
					}
				}
				_ = f.SetCellStyle(name, c, c, moneyStyle)
				_ = f.SetCellStyle(name, d, d, editable)
				_ = f.SetCellStyle(name, e, e, moneyStyle)
				formula := fmt.Sprintf(`IF(AND(C%d="",D%d=""),"",SUM(C%d,D%d))`, n, n, n, n)
				if err := f.SetCellFormula(name, e, formula); err != nil {
					return nil, err
				}
			}
			_ = f.SetColWidth(name, "B", "B", 44)
			_ = f.SetColWidth(name, "G", "G", 85)
			if p.Company.Country == "GB" {
				p.ukFormulas(f)
			}
			if p.Company.Country == "ES" {
				p.spainFormulas(f)
			}
		}
	}
	f.SetActiveSheet(1)
	_ = f.SetCalcProps(&excelize.CalcPropsOptions{FullCalcOnLoad: boolPtr(true)})
	b, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
func boolPtr(b bool) *bool { return &b }
func (p *Package) ukFormulas(f *excelize.File) {
	const s = "Return workpaper"
	cells := map[string]string{}
	for i, b := range p.Boxes {
		cells[b.Code] = fmt.Sprintf("E%d", i+2)
	}
	_ = f.SetCellFormula(s, cells["3"], fmt.Sprintf(`IF(OR(%s="",%s=""),"",SUM(%s,%s))`, cells["1"], cells["2"], cells["1"], cells["2"]))
	_ = f.SetCellFormula(s, cells["5"], fmt.Sprintf(`IF(OR(%s="",%s=""),"",ABS(%s-%s))`, cells["3"], cells["4"], cells["3"], cells["4"]))
	_ = f.SetCellStr(s, "I1", "Box 5 direction")
	_ = f.SetCellFormula(s, "I2", fmt.Sprintf(`IF(OR(%s="",%s=""),"CA INPUT REQUIRED",IF(%s>=%s,"PAYABLE","RECLAIM"))`, cells["3"], cells["4"], cells["3"], cells["4"]))
}

func (p *Package) ZIP() ([]byte, error) {
	files := map[string][]byte{}
	workbook, err := p.Workbook()
	if err != nil {
		return nil, err
	}
	files["accountant.xlsx"] = workbook
	files["README.txt"] = []byte(p.readme())
	raw, err := json.MarshalIndent(p.Data, "", "  ")
	if err != nil {
		return nil, err
	}
	files["raw-records.json"] = raw
	meta, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	files["review.json"] = meta
	tables := append([]Table{p.CheckTable(), p.Workpaper()}, p.Tables...)
	for _, t := range tables {
		if strings.HasPrefix(t.Name, "gstr1/") && p.Status == "BLOCKED_ITEMS_PRESENT" {
			continue
		}
		name := t.Name + ".csv"
		if strings.HasSuffix(t.Name, ".csv") {
			name = t.Name
		}
		if path.Clean(name) != name || strings.Contains(name, "..") {
			return nil, fmt.Errorf("unsafe export filename")
		}
		body, err := csvFile(t)
		if err != nil {
			return nil, err
		}
		files[name] = body
	}
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	type digest struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Bytes  int    `json:"bytes"`
	}
	manifest := struct {
		Version, CompanyID, From, To, Status string
		Files                                []digest
	}{Version: p.Version, CompanyID: p.Company.ID, From: p.From, To: p.To, Status: p.Status}
	for _, n := range names {
		sum := sha256.Sum256(files[n])
		manifest.Files = append(manifest.Files, digest{n, hex.EncodeToString(sum[:]), len(files[n])})
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	files["manifest.json"] = body
	names = append(names, "manifest.json")
	sort.Strings(names)
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, n := range names {
		w, err := z.Create(n)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(files[n]); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (p *Package) spainFormulas(f *excelize.File) {
	const sheet = "Return workpaper"
	cells := map[string]string{}
	for i, b := range p.Boxes {
		cells[b.Code] = fmt.Sprintf("E%d", i+2)
	}
	sumWhenComplete := func(target string, codes []string) {
		refs := []string{}
		for _, code := range codes {
			refs = append(refs, cells[code])
		}
		// COUNT tests numeric completeness: blank/unresolved fields never become zero.
		formula := fmt.Sprintf(`IF(COUNT(%s)=%d,SUM(%s),"")`, strings.Join(refs, ","), len(refs), strings.Join(refs, ","))
		_ = f.SetCellFormula(sheet, cells[target], formula)
	}
	sumWhenComplete("27", []string{"152", "03", "06", "09", "170", "21", "24", "158", "11", "13", "15", "26"})
	sumWhenComplete("45", []string{"29", "31", "33", "35", "37", "39", "41", "42", "43", "44"})
	_ = f.SetCellFormula(sheet, cells["46"], fmt.Sprintf(`IF(COUNT(%s,%s)=2,%s-%s,"")`, cells["27"], cells["45"], cells["27"], cells["45"]))
}
