# AGENTS.md — Rechvix

## Integrating with Rechvix
Read [`prompts/AI_INTEGRATION_PROMPT.md`](prompts/AI_INTEGRATION_PROMPT.md). Essentials: REST under `/api/v1` with scoped Bearer API keys (effective permission = scopes ∩ the user's RBAC), OpenAPI in `api/openapi/openapi.yaml` (in progress), signed webhooks for `invoice.finalized`/`einvoice.*` only, and a local stdio MCP server with 10 read-only tools. No SDK. Do not claim tax or e-invoice certification. Self-hosted: ask for URL and key, never invent or commit them.

## Working on this repo
See `CONTRIBUTING.md`, `docs/architecture.md`. No Co-Authored-By trailers.
