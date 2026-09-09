// Package importer provides the shared CSV/XLSX row-parsing and
// dry-run/validation-report scaffolding used by every module's bulk
// import feature (brief §53: products, customers/suppliers, opening
// stock/balances, price lists). A module owns its own per-entity
// validation, duplicate-detection, and commit logic; this package only
// owns getting a spreadsheet into a uniform []Row shape and accumulating
// a Report as each row is processed — so every importer in the codebase
// produces the same report shape and never silently drops a malformed
// row (brief §53: "never silently discard malformed rows").
package importer

import (
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/xuri/excelize/v2"
)

// Row is one data row, keyed by its header column name (trimmed,
// case-preserved as written in the file — callers compare case-
// insensitively if they want that).
type Row struct {
	Number int // 1-based, counting from the first DATA row (header excluded), so it matches what a spreadsheet user sees as their row's position within the data.
	Fields map[string]string
}

// ParseCSV reads r as CSV: the first row is the header, every following
// row becomes one Row. A row with fewer fields than the header is padded
// with empty strings rather than erroring at parse time — validation
// (which is entity-specific and belongs to the caller) is where a
// missing required field gets reported, not here.
func ParseCSV(r io.Reader) ([]Row, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows; pad/report at the Row level below
	records, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("importer: reading CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, nil
	}
	header := records[0]
	return toRows(header, records[1:]), nil
}

// ParseXLSX reads the first sheet of an XLSX workbook the same way
// ParseCSV reads a CSV: first row is the header, every following row is
// one Row.
func ParseXLSX(r io.Reader) ([]Row, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("importer: opening XLSX: %w", err)
	}
	defer f.Close()
	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("importer: workbook has no sheets")
	}
	rows, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, fmt.Errorf("importer: reading sheet %q: %w", sheets[0], err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	header := rows[0]
	return toRows(header, rows[1:]), nil
}

func toRows(header []string, dataRows [][]string) []Row {
	trimmedHeader := make([]string, len(header))
	for i, h := range header {
		trimmedHeader[i] = strings.TrimSpace(h)
	}
	out := make([]Row, 0, len(dataRows))
	for i, rec := range dataRows {
		fields := make(map[string]string, len(trimmedHeader))
		for col, name := range trimmedHeader {
			if name == "" {
				continue
			}
			if col < len(rec) {
				fields[name] = strings.TrimSpace(rec[col])
			} else {
				fields[name] = ""
			}
		}
		out = append(out, Row{Number: i + 1, Fields: fields})
	}
	return out
}

// RowOutcome is what happened to one row during an import pass.
type RowOutcome string

const (
	OutcomeCommitted RowOutcome = "COMMITTED" // row was valid and (dry_run=false) written
	OutcomeValid     RowOutcome = "VALID"     // row was valid but dry_run=true, so nothing was written
	OutcomeError     RowOutcome = "ERROR"     // row failed validation
	OutcomeDuplicate RowOutcome = "DUPLICATE" // row matched an existing record; not written, not an error
)

// RowResult records what happened to exactly one input row, so a caller
// can always answer "what happened to row N" — brief §53's requirement
// that malformed rows are reported, never silently dropped.
type RowResult struct {
	RowNumber int
	Outcome   RowOutcome
	Message   string // populated for ERROR (why) and DUPLICATE (what it matched)
}

// Report is the full result of one import pass (dry-run or committing).
type Report struct {
	DryRun     bool
	Total      int
	Committed  int
	Valid      int // valid but not committed (dry run)
	Errors     int
	Duplicates int
	Results    []RowResult
}

// Builder accumulates RowResults into a Report as a caller processes
// each parsed Row in order.
type Builder struct {
	dryRun  bool
	results []RowResult
}

func NewBuilder(dryRun bool) *Builder { return &Builder{dryRun: dryRun} }

func (b *Builder) Error(rowNumber int, format string, args ...any) {
	b.results = append(b.results, RowResult{RowNumber: rowNumber, Outcome: OutcomeError, Message: fmt.Sprintf(format, args...)})
}

func (b *Builder) Duplicate(rowNumber int, format string, args ...any) {
	b.results = append(b.results, RowResult{RowNumber: rowNumber, Outcome: OutcomeDuplicate, Message: fmt.Sprintf(format, args...)})
}

// Committed's optional message is for a NON-fatal note about the
// otherwise-successfully-committed row (e.g. catalogue's product import
// noting that a price or tax rate from the same row couldn't be set) —
// the row still counts as COMMITTED, this is not an error/duplicate.
func (b *Builder) Committed(rowNumber int, message ...string) {
	b.results = append(b.results, RowResult{RowNumber: rowNumber, Outcome: OutcomeCommitted, Message: strings.Join(message, "; ")})
}

func (b *Builder) Valid(rowNumber int) {
	b.results = append(b.results, RowResult{RowNumber: rowNumber, Outcome: OutcomeValid})
}

func (b *Builder) Report() Report {
	// Sorted by row number rather than left in append order — a caller
	// that has to defer some rows' Committed() call until after other
	// work finishes (catalogue's product import, setting price/tax
	// after its own transaction commits) would otherwise report rows
	// out of the order a spreadsheet user expects to see them in.
	sort.SliceStable(b.results, func(i, j int) bool { return b.results[i].RowNumber < b.results[j].RowNumber })
	rep := Report{DryRun: b.dryRun, Total: len(b.results), Results: b.results}
	for _, r := range b.results {
		switch r.Outcome {
		case OutcomeCommitted:
			rep.Committed++
		case OutcomeValid:
			rep.Valid++
		case OutcomeError:
			rep.Errors++
		case OutcomeDuplicate:
			rep.Duplicates++
		}
	}
	return rep
}
