import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ImportPanel } from "../../components/ImportPanel";
import ui from "../../components/ui.module.css";
import { api } from "../../lib/api-client";
import { useOrgContext } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";

interface HistoricalInvoice {
  id: string;
  kind: "SALE" | "PURCHASE";
  party_name: string;
  document_number: string;
  document_date: string;
  total: string;
  paid: string;
  due: string;
  currency: string;
}

const today = () => new Date().toISOString().slice(0, 10);

/** One guided place to bring a business over from Vyapar, Tally, Marg or a spreadsheet. The order matters: contacts and
 * items first, then opening balances (which put the books right), then past invoices (reference only). */
export function ImportPage() {
  const qc = useQueryClient();
  const org = useOrgContext();
  const [asOf, setAsOf] = useState(today());
  const refresh = (...keys: string[]) => () => keys.forEach((k) => void qc.invalidateQueries({ queryKey: [k] }));

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Import your data</h1>
          <p className={layout.subtitle}>Move from Vyapar, Tally, Marg or a spreadsheet in four steps. Every step shows a preview first and never writes until you confirm.</p>
        </div>
      </div>

      <section className={layout.panel} aria-labelledby="how-heading">
        <h2 id="how-heading">How to export from your old app</h2>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(240px, 1fr))", gap: 16 }}>
          <div>
            <h3 style={{ fontSize: "1rem" }}>Vyapar</h3>
            <p className={ui.muted}>Export your party list, item list, party balances and sales and purchase reports to Excel from its reports section, then upload each file below. The column names are matched for you and you can change any match.</p>
          </div>
          <div>
            <h3 style={{ fontSize: "1rem" }}>Tally Prime / ERP 9</h3>
            <p className={ui.muted}>Export your masters as XML for contacts and stock items and choose the .xml file in steps 1 and 2. For balances and invoices, export a report to Excel or CSV.</p>
          </div>
          <div>
            <h3 style={{ fontSize: "1rem" }}>Marg, Busy or a sheet</h3>
            <p className={ui.muted}>Any CSV or Excel file works. Match each of your columns to a Rechvix field; fix one value for every row where a column is missing.</p>
          </div>
        </div>
      </section>

      <h2 style={{ margin: "8px 0 0" }}>1. Customers and suppliers</h2>
      <ImportPanel
        title="Customers and suppliers"
        path="/contacts/parties/import"
        mapColumns
        defaultOpen
        columns={["legal_name", "party_type (CUSTOMER/SUPPLIER/BOTH)", "currency_code", "phone", "email"]}
        sampleRows={[{ legal_name: "Asha General Stores", party_type: "CUSTOMER", currency_code: "INR", phone: "9876543210", email: "asha@example.com" }]}
        onImported={refresh("parties")}
      />

      <h2 style={{ margin: "8px 0 0" }}>2. Items and opening stock</h2>
      <ImportPanel
        title="Items"
        path="/catalogue/products/import"
        mapColumns
        defaultOpen
        extraQuery={org.warehouse?.ID ? { warehouse_id: org.warehouse.ID } : undefined}
        columns={["name", "base_uom_code", "hsn_sac_code", "price", "gst_rate", "opening_qty (goes into your current warehouse)", "opening_cost"]}
        sampleRows={[{ name: "Basmati Rice 5kg", base_uom_code: "PCS", hsn_sac_code: "1006", price: "650", gst_rate: "5", opening_qty: "40", opening_cost: "520" }]}
        onImported={refresh("products", "catalogue")}
      />

      <h2 style={{ margin: "8px 0 0" }}>3. Opening balances</h2>
      <section className={layout.panel}>
        <p className={ui.muted} style={{ marginTop: 0 }}>
          What each customer still owes you and what you still owe each supplier on the day you start. These become real entries in your accounts (against Opening Balance Equity), so ledgers, ageing and the dashboard are right from day one. A contact can only be given one opening balance, so re-running a file is safe. Import your contacts first.
        </p>
        <div className={ui.field} style={{ maxWidth: 260 }}>
          <label htmlFor="as-of">Balances as of</label>
          <input id="as-of" type="date" className={ui.input} value={asOf} max={today()} onChange={(e) => setAsOf(e.target.value)} />
        </div>
      </section>
      <ImportPanel
        title="Opening balances"
        path="/migration/opening-balances/import"
        mapColumns
        defaultOpen
        extraQuery={{ as_of: asOf }}
        columns={["party_name (must already exist)", "balance (amount, above zero)", "balance_type (RECEIVABLE they owe you / PAYABLE you owe them; optional for customers and suppliers)"]}
        sampleRows={[{ party_name: "Asha General Stores", balance: "12500.50", balance_type: "RECEIVABLE" }]}
        onImported={refresh("receivables-detailed", "dashboard", "parties")}
      />

      <h2 style={{ margin: "8px 0 0" }}>4. Past invoices (history)</h2>
      <section className={layout.panel}>
        <p className={ui.muted} style={{ margin: 0 }}>
          Keep your old invoice numbers, dates and amounts searchable. These are reference records only: they never change stock, tax returns or your accounts, so nothing is counted twice. Your balances come from step 3.
        </p>
      </section>
      <ImportPanel
        title="Past sales invoices"
        path="/migration/invoices/import"
        mapColumns
        defaultOpen
        extraQuery={{ kind: "SALE" }}
        columns={["party_name (must already exist)", "document_number", "document_date (day/month/year)", "total", "paid (optional)", "notes (optional)"]}
        sampleRows={[{ party_name: "Asha General Stores", document_number: "INV-0041", document_date: "25/12/2025", total: "1180", paid: "180", notes: "" }]}
        onImported={refresh("historical-invoices")}
      />
      <ImportPanel
        title="Past purchase bills"
        path="/migration/invoices/import"
        mapColumns
        defaultOpen
        extraQuery={{ kind: "PURCHASE" }}
        columns={["party_name (must already exist)", "document_number", "document_date (day/month/year)", "total", "paid (optional)", "notes (optional)"]}
        sampleRows={[{ party_name: "Bulk Traders", document_number: "BILL-212", document_date: "02/01/2026", total: "5000", paid: "0", notes: "" }]}
        onImported={refresh("historical-invoices")}
      />

      <HistoryRegister />
    </div>
  );
}

function HistoryRegister() {
  const [kind, setKind] = useState<"" | "SALE" | "PURCHASE">("");
  const [q, setQ] = useState("");
  const list = useQuery({
    queryKey: ["historical-invoices", kind, q],
    queryFn: () => api.getListField<HistoricalInvoice>(`/migration/invoices?kind=${kind}&q=${encodeURIComponent(q)}`, "invoices"),
  });
  const rows = list.data ?? [];

  return (
    <section className={layout.panel} aria-labelledby="register-heading">
      <h2 id="register-heading">Imported invoice history</h2>
      <div className={ui.toolbar} style={{ marginBottom: 12, flexWrap: "wrap" }}>
        <input className={ui.input} placeholder="Search by number or name…" aria-label="Search imported invoices" value={q} onChange={(e) => setQ(e.target.value)} />
        <select className={ui.select} aria-label="Kind" value={kind} onChange={(e) => setKind(e.target.value as "" | "SALE" | "PURCHASE")}>
          <option value="">Sales and purchases</option>
          <option value="SALE">Sales only</option>
          <option value="PURCHASE">Purchases only</option>
        </select>
      </div>
      {list.isPending ? (
        <div className={layout.skeleton} style={{ height: 96 }} aria-hidden="true" />
      ) : list.isError ? (
        <p className={layout.errorState} role="alert">
          Couldn't load the history.
        </p>
      ) : rows.length === 0 ? (
        <p className={layout.emptyState}>Nothing imported yet.</p>
      ) : (
        <div className={ui.tableScroll} tabIndex={0}>
          <table className={ui.table}>
            <caption className="srOnly">Invoices imported from your previous app</caption>
            <thead>
              <tr>
                <th scope="col">Date</th>
                <th scope="col">Number</th>
                <th scope="col">Type</th>
                <th scope="col">Name</th>
                <th scope="col" className="num">Total</th>
                <th scope="col" className="num">Due</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.id}>
                  <td>{r.document_date}</td>
                  <td>{r.document_number}</td>
                  <td>{r.kind === "SALE" ? "Sale" : "Purchase"}</td>
                  <td>{r.party_name}</td>
                  <td className="num">{r.total}</td>
                  <td className="num">{r.due}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
