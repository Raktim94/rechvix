# Rechvix mobile (iOS + Android)

One Expo / React Native codebase (TypeScript) for both phones. **Standalone first**: the app works fully on its own with no server at all
(items, customers, stock, GST invoices, PDF sharing, backup). Connect a Rechvix server any time and everything uploads and merges.

## Standalone mode (no server)

First launch: welcome -> business details -> billing. No account, no server, no internet needed.
The phone owns items (name, price, GST %, stock, SKU, barcode), customers, stock levels, GST maths (CGST+SGST, or IGST for another state),
gap-free invoice numbers (`INV-0001`), unpaid tracking, PDF invoices (share to WhatsApp etc.) and a JSON backup export.

## Connecting a server later (More -> Connect a server)

Upload order: customers -> items -> sales. Everything is merged, never duplicated:
- a customer is matched to the server's by exact name, then by phone number (the server allows one party per phone);
- an item is matched by SKU; a matched item keeps the server's price and stock (nothing is overwritten);
- a new item gets its product, variant, barcode, price, HSN GST rate and opening stock created. Opening stock is the quantity
  from *before* the phone's unsent sales (those sales deduct it when they sync, so it is never counted twice);
- each upload step is recorded so a dropped connection can never post opening stock or a sale twice;
- one record the server refuses (e.g. a bad value) is reported and retried; it does not stop the others.
The phone's own invoice numbers are replaced by the server's when each sale syncs. "Work without the server" disconnects without deleting anything.

## Billing, receivables and invoices (2.1)

- **Prices.** "Price includes GST" is the default: type the final price (₹105 at 5% stays ₹105; two units are ₹210). Switch it off to type
  prices before GST. All money is exact decimal arithmetic (BigInt minor units); each GST component is rounded half-up per line, the same
  rule the server uses, and `test/fixtures/server-parity.json` plus `npm run test:live` check the phone against a real server to the paisa.
  Each saved sale stores its pricing mode, quantities, tax split and totals, so invoices and PDFs are drawn from what was issued and never
  recomputed. Sales queued before 2.1 have no pricing mode and keep their original (before-GST) meaning.
- **Quantities** are typed directly (fractions where the item allows: kg, litre, ... or per-item setting); +/- are shortcuts.
- **Payments** are separate rows (`payments` table), not a flag on the sale. A payment can be split across invoices (oldest first or a chosen one);
  extra money is kept as the customer's advance. Each receipt carries `client_ref = mobile:pay:<id>`, so retries book it once on the server.
  A payment is never erased: reversing it marks it reversed with a reason (locally, or through `POST /accounting/receipts/{id}/reverse`).
- **Editing.** An invoice the server has never seen can be edited (old version kept in `sale_revisions`). A synced invoice is corrected through
  Correct invoice: its payments are reversed, it is cancelled on the server (`POST /sales/documents/{id}/cancel`), and a new invoice is issued.
- **Invoice designs:** Classic tax invoice (A4), Clean modern (A4), Compact receipt (80 mm). Presentation only; the figures are identical.
- **Server changes** (migration `0065_receipt_reversal`): idempotent receipts via `client_ref`, receipt reversal, reversed receipts excluded from paid totals.
- **Limits.** Dashboard and To collect figures come from this phone's own invoices; when linked, the server's organisation-wide receivable is
  shown separately. Reminders open WhatsApp or the share sheet; delivery cannot be known.

## Quotations, credit notes and returns (2.2)

- **Quotations** (More -> Quotations; on New sale choose *Save as quotation*). A priced offer kept on this phone (`quotes` table, numbers `QT-0001`).
  It never touches stock, invoices or what anyone owes, and is not sent to the server. Share it as a PDF in any invoice design (titled
  QUOTATION, with its validity, no payment block). *Make an invoice from this* loads it into the bill; saving the invoice marks the quotation converted.
- **Credit notes and returns** (invoice -> *Credit / return items*; needs a connection and an invoice the server already holds). Pick the
  quantity per line. *Goods came back* issues a server **sales return** (stock goes up again and the balance falls); *Money only* issues a
  **credit note** (balance falls, stock unchanged). The server numbers it; the phone records it only after the server has issued it. It is
  resumable: the intent is saved first, the server draft id is remembered, and finalizing twice is harmless, so a dropped connection never makes a
  second credit note. Limits: a part-return of a *discounted* line is refused (the server copies a line's discount whole), and a credit above
  what was sold is refused. Credit notes lower the invoice's net bill everywhere (invoice, To collect, PDF); money already received beyond
  the net bill is shown as *owed back to the customer*, never converted into a payment or a refund (that is your decision).
- **Low stock.** Each item can have its own low-stock level (item editor, even for server items; blank = 5; kept on the phone by SKU, not uploaded). Home counts items at or below their level and says how many are out; Items has All / Low stock / Out of stock filters with a "low" mark on each row; while billing, a line warns when the sale would leave the item at or below its level, or use up all of it, besides the existing "more than in stock" warning.
- **Bill of supply, delivery note.** The document title follows the business: a GST-registered business (a GSTIN typed in, or a linked server company that is registered) issues a TAX INVOICE; an Indian business with no GSTIN gets BILL OF SUPPLY; outside India it is a plain INVOICE. *Share delivery note* on an invoice prints the items and quantities only (no prices, tax, totals or payment status) with a signature line; it changes no stock or balance.
- **Backup and restore** (More). *Export a backup* writes every record (invoices, payments, customers, items, quotations, credit notes, edit history, low-stock levels, counters; never the server link). *Restore from a backup* shows what the file holds, saves a copy of the current data, then replaces everything, all or nothing (a failure part-way leaves the phone untouched). A file from a newer app is refused; an old-format file (made before 2.3.0, which never held payments) restores its records and leaves the phone's payments alone.
- **HSN guard.** In India an item with a GST rate but no HSN/SAC code is held back from upload (it stays editable) with a message naming it, because the server taxes by HSN and, once uploaded, the item could not be edited here.
- **Keyboard.** The total / Save bar on New sale lifts above the on-screen keyboard (measured, so it works with or without Android resizing the window).


## How sync works

- **Connect once** to your self-hosted server (address is checked against `/health/ready`), then sign in. The session
  cookie is kept in the OS keychain/keystore (`expo-secure-store`) and sent as a header, so it also works over a plain-http shop LAN.
- **Local SQLite** (`expo-sqlite`) holds the catalogue (with price and stock), customers, recent invoices and the **outbox** of sales made on the phone.
- **Selling offline**: a sale is written to the outbox first (it can never be lost), then synced in the background.
  Totals shown offline are *before tax*; the server computes tax and the grand total.
- **Sync = replay through the normal sales API** (`src/sync/saga.ts`): create draft -> add lines -> finalise -> receipt.
  Finalising posts the ledger, moves stock and assigns the next invoice number, so only the server does it, exactly once.
  Every step is resumable: the phone can be killed or lose signal at any instant and a retry never duplicates lines,
  invoices or receipts (`test/saga.test.ts` crashes the connection after every step to prove it).
  Each draft is tagged `mobile:<id>` in the customer reference.
- **When it syncs**: on reconnect, when the app opens, after sign-in, every 45 s while sales are waiting, and on "Sync now".
- Sales the server refuses (e.g. insufficient stock) are kept as **Needs attention** with the server's message; retry or discard.
- If the session expires, sales stay queued; sign in again and they sync.

Not in this version: creating customers/items on the phone (do that on the web), returns, purchases, printing. Offline conflicts
only exist for sales, and those never conflict: every invoice is new and numbered by the server.

## Develop

```bash
npm install
npm test            # 130+ tests: sync saga under crashes, engine, API client (real SQLite via node:sqlite)
npm run typecheck
RECHVIX_URL=http://localhost:8099 RECHVIX_EMAIL=... RECHVIX_PASSWORD=... npm run test:live   # same code vs a real server
npx expo start      # needs a dev build (expo-sqlite, camera); Expo Go is not enough
```

## Build

`android/` and `ios/` are generated (`expo prebuild`) and not committed.

```bash
# Android APK (JDK 17+, Android SDK)
npx expo prebuild --platform android && cd android && ./gradlew assembleRelease -PreactNativeArchitectures=arm64-v8a,x86_64
# sign with a keystore you keep (updates must use the same key):
apksigner sign --ks ~/.rechvix-android.keystore app/build/outputs/apk/release/app-release.apk

# iOS (macOS + Xcode only): see .github/workflows/mobile.yml (workflow_dispatch) for the exact steps
npx expo prebuild --platform ios && cd ios && pod install
```

The Android `applicationId` is `in.nodedr.rechvix`, the same as the earlier web-shell APK, so it installs over it when signed with the same key.
iOS bundle id: `in.nodedr.rechvix`. A real iPhone build needs your Apple Developer team and signing; the workflow builds for the simulator.
