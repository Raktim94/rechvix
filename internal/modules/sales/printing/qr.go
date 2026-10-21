package printing

import (
	"bytes"
	"fmt"
	"html/template"
	"image"
	"image/png"
	"strings"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
)

// VerifactuQR is the tax-agency QR block AEAT requires on an invoice issued by a VERI*FACTU system. It is only
// populated for invoices that have a record in the company's chain, never fabricated (see the same rule for IRN).
type VerifactuQR struct {
	URL     string
	Heading string // "QR tributario:"
	Legend  string // "Factura verificable en la sede electrónica de la AEAT"
	Short   string // "VERI*FACTU"
}

// QRSizeMM is the printed side of the code; AEAT requires between 30 and 40 mm.
const QRSizeMM = 35.0

const grayBlack = 0

func encodeQR(content string) (barcode.Barcode, error) {
	// ISO/IEC 18004:2015 with error-correction level M, as AEAT's Orden HAC/1177/2024 art. 21 requires.
	return qr.Encode(content, qr.M, qr.Auto)
}

// qrPNG renders the code as a black-on-white PNG with a four-module quiet zone.
func qrPNG(content string) ([]byte, error) {
	code, err := encodeQR(content)
	if err != nil {
		return nil, fmt.Errorf("printing: building QR code: %w", err)
	}
	n := code.Bounds().Dx()
	const px, quiet = 8, 4
	size := (n + 2*quiet) * px
	img := image.NewGray(image.Rect(0, 0, size, size))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if r, _, _, _ := code.At(x, y).RGBA(); r == 0 {
				for dy := 0; dy < px; dy++ {
					for dx := 0; dx < px; dx++ {
						img.Pix[((y+quiet)*px+dy)*img.Stride+(x+quiet)*px+dx] = grayBlack
					}
				}
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// qrSVG renders the code as an inline SVG path (no external resources, so the print page's strict CSP is untouched).
func qrSVG(content string) (template.HTML, error) {
	code, err := encodeQR(content)
	if err != nil {
		return "", err
	}
	n := code.Bounds().Dx()
	const quiet = 4
	var d strings.Builder
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if r, _, _, _ := code.At(x, y).RGBA(); r == 0 {
				fmt.Fprintf(&d, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	dim := n + 2*quiet
	return template.HTML(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%dmm" height="%dmm" shape-rendering="crispEdges" role="img" aria-label="QR tributario"><rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`,
		dim, dim, int(QRSizeMM), int(QRSizeMM), dim, dim, d.String())), nil
}
