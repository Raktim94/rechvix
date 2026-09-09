# AI bill import — the prompt

Rechvix can turn a distributor/supplier bill into a draft purchase (new
products created automatically for anything not already in your
catalogue) two ways:

1. **In-app OCR** — click "Scan distributor bill" on the Catalogue or
   Purchases page and photograph the bill directly. Runs entirely in
   your browser, free, no upload anywhere.
2. **An AI chat site you already use** (ChatGPT, Gemini, Claude, ...) —
   for a messier or handwritten bill where the built-in OCR struggles.
   This costs nothing extra if you already have free access to one of
   those tools; Rechvix itself never calls any AI API or sends your
   bill anywhere.

This file is the prompt for option 2. The in-app "Import from AI" button
(next to "Scan distributor bill") shows this same text with a copy
button, so you don't need to come back to this file to use it.

## How to use it

1. Open ChatGPT, Gemini, or a similar AI chat site in your browser.
2. Upload a photo or PDF of the distributor bill to that chat.
3. Copy the prompt below and paste it into the same chat.
4. The AI replies with a Markdown document. Save that reply as a
   `.md` (or `.txt`) file — select all, copy, paste into a plain text
   file, save it.
5. In Rechvix, click **Import from AI** (next to "Scan distributor
   bill") and upload that file. You'll land on the same review screen
   as the built-in scanner: match or create each product, pick or
   create the distributor, then create the purchase.

## The prompt

````text
You are extracting data from a distributor/supplier bill or invoice
(image or PDF) for import into inventory/billing software. Read the
attached document carefully and output ONLY a Markdown document in
EXACTLY this format, with no extra commentary before or after it:

Distributor Name: <the seller/distributor's business name>
GSTIN: <their GSTIN if shown on the bill, else leave blank>
Phone: <their phone number if shown, else leave blank>
Invoice Number: <the bill/invoice number, if shown>
Invoice Date: <the bill date, in YYYY-MM-DD format if you can tell>

## Items

| Description | HSN/SAC | Quantity | Unit | Rate | Amount |
|---|---|---|---|---|---|
| <item name as printed> | <HSN/SAC code if shown, else blank> | <quantity, digits only> | <unit, e.g. PCS/KG/BOX/LTR> | <price per unit, digits only> | <line total, digits only> |

(one row per line item on the bill — include every item, even if a
field is unclear)

Rules:
- Use plain numbers only for Quantity, Rate, and Amount — no currency
  symbols, no thousands separators, no text.
- If Rate is missing but Quantity and Amount are both present, compute
  Rate = Amount / Quantity yourself.
- Do not invent or guess any data that isn't actually on the bill —
  leave a field blank rather than making something up.
- Output nothing except the Markdown described above: no explanation,
  no "here is the extracted data" preamble, no code fence around the
  whole reply.
````

## Why this format

Rechvix's importer looks for the `Distributor Name:` / `GSTIN:` /
`Phone:` lines and a Markdown table under an `## Items` heading, matching
table columns by name rather than position — so an AI reordering
columns or adding an extra one won't misalign your data. Anything the
parser can't confidently find is just left blank for you to fill in on
the review screen; nothing is silently guessed. If an AI wraps its
whole reply in a ` ```markdown ` code fence despite the instructions,
that's fine too — the importer strips it automatically.
