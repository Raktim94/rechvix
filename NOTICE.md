# Rechvix notice

Rechvix
Copyright (C) 2026 Nodedr Infotech Private Limited and Raktim Ranjit

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <https://www.gnu.org/licenses/>.

Workflow adaptation references (same maintainer, AGPL-3.0):
- Zulivio, Raktim94/zulivio: organisation-scoped owner/reference validation and assignment workflow semantics, adapted to Rechvix Go/PostgreSQL in internal/modules/crm. No NestJS/Prisma dependency or schema was copied.
- Mavri, Raktim94/mavri: document-first workflow and recurring-billing reference, mapped in docs/reuse-matrix.md; the on-screen invoice layout (apps/web/src/components/InvoicePaper) adapts the structure of its Paper view without copying code. The Rechvix implementation retains its own accounting, identity and PostgreSQL model.
- IBM Plex font files on the product website retain their SIL Open Font License in public/fonts/IBM-Plex-OFL.txt (website repository).
