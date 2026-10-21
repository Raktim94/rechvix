package domain

import (
	"bytes"
	"encoding/xml"
	"strings"

	"github.com/shopspring/decimal"
)

// Namespaces from AEAT's schemas (tikeV1.0).
const (
	NSInfo     = "https://www2.agenciatributaria.gob.es/static_files/common/internet/dep/aplicaciones/es/aeat/tike/cont/ws/SuministroInformacion.xsd"
	NSLR       = "https://www2.agenciatributaria.gob.es/static_files/common/internet/dep/aplicaciones/es/aeat/tike/cont/ws/SuministroLR.xsd"
	NSResponse = "https://www2.agenciatributaria.gob.es/static_files/common/internet/dep/aplicaciones/es/aeat/tike/cont/ws/RespuestaSuministro.xsd"
)

type xw struct{ b bytes.Buffer }

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(strings.TrimSpace(s)))
	return b.String()
}

func (w *xw) el(name, val string) {
	w.b.WriteString("<sf:" + name + ">" + esc(val) + "</sf:" + name + ">")
}
func (w *xw) open(name string)                     { w.b.WriteString("<sf:" + name + ">") }
func (w *xw) close(name string)                    { w.b.WriteString("</sf:" + name + ">") }
func (w *xw) money(name string, d decimal.Decimal) { w.el(name, Amount(d)) }

func (w *xw) party(p Party) {
	w.el("NombreRazon", p.Name)
	if p.NIF != "" {
		w.el("NIF", p.NIF)
		return
	}
	w.idOther(p.IDOther)
}

func (w *xw) idOther(o *IDOther) {
	w.open("IDOtro")
	if o.CountryCode != "" {
		w.el("CodigoPais", o.CountryCode)
	}
	w.el("IDType", o.IDType)
	w.el("ID", o.ID)
	w.close("IDOtro")
}

func (w *xw) system(s System) {
	w.open("SistemaInformatico")
	w.el("NombreRazon", s.ProducerName)
	if s.ProducerNIF != "" {
		w.el("NIF", s.ProducerNIF)
	} else if s.ProducerOther != nil {
		w.idOther(s.ProducerOther)
	}
	w.el("NombreSistemaInformatico", s.SystemName)
	w.el("IdSistemaInformatico", s.SystemID)
	w.el("Version", s.Version)
	w.el("NumeroInstalacion", s.InstallationNumber)
	w.el("TipoUsoPosibleSoloVerifactu", yn(s.OnlyVeriFactu))
	w.el("TipoUsoPosibleMultiOT", yn(s.MultiTaxpayer))
	w.el("IndicadorMultiplesOT", yn(s.MultipleTaxpayers))
	w.close("SistemaInformatico")
}

func yn(b bool) string {
	if b {
		return "S"
	}
	return "N"
}

func (w *xw) chain(prev *ChainRef) {
	w.open("Encadenamiento")
	if prev == nil {
		w.el("PrimerRegistro", "S")
	} else {
		w.open("RegistroAnterior")
		w.el("IDEmisorFactura", prev.IssuerNIF)
		w.el("NumSerieFactura", prev.Number)
		w.el("FechaExpedicionFactura", prev.IssueDate.Format(DateLayout))
		w.el("Huella", prev.Hash)
		w.close("RegistroAnterior")
	}
	w.close("Encadenamiento")
}

func dec(d *decimal.Decimal) string { return Amount(*d) }

// rate renders a percentage with up to two decimals as the schema's pattern \d{1,3}(\.\d{0,2})? allows.
func rate(d decimal.Decimal) string { return d.StringFixed(2) }

// XML renders the invoice record as an <sf:RegistroAlta> fragment (namespace prefix sf declared by
// the enclosing envelope) including its fingerprint.
func (a Alta) XML() string {
	var w xw
	w.open("RegistroAlta")
	w.el("IDVersion", "1.0")
	w.open("IDFactura")
	w.el("IDEmisorFactura", a.ID.IssuerNIF)
	w.el("NumSerieFactura", a.ID.Number)
	w.el("FechaExpedicionFactura", a.ID.IssueDate.Format(DateLayout))
	w.close("IDFactura")
	w.el("NombreRazonEmisor", a.IssuerName)
	w.el("TipoFactura", a.InvoiceType)
	if strings.HasPrefix(a.InvoiceType, "R") {
		rt := a.RectType
		if rt == "" {
			rt = "I"
		}
		w.el("TipoRectificativa", rt)
		if len(a.Rectified) > 0 {
			w.open("FacturasRectificadas")
			for _, r := range a.Rectified {
				w.open("IDFacturaRectificada")
				w.el("IDEmisorFactura", r.IssuerNIF)
				w.el("NumSerieFactura", r.Number)
				w.el("FechaExpedicionFactura", r.IssueDate.Format(DateLayout))
				w.close("IDFacturaRectificada")
			}
			w.close("FacturasRectificadas")
		}
	}
	w.el("DescripcionOperacion", a.Description)
	if a.Simplified && a.InvoiceType == "F1" {
		w.el("FacturaSimplificadaArt7273", "S")
	}
	if a.WithoutRecipient {
		w.el("FacturaSinIdentifDestinatarioArt61d", "S")
	}
	if len(a.Recipients) > 0 {
		w.open("Destinatarios")
		for _, r := range a.Recipients {
			w.open("IDDestinatario")
			w.party(r)
			w.close("IDDestinatario")
		}
		w.close("Destinatarios")
	}
	w.open("Desglose")
	for _, b := range a.Breakdown {
		w.open("DetalleDesglose")
		if b.Tax != "" {
			w.el("Impuesto", b.Tax)
		}
		if b.Regime != "" {
			w.el("ClaveRegimen", b.Regime)
		}
		if b.Exempt != "" {
			w.el("OperacionExenta", b.Exempt)
		} else {
			w.el("CalificacionOperacion", b.Qualification)
		}
		if b.Rate != nil {
			w.el("TipoImpositivo", rate(*b.Rate))
		}
		w.money("BaseImponibleOimporteNoSujeto", b.Base)
		if b.Quota != nil {
			w.el("CuotaRepercutida", dec(b.Quota))
		}
		if b.SurchargeRate != nil {
			w.el("TipoRecargoEquivalencia", rate(*b.SurchargeRate))
		}
		if b.SurchargeQuota != nil {
			w.el("CuotaRecargoEquivalencia", dec(b.SurchargeQuota))
		}
		w.close("DetalleDesglose")
	}
	w.close("Desglose")
	w.money("CuotaTotal", a.TaxTotal)
	w.money("ImporteTotal", a.InvoiceTotal)
	w.chain(a.Previous)
	w.system(a.System)
	w.el("FechaHoraHusoGenRegistro", a.GeneratedAt.Format(TimestampLayout))
	w.el("TipoHuella", "01")
	w.el("Huella", a.Hash())
	w.close("RegistroAlta")
	return w.b.String()
}

// XML renders the cancellation record as an <sf:RegistroAnulacion> fragment.
func (c Cancel) XML() string {
	var w xw
	w.open("RegistroAnulacion")
	w.el("IDVersion", "1.0")
	w.open("IDFactura")
	w.el("IDEmisorFacturaAnulada", c.ID.IssuerNIF)
	w.el("NumSerieFacturaAnulada", c.ID.Number)
	w.el("FechaExpedicionFacturaAnulada", c.ID.IssueDate.Format(DateLayout))
	w.close("IDFactura")
	w.chain(c.Previous)
	w.system(c.System)
	w.el("FechaHoraHusoGenRegistro", c.GeneratedAt.Format(TimestampLayout))
	w.el("TipoHuella", "01")
	w.el("Huella", c.Hash())
	w.close("RegistroAnulacion")
	return w.b.String()
}

// Envelope wraps already-built record fragments in AEAT's RegFactuSistemaFacturacion request body.
func Envelope(issuerName, issuerNIF string, records []string) string {
	var b bytes.Buffer
	b.WriteString(`<sfLR:RegFactuSistemaFacturacion xmlns:sfLR="` + NSLR + `" xmlns:sf="` + NSInfo + `">`)
	b.WriteString("<sfLR:Cabecera><sf:ObligadoEmision><sf:NombreRazon>" + esc(issuerName) + "</sf:NombreRazon><sf:NIF>" + esc(issuerNIF) + "</sf:NIF></sf:ObligadoEmision></sfLR:Cabecera>")
	for _, r := range records {
		b.WriteString("<sfLR:RegistroFactura>" + r + "</sfLR:RegistroFactura>")
	}
	b.WriteString("</sfLR:RegFactuSistemaFacturacion>")
	return b.String()
}

// SOAP wraps an envelope body in a SOAP 1.1 envelope.
func SOAP(body string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header/><soapenv:Body>` + body + `</soapenv:Body></soapenv:Envelope>`
}
