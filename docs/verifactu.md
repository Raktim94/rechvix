# VERI*FACTU (Spain): implementation status

**Status: in development. Rechvix is not VERI*FACTU compliant today and must not be described as such.**

Spain's invoicing-software regulation (Real Decreto 1007/2023, Orden HAC/1177/2024) requires, for software
that issues invoices, a tamper-evident chain of invoice records, a QR code on the invoice and, in the
VERI*FACTU mode, submission of each record to the Agencia Tributaria (AEAT). The producer of the software
must also publish a *declaración responsable*. AEAT's note (updated 13 October 2026) gives the adaptation
deadlines as 1 January 2027 for corporate-income-tax payers and 1 July 2027 for other taxpayers; recheck it
before relying on a date.

## What exists

`internal/modules/verifactu/domain` (pure functions, no I/O, not yet wired into invoicing):

| Piece | Spec | Verified by |
|---|---|---|
| Record fingerprint (huella), SHA-256, upper-case hex, for invoice (alta) and cancellation (anulación) records | AEAT "Detalle de las especificaciones técnicas para generación de la huella o hash" v0.1.2 | The three worked examples in section 6 of that document, reproduced exactly in `domain_test.go` |
| QR verification URL with URL-encoded parameters, test and production hosts | AEAT "Características del QR" v0.5.0, sections 4 to 6 | The encoding example in section 4 |
| Invoice-number rules (1 to 60 printable ASCII characters) | Same QR document, parameter table | Unit tests |

## What is not built yet

1. A persistent, append-only record chain per issuer and installation (database table, no UPDATE or DELETE
   grant, serialised chaining) written in the same transaction that finalizes or cancels an invoice.
2. The AEAT XML records (alta, anulación, system information, installation number) validated against AEAT's XSDs.
3. Submission to AEAT over mutual TLS with a qualified certificate, flow control, retries and the handling of
   accepted, accepted-with-errors and rejected responses. This needs a real certificate and AEAT's external
   test portal; it has not been run against AEAT.
4. The QR code and the legend ("QR tributario:", "Factura verificable en la sede electrónica de la AEAT" or
   "VERI*FACTU") on the A4, compact and 80 mm thermal layouts, 30 to 40 mm, error-correction level M.
5. The *declaración responsable*. It is a legal statement that only the producer, NODEDR INFOTECH PRIVATE
   LIMITED, can make after the above is built and tested. Rechvix cannot generate or sign it.

Rows 1 to 4 also apply to the sibling products (nodedr-pos, OrderRestro), which are separate codebases.
