# Rechvix — AI integration prompt

Paste into a coding agent with your task, e.g. "Build a read-only sales and receivables dashboard on Rechvix data, following `prompts/AI_INTEGRATION_PROMPT.md`."

Verified against `docs/api.md`, `api/openapi/openapi.yaml`, `apps/mcp/main.go` and `internal/modules/ai/tools.go`. Code wins over this file; report mismatches.

## What it is
Self-hosted billing, inventory, double-entry accounting and CRM (Go + Postgres): multiple companies, languages and invoice currencies, GST and other country tax packs (scope per country is documented, not guaranteed), e-invoicing (VERI*FACTU implemented, **not certified**).

## Use it when
A business running Rechvix needs a custom dashboard, a customer/product sync, invoice creation from another system, or an AI assistant answering questions about sales, stock, receivables and GST.

## Do not use it when
- No Rechvix instance exists: self-hosted, ask for the URL.
- You need statutory filing or certified e-invoicing: do not claim compliance. Read `docs/tax-regimes.md` and `docs/verifactu.md`.
- You need MCP **write** actions: the MCP server is read-only by design.

## Verified capability map
| Need | How |
|---|---|
| REST (every route under `/api/v1`) | session cookie (web app) **or** `Authorization: Bearer <key>`. OpenAPI: `api/openapi/openapi.yaml` (its own header says it is in progress; verify routes against `apps/server/main.go`). |
| API key scopes | `products:read`, `inventory:read`, `customers:read`, `customers:write`, `invoices:read`, `invoices:write`, `reports:read`. Effective permission = key scopes **∩** the owning user's RBAC, never more. Keys are hashed at rest, shown once, revocable, optional expiry and IP restriction. Created via the API-key endpoints (`/api/v1/api-keys`, identity module; screen in the web app). |
| Error envelope | `{"error":{"code","message","details?","request_id"}}` |
| Outbound webhooks | events actually wired: `invoice.finalized`, `einvoice.generated`, `einvoice.failed`. Signed HMAC-SHA256 over `timestamp + eventID + body` (verify before trusting); retried with backoff, dead-lettered after 8 attempts. Other catalogue events (`payment.*`, `stock.*`, `customer.created`, …) are **not** emitted yet. |
| MCP | local **stdio** process `apps/mcp`; 10 **read-only** tools: `search_products`, `get_product`, `get_inventory`, `get_customer`, `get_invoice`, `list_invoices`, `get_sales_summary`, `get_receivables`, `get_stock_summary`, `get_gst_summary`. Each call is audited. No tool takes an organisation id (tenant fixed by the key). |
| SDK | none |

## Credentials
Create a key with only what the integration needs (dashboard: `invoices:read reports:read inventory:read products:read`).
```env
# server only
RECHVIX_BASE_URL=https://rechvix.example.com
RECHVIX_API_KEY=...
RECHVIX_WEBHOOK_SECRET=...
# MCP process only
MCP_API_KEY=...
```
(`MCP_API_KEY` is the name the MCP binary reads; the others are this guide's convention.) Never `NEXT_PUBLIC_`, never committed.

## Steps (Next.js dashboard)
1. Read `api/openapi/openapi.yaml` and the route tree in `apps/server/main.go`; list the exact routes you will call and confirm each scope. Do not guess paths.
2. Server-only client with timeouts and typed error envelope; map `request_id` into your logs.
3. Fetch on the server; cache private financial data with `no-store` (or per-user short TTL). Never cache one tenant's data for another.
4. Invoice creation (`invoices:write`): use a dedicated narrow key, validate input server-side, and surface the API's validation `details`. Do not treat a created draft as finalized/e-invoiced.
5. Webhook receiver: raw body, verify timestamp + HMAC per `docs/adr/0005-webhook-delivery-fanout.md`, constant-time compare, reject stale timestamps, de-duplicate on event id, 2xx fast.

## MCP setup
```bash
# on the Rechvix host, with the same config/DB env as the server
MCP_API_KEY=… go run ./apps/mcp        # or the built binary
claude mcp add rechvix -e MCP_API_KEY=… -- /absolute/path/to/mcp-binary
```
The binary loads the server's normal config and connects to its database, so it runs **where the database is reachable**, not from a laptop over the internet. The `claude mcp add` line is standard Claude Code syntax; it was not run against a live Rechvix while writing this. Financial data (customers, invoices, receivables) is confidential: do not paste results into public tickets, logs or third-party tools.

## Verify
Non-production instance: key with only `invoices:read` cannot create an invoice (`403`); revoked key `401`; dashboard totals match the app; tampered webhook rejected; bundle contains no key. Report what you could not test.

References: `docs/api.md`, `api/openapi/openapi.yaml`, `docs/accountant-exports.md`, `docs/tax-regimes.md`, `docs/zulivio-integration.md`.
