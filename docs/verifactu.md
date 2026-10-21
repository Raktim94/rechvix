# VERI*FACTU (Spain): implementation status

**Status: implemented, not certified. Rechvix must not be described as VERI*FACTU compliant until the declaracion responsable has been made by the producer and the flow has passed AEAT's external test portal.**

Spain's invoicing-software regulation (Real Decreto 1007/2023, Orden HAC/1177/2024) requires, for software that issues invoices, a tamper-evident chain of invoice records, a QR code on the invoice and, in VERI*FACTU mode, submission of each record to the Agencia Tributaria (AEAT). AEAT's note (updated 13 October 2026) gives the adaptation deadlines as 1 January 2027 for corporate-income-tax payers and 1 July 2027 for other taxpayers; recheck it before relying on a date.

## What is built

| Piece | Where | Verified by |
|---|---|---|
| Record fingerprint (huella), SHA-256, upper-case hex, invoice and cancellation records | `internal/modules/verifactu/domain` | The three worked examples in AEAT's hash specification v0.1.2, section 6 |
| QR verification URL, URL-encoded, test and production hosts; QR image at error-correction level M | `domain/qr.go`, `sales/printing/qr.go` | The encoding example in AEAT's QR document v0.5.0 |
| Append-only, hash-chained record table, per company, serialised by a row lock | migration 0049, `verifactu/pg` | Integration tests, including concurrent finalization and refused UPDATE, DELETE and broken chains |
| Records written in the same transaction as finalizing or cancelling an invoice (F1, F2, R1 by differences, anulacion) | `sales/app` | Integration tests |
| AEAT XML (alta, anulacion, system information, installation number) | `domain/xml.go` | Validated against AEAT's published XSD (tikeV1.0) in `xml_test.go` |
| QR and legend on A4, compact and 58/80 mm thermal layouts | `sales/printing` | Integration test renders every layout |
| Submission over mutual TLS with the company's certificate, flow control, per-record results | `verifactu/aeat`, worker | Parser and flow tests against AEAT-shaped responses and a stand-in transport |
| Settings, certificate upload (encrypted), register, chain verification, XML export | Settings, Tax | Integration tests |

## What is not done

1. A run against AEAT's real external test portal. It needs the company's own certificate. Until it has been done the submission code has only been exercised against AEAT-shaped responses.
2. The *declaracion responsable*, a legal statement that only the producer (NODEDR INFOTECH PRIVATE LIMITED) can make. Rechvix cannot generate or sign it, and `VERIFACTU_PRODUCER_ID` must be supplied by the producer.
3. Not covered: invoices in a currency other than euros (refused while VERI*FACTU is on), debit notes (refused), substitution rectifications (R1 by differences only), simplified-invoice qualification rules beyond issuing F2 when the customer has no tax ID, third-party issuance, and the event register used by non-VERI*FACTU systems.
4. OrderRestro and NODEDR POS are separate products and are not covered by this module.

## Operating it

Settings, Tax, VERI*FACTU (Spanish companies only): enter the company NIF in company settings, upload the electronic certificate, choose the test environment, switch it on, issue an invoice and send. Use "Verify the chain" and "Download records (XML)" for evidence. Start in the test environment.
