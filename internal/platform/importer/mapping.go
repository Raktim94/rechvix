package importer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Field describes one target column an import understands. Synonyms are
// the headers other products (Vyapar, Tally, Marg, hand-made sheets) use
// for the same thing, so a file exported elsewhere maps without renaming
// its columns first.
type Field struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Required bool     `json:"required"`
	Numeric  bool     `json:"numeric,omitempty"` // strip currency symbols, thousands separators and a trailing % before the module parses the value
	Synonyms []string `json:"synonyms,omitempty"`
}

// Inspection is what the wizard needs to draw its mapping step.
type Inspection struct {
	Headers   []string            `json:"headers"`
	Fields    []Field             `json:"fields"`
	Suggested map[string]string   `json:"suggested"` // target field -> source header
	Sample    []map[string]string `json:"sample"`    // first rows as they appear in the file
	Total     int                 `json:"total"`
}

const constantPrefix = "="

// Inspect lists the file's headers and proposes a mapping for fields.
func Inspect(rows []Row, fields []Field) Inspection {
	headers := headersOf(rows)
	ins := Inspection{Headers: headers, Fields: fields, Suggested: suggest(headers, fields), Total: len(rows)}
	for i := 0; i < len(rows) && i < 5; i++ {
		ins.Sample = append(ins.Sample, rows[i].Fields)
	}
	return ins
}

// ParseMapping decodes the mapping query parameter: a JSON object of
// target field -> source header. A value starting with "=" is a constant
// applied to every row (e.g. {"party_type":"=CUSTOMER"}). An empty string
// means "no explicit mapping", which ApplyMapping answers with suggestions.
func ParseMapping(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("importer: mapping must be a JSON object of field to column: %w", err)
	}
	return m, nil
}

// ApplyMapping rewrites each row so its keys are the target field names
// the module reads. With a nil mapping the suggested one is used, so an
// unmapped upload whose headers already match (or are well-known
// synonyms) still works. Columns not mapped to any field are dropped; a
// mapped column missing from the file is reported as an error.
func ApplyMapping(rows []Row, fields []Field, mapping map[string]string) ([]Row, error) {
	if mapping == nil {
		mapping = suggest(headersOf(rows), fields)
	}
	known := make(map[string]Field, len(fields))
	for _, f := range fields {
		known[f.Name] = f
	}
	headers := map[string]bool{}
	for _, h := range headersOf(rows) {
		headers[h] = true
	}
	for target, src := range mapping {
		if _, ok := known[target]; !ok {
			return nil, fmt.Errorf("importer: %q is not a field of this import", target)
		}
		if !strings.HasPrefix(src, constantPrefix) && src != "" && len(rows) > 0 && !headers[src] {
			return nil, fmt.Errorf("importer: the file has no column %q", src)
		}
	}
	out := make([]Row, len(rows))
	for i, r := range rows {
		nf := make(map[string]string, len(mapping))
		for target, src := range mapping {
			var v string
			switch {
			case src == "":
				continue
			case strings.HasPrefix(src, constantPrefix):
				v = strings.TrimPrefix(src, constantPrefix)
			default:
				v = r.Fields[src]
			}
			if known[target].Numeric {
				v = CleanNumber(v)
			}
			nf[target] = v
		}
		out[i] = Row{Number: r.Number, Fields: nf}
	}
	return out, nil
}

// CleanNumber removes what spreadsheets and Indian bills put around a
// number — rupee signs, "Rs", thousands separators (1,23,456.50 included),
// a trailing percent and surrounding spaces — leaving a plain decimal.
func CleanNumber(s string) string {
	s = strings.TrimSpace(s)
	for _, junk := range []string{"₹", "Rs.", "Rs", "INR", ",", "%", " ", " "} {
		s = strings.ReplaceAll(s, junk, "")
	}
	return s
}

func headersOf(rows []Row) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		for h := range r.Fields {
			if !seen[h] {
				seen[h] = true
				out = append(out, h)
			}
		}
	}
	sort.Strings(out)
	return out
}

func normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// suggest matches headers to fields by normalised name, label, then
// synonyms; each header is used for at most one field.
func suggest(headers []string, fields []Field) map[string]string {
	byNorm := make(map[string]string, len(headers))
	for _, h := range headers {
		byNorm[normalise(h)] = h
	}
	used := map[string]bool{}
	out := map[string]string{}
	for _, f := range fields {
		candidates := append([]string{f.Name, f.Label}, f.Synonyms...)
		for _, c := range candidates {
			if h, ok := byNorm[normalise(c)]; ok && !used[h] {
				out[f.Name] = h
				used[h] = true
				break
			}
		}
	}
	return out
}
