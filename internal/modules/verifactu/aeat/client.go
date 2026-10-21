// Package aeat is the SOAP client for AEAT's VERI*FACTU invoice-record submission service
// (SistemaFacturacion.wsdl, operation RegFactuSistemaFacturacion). It authenticates with the taxpayer's
// qualified electronic certificate over mutual TLS.
package aeat

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/pkcs12"

	"rechvix/internal/modules/verifactu/domain"
)

// Endpoints published in AEAT's WSDL for systems that issue verifiable invoices (personal-certificate hosts).
const (
	EndpointTest       = "https://prewww1.aeat.es/wlpl/TIKE-CONT/ws/SistemaFacturacion/VerifactuSOAP"
	EndpointProduction = "https://www1.agenciatributaria.gob.es/wlpl/TIKE-CONT/ws/SistemaFacturacion/VerifactuSOAP"
)

// EndpointFor returns the service URL for an environment.
func EndpointFor(env domain.Environment) string {
	if env == domain.EnvProduction {
		return EndpointProduction
	}
	return EndpointTest
}

// Line is AEAT's verdict on one submitted record.
type Line struct {
	IssuerNIF string
	Number    string
	IssueDate string
	Operation string // Alta or Anulacion
	State     string // Correcto, AceptadoConErrores, Incorrecto
	ErrorCode string
	ErrorText string
}

// Response is the parsed RespuestaRegFactuSistemaFacturacion.
type Response struct {
	CSV         string
	State       string // Correcto, ParcialmenteCorrecto, Incorrecto
	WaitSeconds int
	Lines       []Line
	Raw         string
}

// Fault is a SOAP fault or a transport-level rejection (for example an invalid certificate).
type Fault struct {
	Code    string
	Message string
}

func (f *Fault) Error() string { return fmt.Sprintf("aeat: %s: %s", f.Code, f.Message) }

// Client posts record batches to AEAT.
type Client struct {
	HTTP *http.Client
	URL  string
}

// ParseCertificate decodes a PKCS#12 bundle into a TLS client certificate and reports its subject and expiry.
func ParseCertificate(p12 []byte, password string) (tls.Certificate, string, time.Time, error) {
	key, leaf, err := pkcs12.Decode(p12, password)
	if err != nil {
		return tls.Certificate{}, "", time.Time{}, fmt.Errorf("aeat: reading certificate (wrong password or not a .p12/.pfx file): %w", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf}
	return cert, leaf.Subject.String(), leaf.NotAfter, nil
}

// NewClient builds a mutual-TLS client for the environment's endpoint.
func NewClient(env domain.Environment, cert tls.Certificate) *Client {
	return &Client{
		URL: EndpointFor(env),
		HTTP: &http.Client{
			Timeout: 90 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{cert},
				MinVersion:   tls.VersionTLS12,
			}},
		},
	}
}

type soapEnvelope struct {
	Body struct {
		Inner []byte `xml:",innerxml"`
	} `xml:"Body"`
}

type soapFault struct {
	Code   string `xml:"faultcode"`
	String string `xml:"faultstring"`
}

type respXML struct {
	CSV               string `xml:"CSV"`
	TiempoEsperaEnvio int    `xml:"TiempoEsperaEnvio"`
	EstadoEnvio       string `xml:"EstadoEnvio"`
	Lines             []struct {
		ID struct {
			Issuer string `xml:"IDEmisorFactura"`
			Number string `xml:"NumSerieFactura"`
			Date   string `xml:"FechaExpedicionFactura"`
		} `xml:"IDFactura"`
		Operacion string `xml:"Operacion>TipoOperacion"`
		Estado    string `xml:"EstadoRegistro"`
		Code      string `xml:"CodigoErrorRegistro"`
		Text      string `xml:"DescripcionErrorRegistro"`
	} `xml:"RespuestaLinea"`
}

// Send posts one RegFactuSistemaFacturacion request body and parses the answer.
func (c *Client) Send(ctx context.Context, requestBody string) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(domain.SOAP(requestBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("SOAPAction", "")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aeat: contacting the tax agency: %w", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("aeat: reading response: %w", err)
	}
	return parse(res.StatusCode, raw)
}

func parse(status int, raw []byte) (*Response, error) {
	var env soapEnvelope
	if err := xml.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("aeat: unexpected response (HTTP %d): %s", status, snippet(raw))
	}
	inner := env.Body.Inner
	if bytes.Contains(inner, []byte("Fault")) {
		var f struct {
			Fault soapFault `xml:"Fault"`
		}
		wrap := append(append([]byte("<w>"), inner...), []byte("</w>")...)
		if err := xml.Unmarshal(wrap, &f); err == nil && (f.Fault.Code != "" || f.Fault.String != "") {
			return nil, &Fault{Code: f.Fault.Code, Message: f.Fault.String}
		}
	}
	var r respXML
	wrap := append(append([]byte("<w>"), inner...), []byte("</w>")...)
	var holder struct {
		R respXML `xml:"RespuestaRegFactuSistemaFacturacion"`
	}
	if err := xml.Unmarshal(wrap, &holder); err != nil {
		return nil, fmt.Errorf("aeat: unreadable response body: %w", err)
	}
	r = holder.R
	if r.EstadoEnvio == "" && status >= 400 {
		return nil, fmt.Errorf("aeat: HTTP %d: %s", status, snippet(raw))
	}
	if r.EstadoEnvio == "" {
		return nil, errors.New("aeat: response has no EstadoEnvio")
	}
	out := &Response{CSV: strings.TrimSpace(r.CSV), State: r.EstadoEnvio, WaitSeconds: r.TiempoEsperaEnvio, Raw: string(raw)}
	for _, l := range r.Lines {
		out.Lines = append(out.Lines, Line{
			IssuerNIF: l.ID.Issuer, Number: l.ID.Number, IssueDate: l.ID.Date, Operation: l.Operacion,
			State: l.Estado, ErrorCode: l.Code, ErrorText: l.Text,
		})
	}
	return out, nil
}

func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b)), " ")
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}
