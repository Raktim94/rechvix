package printing

import (
	"reflect"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/gobolditalic"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/text/encoding/charmap"

	"github.com/go-pdf/fpdf"
)

// The PDF library's built-in fonts (Helvetica, Times) take Windows-1252 bytes,
// not UTF-8, so a name like "Muñoz" or the euro sign would print as garbage if
// passed through unchanged. RenderPDF therefore picks one of two paths per
// document:
//
//   - every character fits Windows-1252 (Western European languages and €):
//     keep the built-in fonts, so every theme looks exactly as before, and
//     convert the text;
//   - anything else (Polish, Czech, Hungarian, Greek, Cyrillic, ...): embed the
//     Go fonts (Unicode TrueType, BSD-licensed, covering Latin, Greek and Cyrillic)
//     and use them for the whole document in every theme.
//
// Scripts those fonts do not contain (for example Devanagari or CJK) still cannot
// be printed; that is a known limit, see docs/localisation.md.

const unicodeFamily = "gosans"

// registerUnicodeFonts adds the embedded Unicode family to pdf.
func registerUnicodeFonts(pdf *fpdf.Fpdf) {
	pdf.AddUTF8FontFromBytes(unicodeFamily, "", goregular.TTF)
	pdf.AddUTF8FontFromBytes(unicodeFamily, "B", gobold.TTF)
	pdf.AddUTF8FontFromBytes(unicodeFamily, "I", goitalic.TTF)
	pdf.AddUTF8FontFromBytes(unicodeFamily, "BI", gobolditalic.TTF)
}

// fitsWindows1252 reports whether every rune of s exists in Windows-1252.
func fitsWindows1252(s string) bool {
	for _, r := range s {
		if r < utf8.RuneSelf {
			continue
		}
		if _, ok := charmap.Windows1252.EncodeRune(r); !ok {
			return false
		}
	}
	return true
}

// toWindows1252 converts s for the built-in fonts; callers must have checked
// fitsWindows1252 first.
func toWindows1252(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < utf8.RuneSelf {
			out = append(out, byte(r))
			continue
		}
		b, _ := charmap.Windows1252.EncodeRune(r)
		out = append(out, b)
	}
	return string(out)
}

var timeType = reflect.TypeOf(time.Time{})

// mapStrings returns a copy of v with f applied to every string. Only exported
// struct fields, slices and pointers are walked; byte slices (images, QR data),
// time.Time and other opaque values are returned unchanged.
func mapStrings(v reflect.Value, f func(string) string) reflect.Value {
	switch v.Kind() {
	case reflect.String:
		return reflect.ValueOf(f(v.String())).Convert(v.Type())
	case reflect.Struct:
		if v.Type() == timeType || v.Type().PkgPath() != reflect.TypeOf(InvoiceData{}).PkgPath() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if out.Field(i).CanSet() {
				out.Field(i).Set(mapStrings(v.Field(i), f))
			}
		}
		return out
	case reflect.Slice:
		if v.IsNil() || v.Type().Elem().Kind() == reflect.Uint8 {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(mapStrings(v.Index(i), f))
		}
		return out
	case reflect.Ptr:
		if v.IsNil() {
			return v
		}
		inner := mapStrings(v.Elem(), f)
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(inner)
		return out
	}
	return v
}

// everyString calls fn for each string in the document data.
func everyString(data InvoiceData, fn func(string)) {
	mapStrings(reflect.ValueOf(data), func(s string) string { fn(s); return s })
}

// needsUnicode reports whether any text in data cannot be printed with the
// built-in fonts.
func needsUnicode(data InvoiceData) bool {
	need := false
	everyString(data, func(s string) {
		if !fitsWindows1252(s) {
			need = true
		}
	})
	return need
}

// forBuiltInFonts returns data with every string converted to Windows-1252.
func forBuiltInFonts(data InvoiceData) InvoiceData {
	return mapStrings(reflect.ValueOf(data), toWindows1252).Interface().(InvoiceData)
}

// labelsForBuiltInFonts converts a label set to Windows-1252, like forBuiltInFonts
// does for the document data, so "Désignation" and "Catégorie" print correctly. Every
// shipped label set fits Windows-1252 (a test enforces it).
func labelsForBuiltInFonts(l Labels) Labels {
	v := reflect.ValueOf(l)
	out := reflect.New(v.Type()).Elem()
	for i := 0; i < v.NumField(); i++ {
		switch f := v.Field(i); f.Kind() {
		case reflect.String:
			out.Field(i).SetString(toWindows1252(f.String()))
		case reflect.Map:
			m := reflect.MakeMapWithSize(f.Type(), f.Len())
			for _, k := range f.MapKeys() {
				m.SetMapIndex(k, reflect.ValueOf(toWindows1252(f.MapIndex(k).String())))
			}
			out.Field(i).Set(m)
		}
	}
	return out.Interface().(Labels)
}
