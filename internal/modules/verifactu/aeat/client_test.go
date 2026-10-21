package aeat

import (
	"errors"
	"testing"
)

const okResponse = `<?xml version="1.0" encoding="UTF-8"?>
<env:Envelope xmlns:env="http://schemas.xmlsoap.org/soap/envelope/"><env:Header/><env:Body>
<tikR:RespuestaRegFactuSistemaFacturacion xmlns:tikR="https://www2.agenciatributaria.gob.es/static_files/common/internet/dep/aplicaciones/es/aeat/tike/cont/ws/RespuestaSuministro.xsd" xmlns:tik="https://www2.agenciatributaria.gob.es/static_files/common/internet/dep/aplicaciones/es/aeat/tike/cont/ws/SuministroInformacion.xsd">
<tikR:CSV>A-12345678901234</tikR:CSV>
<tikR:Cabecera><tik:ObligadoEmision><tik:NombreRazon>Empresa Demo SL</tik:NombreRazon><tik:NIF>89890001K</tik:NIF></tik:ObligadoEmision></tikR:Cabecera>
<tikR:TiempoEsperaEnvio>60</tikR:TiempoEsperaEnvio>
<tikR:EstadoEnvio>ParcialmenteCorrecto</tikR:EstadoEnvio>
<tikR:RespuestaLinea><tikR:IDFactura><tik:IDEmisorFactura>89890001K</tik:IDEmisorFactura><tik:NumSerieFactura>FAC/1</tik:NumSerieFactura><tik:FechaExpedicionFactura>21-10-2026</tik:FechaExpedicionFactura></tikR:IDFactura><tikR:Operacion><tik:TipoOperacion>Alta</tik:TipoOperacion></tikR:Operacion><tikR:EstadoRegistro>Correcto</tikR:EstadoRegistro></tikR:RespuestaLinea>
<tikR:RespuestaLinea><tikR:IDFactura><tik:IDEmisorFactura>89890001K</tik:IDEmisorFactura><tik:NumSerieFactura>FAC/2</tik:NumSerieFactura><tik:FechaExpedicionFactura>21-10-2026</tik:FechaExpedicionFactura></tikR:IDFactura><tikR:Operacion><tik:TipoOperacion>Alta</tik:TipoOperacion></tikR:Operacion><tikR:EstadoRegistro>Incorrecto</tikR:EstadoRegistro><tikR:CodigoErrorRegistro>1100</tikR:CodigoErrorRegistro><tikR:DescripcionErrorRegistro>Valor del campo incorrecto</tikR:DescripcionErrorRegistro></tikR:RespuestaLinea>
</tikR:RespuestaRegFactuSistemaFacturacion></env:Body></env:Envelope>`

func TestParseResponse(t *testing.T) {
	r, err := parse(200, []byte(okResponse))
	if err != nil {
		t.Fatal(err)
	}
	if r.CSV != "A-12345678901234" || r.State != "ParcialmenteCorrecto" || r.WaitSeconds != 60 || len(r.Lines) != 2 {
		t.Fatalf("unexpected: %+v", r)
	}
	if r.Lines[0].Number != "FAC/1" || r.Lines[0].State != "Correcto" || r.Lines[0].Operation != "Alta" {
		t.Fatalf("line 0: %+v", r.Lines[0])
	}
	if r.Lines[1].State != "Incorrecto" || r.Lines[1].ErrorCode != "1100" || r.Lines[1].ErrorText == "" {
		t.Fatalf("line 1: %+v", r.Lines[1])
	}
}

func TestParseFault(t *testing.T) {
	_, err := parse(500, []byte(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><soapenv:Fault><faultcode>soapenv:Client</faultcode><faultstring>Codigo[4102].El XML no cumple el esquema</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`))
	var f *Fault
	if !errors.As(err, &f) || f.Code == "" {
		t.Fatalf("want a Fault, got %v", err)
	}
}

func TestParseGarbage(t *testing.T) {
	if _, err := parse(502, []byte("<html>Bad gateway</html>")); err == nil {
		t.Fatal("garbage must be an error")
	}
}
