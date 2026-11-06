import * as Print from "expo-print";
import * as Sharing from "expo-sharing";
import { File, Paths } from "expo-file-system";
import type { Db } from "../db/types";
import { invoiceHtml, type InvoiceData } from "./invoice";

/** Renders the invoice to a PDF and opens the system share sheet (WhatsApp, email, print, save...). */
export async function shareInvoice(d: InvoiceData): Promise<void> {
  const { uri } = await Print.printToFileAsync({ html: invoiceHtml(d) });
  // Name the file after the invoice, so the customer receives "Invoice-INV-0001.pdf" rather than a random id.
  const named = new File(Paths.cache, `Invoice-${d.number.replace(/[^A-Za-z0-9._-]+/g, "-")}.pdf`);
  if (named.exists) named.delete();
  new File(uri).copy(named);
  if (await Sharing.isAvailableAsync()) await Sharing.shareAsync(named.uri, { mimeType: "application/pdf", UTI: ".pdf", dialogTitle: `Invoice ${d.number}` });
}

/** Everything on the phone as one JSON file. For a standalone phone this is the safety net; linking a server is the better one. */
export async function exportBackup(db: Db): Promise<void> {
  const dump = {
    app: "rechvix-mobile", exportedAt: new Date().toISOString(),
    kv: await db.all("SELECT key, value FROM kv WHERE key != 'server'"),
    products: await db.all("SELECT * FROM products"), parties: await db.all("SELECT * FROM parties"),
    sales: await db.all("SELECT * FROM sales"), id_map: await db.all("SELECT * FROM id_map"),
  };
  const f = new File(Paths.cache, `rechvix-backup-${new Date().toISOString().slice(0, 10)}.json`);
  if (f.exists) f.delete();
  f.create();
  f.write(JSON.stringify(dump, null, 2));
  if (await Sharing.isAvailableAsync()) await Sharing.shareAsync(f.uri, { mimeType: "application/json", dialogTitle: "Rechvix backup" });
}
