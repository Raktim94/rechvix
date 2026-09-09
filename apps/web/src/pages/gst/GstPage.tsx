import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ReportTable } from "../../components/ReportTable";
import ui from "../../components/ui.module.css";
import { api, apiUrl, ApiError } from "../../lib/api-client";
import type { Organisation } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";
import { DOCUMENT_TYPE_LABELS, EWB_ELIGIBLE_TYPES, type SalesDocument } from "../sales/types";

interface Vehicle {
  ID: string;
  RegistrationNumber: string;
  Nickname: string;
  VehicleType: string;
}
interface Transporter {
  ID: string;
  Name: string;
  TransporterID: string;
  GSTIN: string;
}
interface TaxRate {
  HSNSACCode: string;
  GSTRate: string;
  CessRate: string;
  ValidFrom: string;
}

function EWayBillModeSection() {
  const queryClient = useQueryClient();
  const org = useQuery({ queryKey: ["organisation"], queryFn: () => api.get<Organisation>("/organisation") });
  const portalUrl = useQuery({ queryKey: ["ewaybill-portal-url"], queryFn: () => api.get<{ url: string }>("/ewaybill/portal-url") });

  const setMode = useMutation({
    mutationFn: (mode: "FREE_PORTAL" | "AUTOMATIC_API") => api.put("/organisation/ewaybill-mode", { mode }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["organisation"] }),
  });

  const [thresholdInput, setThresholdInput] = useState<string | null>(null);
  const effectiveThreshold = thresholdInput ?? org.data?.EWayBillThresholdOverride ?? "";

  const setThreshold = useMutation({
    // null clears the override (back to the national/state default the
    // ewaybill.eligibility engine would otherwise pick) -- an empty
    // input field means "clear", not "zero".
    mutationFn: (value: string) => api.put("/organisation/ewaybill-threshold", { value: value.trim() === "" ? null : value.trim() }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["organisation"] });
      setThresholdInput(null);
    },
  });

  return (
    <div className={layout.panel}>
      <h2>e-Way Bill</h2>
      <p className={layout.subtitle} style={{ marginBottom: 12 }}>
        Choose how e-Way Bills are generated for this business.
      </p>
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <label style={{ display: "flex", gap: 8, alignItems: "start" }}>
          <input
            type="radio"
            name="ewb-mode"
            checked={org.data?.EWayBillMode !== "AUTOMATIC_API"}
            disabled={setMode.isPending}
            onChange={() => setMode.mutate("FREE_PORTAL")}
          />
          <span>
            <strong>Free government portal</strong> (recommended) — we prepare the file, you upload it yourself on the
            official government website. No extra cost, no account needed with us.
          </span>
        </label>
        <label style={{ display: "flex", gap: 8, alignItems: "start" }}>
          <input
            type="radio"
            name="ewb-mode"
            checked={org.data?.EWayBillMode === "AUTOMATIC_API"}
            disabled={setMode.isPending}
            onChange={() => setMode.mutate("AUTOMATIC_API")}
          />
          <span>
            <strong>Automatic</strong> — generated for you instantly through a paid government-approved connection.
            Requires a separate subscription.
          </span>
        </label>
      </div>
      {portalUrl.data ? (
        <p className={layout.subtitle} style={{ marginTop: 12 }}>
          Official portal:{" "}
          <a href={portalUrl.data.url} target="_blank" rel="noopener noreferrer">
            {portalUrl.data.url}
          </a>
        </p>
      ) : null}

      <hr style={{ margin: "16px 0", border: "none", borderTop: "1px solid var(--color-border)" }} />
      <h3 style={{ marginTop: 0 }}>e-Way Bill threshold</h3>
      <p className={layout.subtitle} style={{ marginBottom: 12 }}>
        An invoice needs an e-Way Bill once its consignment value crosses this amount.{" "}
        {org.data?.EWayBillThresholdOverride ? "Overriding the national default." : "Currently using the national default (₹50,000, unless a state-specific rule applies)."}
      </p>
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <input
          className={ui.input}
          style={{ maxWidth: 200 }}
          inputMode="decimal"
          placeholder="e.g. 50000"
          value={effectiveThreshold}
          onChange={(e) => setThresholdInput(e.target.value)}
        />
        <button type="button" className={ui.btnPrimary} disabled={setThreshold.isPending} onClick={() => setThreshold.mutate(effectiveThreshold)}>
          {setThreshold.isPending ? "Saving…" : "Save"}
        </button>
        {org.data?.EWayBillThresholdOverride ? (
          <button type="button" className={ui.btnSecondary} disabled={setThreshold.isPending} onClick={() => setThreshold.mutate("")}>
            Use national default instead
          </button>
        ) : null}
      </div>
      {setThreshold.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {setThreshold.error instanceof ApiError ? setThreshold.error.message : "Could not save this threshold."}
        </p>
      ) : null}
    </div>
  );
}

function VehiclesSection() {
  const queryClient = useQueryClient();
  const [reg, setReg] = useState("");
  const [nickname, setNickname] = useState("");
  const vehicles = useQuery({ queryKey: ["vehicles"], queryFn: () => api.getListField<Vehicle>("/logistics/vehicles", "vehicles") });
  const createVehicle = useMutation({
    mutationFn: () => api.post("/logistics/vehicles", { registration_number: reg, nickname, vehicle_type: "TRUCK", default_transport_mode: "ROAD" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["vehicles"] });
      setReg("");
      setNickname("");
    },
  });

  return (
    <div className={layout.panel}>
      <h2>Vehicles</h2>
      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="vehicle-reg">Registration number</label>
          <input id="vehicle-reg" className={ui.input} value={reg} onChange={(e) => setReg(e.target.value)} placeholder="e.g. MH12AB1234" />
        </div>
        <div className={ui.field}>
          <label htmlFor="vehicle-nickname">Nickname (optional)</label>
          <input id="vehicle-nickname" className={ui.input} value={nickname} onChange={(e) => setNickname(e.target.value)} />
        </div>
        <button type="button" className={ui.btnPrimary} disabled={!reg || createVehicle.isPending} onClick={() => createVehicle.mutate()}>
          Add vehicle
        </button>
      </div>
      {vehicles.data?.length ? (
        <div className={ui.tableScroll} style={{ marginTop: 12 }}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Registration</th>
                <th scope="col">Nickname</th>
              </tr>
            </thead>
            <tbody>
              {vehicles.data.map((v) => (
                <tr key={v.ID}>
                  <td>{v.RegistrationNumber}</td>
                  <td>{v.Nickname || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className={layout.emptyState}>No vehicles added yet.</p>
      )}
    </div>
  );
}

function TransportersSection() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [gstin, setGstin] = useState("");
  const transporters = useQuery({ queryKey: ["transporters"], queryFn: () => api.getListField<Transporter>("/logistics/transporters", "transporters") });
  const createTransporter = useMutation({
    mutationFn: () => api.post("/logistics/transporters", { name, transporter_id: "", gstin, phone: "", address: "", default_transport_mode: "ROAD" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["transporters"] });
      setName("");
      setGstin("");
    },
  });

  return (
    <div className={layout.panel}>
      <h2>Transporters</h2>
      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="transporter-name">Name</label>
          <input id="transporter-name" className={ui.input} value={name} onChange={(e) => setName(e.target.value)} />
        </div>
        <div className={ui.field}>
          <label htmlFor="transporter-gstin">GSTIN (optional)</label>
          <input id="transporter-gstin" className={ui.input} value={gstin} onChange={(e) => setGstin(e.target.value)} />
        </div>
        <button type="button" className={ui.btnPrimary} disabled={!name || createTransporter.isPending} onClick={() => createTransporter.mutate()}>
          Add transporter
        </button>
      </div>
      {transporters.data?.length ? (
        <div className={ui.tableScroll} style={{ marginTop: 12 }}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Name</th>
                <th scope="col">GSTIN</th>
              </tr>
            </thead>
            <tbody>
              {transporters.data.map((t) => (
                <tr key={t.ID}>
                  <td>{t.Name}</td>
                  <td>{t.GSTIN || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className={layout.emptyState}>No transporters added yet.</p>
      )}
    </div>
  );
}

function TaxRatesSection() {
  const queryClient = useQueryClient();
  const [hsn, setHsn] = useState("");
  const [lookupHsn, setLookupHsn] = useState("");
  const [gstRate, setGstRate] = useState("");
  const [validFrom, setValidFrom] = useState(() => new Date().toISOString().slice(0, 10));

  const rates = useQuery({
    queryKey: ["tax-rates", lookupHsn],
    queryFn: () => api.getListField<TaxRate>(`/gst/tax-rates/${encodeURIComponent(lookupHsn)}`, "tax_rates"),
    enabled: lookupHsn.length > 0,
  });

  const createRate = useMutation({
    mutationFn: () =>
      api.post("/gst/tax-rates", { hsn_sac_code: hsn, classification: "TAXABLE", gst_rate: gstRate, cess_rate: "0", valid_from: validFrom, valid_to: null }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tax-rates"] });
      setLookupHsn(hsn);
    },
  });

  return (
    <div className={layout.panel}>
      <h2>Tax rates</h2>
      <div className={ui.field} style={{ maxWidth: 300, marginBottom: 16 }}>
        <label htmlFor="hsn-lookup">Look up a rate by HSN/SAC code</label>
        <input id="hsn-lookup" className={ui.input} value={lookupHsn} onChange={(e) => setLookupHsn(e.target.value)} />
      </div>
      {lookupHsn ? (
        rates.data?.length ? (
          <div className={ui.tableScroll} style={{ marginBottom: 16 }}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">GST rate</th>
                  <th scope="col">Cess</th>
                  <th scope="col">Effective from</th>
                </tr>
              </thead>
              <tbody>
                {rates.data.map((r, i) => (
                  <tr key={i}>
                    <td className="num">{r.GSTRate}%</td>
                    <td className="num">{r.CessRate}%</td>
                    <td>{new Date(r.ValidFrom).toLocaleDateString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className={layout.emptyState}>No rate on file for this code yet.</p>
        )
      ) : null}

      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="new-rate-hsn">HSN/SAC code</label>
          <input id="new-rate-hsn" className={ui.input} value={hsn} onChange={(e) => setHsn(e.target.value)} />
        </div>
        <div className={ui.field}>
          <label htmlFor="new-rate-gst">GST rate (%)</label>
          <input id="new-rate-gst" className={ui.input} value={gstRate} onChange={(e) => setGstRate(e.target.value)} />
        </div>
        <div className={ui.field}>
          <label htmlFor="new-rate-from">Effective from</label>
          <input id="new-rate-from" type="date" className={ui.input} value={validFrom} onChange={(e) => setValidFrom(e.target.value)} />
        </div>
        <button type="button" className={ui.btnPrimary} disabled={!hsn || !gstRate || createRate.isPending} onClick={() => createRate.mutate()}>
          Save rate
        </button>
      </div>
      {createRate.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {createRate.error instanceof ApiError ? createRate.error.message : "Could not save this tax rate."}
        </p>
      ) : null}
    </div>
  );
}

/** Bulk-selection e-Way Bill prepare (docs/architecture.md §9b) — the
 * SplitBatch/PrepareFreePortalUploadBatch primitives existed server-side
 * since Stage 8c with no UI to select multiple invoices at once; each
 * invoice's own page (EwayBillCard) already covers the single-document
 * flow. Downloads one ZIP: numbered batch files ready to upload to the
 * government portal, plus a MANIFEST.txt listing anything that couldn't
 * be included and why (not eligible yet, missing distance, etc.) — a
 * skip is surfaced, never silently dropped from the download. */
function BulkEwayBillPanel() {
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [busy, setBusy] = useState(false);

  const documents = useQuery({
    queryKey: ["sales-documents"],
    queryFn: () => api.getListField<SalesDocument>("/sales/documents", "documents"),
  });
  const eligible = (documents.data ?? []).filter((d) => d.Status === "FINALIZED" && EWB_ELIGIBLE_TYPES.has(d.DocumentType));

  function toggle(id: string) {
    setSelected((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  async function prepareSelected() {
    setError(null);
    setDone(false);
    setBusy(true);
    try {
      const res = await fetch(apiUrl("/ewaybill/portal-batch"), {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sales_document_ids: Array.from(selected) }),
      });
      if (!res.ok) {
        const body: unknown = await res.json().catch(() => null);
        const message =
          body && typeof body === "object" && "error" in body && typeof (body as { error?: { message?: string } }).error?.message === "string"
            ? (body as { error: { message: string } }).error.message
            : `Could not prepare this batch (${res.status}).`;
        throw new ApiError(res.status, "BATCH_FAILED", message);
      }
      const disposition = res.headers.get("Content-Disposition") ?? "";
      const filename = /filename="([^"]+)"/.exec(disposition)?.[1] ?? "ewaybill-batch.zip";
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      setDone(true);
      setSelected(new Set());
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not prepare this batch.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className={layout.panel}>
      <h2>Bulk e-Way Bill prepare</h2>
      <p className={layout.subtitle} style={{ marginBottom: 12 }}>
        Select multiple invoices and download one ZIP of government-portal upload files, instead of preparing each
        one individually from its own invoice page.
      </p>
      {documents.isPending ? (
        <div className={layout.skeleton} style={{ height: 120 }} aria-hidden="true" />
      ) : eligible.length === 0 ? (
        <p className={layout.emptyState}>No finalized invoices are eligible for an e-Way Bill yet.</p>
      ) : (
        <>
          <div className={ui.tableScroll} style={{ maxHeight: 280, overflowY: "auto" }}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col" />
                  <th scope="col">Number</th>
                  <th scope="col">Type</th>
                  <th scope="col">Date</th>
                </tr>
              </thead>
              <tbody>
                {eligible.map((d) => (
                  <tr key={d.ID}>
                    <td>
                      <input
                        type="checkbox"
                        aria-label={`Select ${d.DocumentNumber}`}
                        checked={selected.has(d.ID)}
                        onChange={() => toggle(d.ID)}
                      />
                    </td>
                    <td>{d.DocumentNumber}</td>
                    <td>{DOCUMENT_TYPE_LABELS[d.DocumentType]}</td>
                    <td>{new Date(d.IssueDate).toLocaleDateString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            <button type="button" className={ui.btnPrimary} disabled={selected.size === 0 || busy} onClick={() => void prepareSelected()}>
              {busy ? "Preparing…" : `Prepare ${selected.size || ""} selected`.trim()}
            </button>
          </div>
        </>
      )}
      {error ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {error}
        </p>
      ) : null}
      {done ? <p style={{ color: "var(--color-positive)", marginTop: 8 }}>Batch downloaded — check MANIFEST.txt inside for anything skipped.</p> : null}
    </div>
  );
}

export function GstPage() {
  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>GST / Tax</h1>
          <p className={layout.subtitle}>Tax rates, e-Way Bill settings, and filing summaries.</p>
        </div>
      </div>

      <EWayBillModeSection />
      <BulkEwayBillPanel />
      <VehiclesSection />
      <TransportersSection />
      <TaxRatesSection />

      <div className={layout.panel}>
        <h2>HSN summary</h2>
        <ReportTable path="/reports/tax/hsn-summary?format=json" />
      </div>
      <div className={layout.panel}>
        <h2>Tax-rate summary</h2>
        <ReportTable path="/reports/tax/rate-summary?format=json" />
      </div>
      <div className={layout.panel}>
        <h2>GSTR-1 summary</h2>
        <p className={layout.subtitle} style={{ marginBottom: 12 }}>
          Prepared from your finalized sales for the current data — not a filing submission. Export and hand this to
          your CA, or use it to fill the government GSTR-1 form yourself.
        </p>
        <ReportTable path="/reports/tax/gstr1?format=json" />
      </div>
      <div className={layout.panel}>
        <h2>GSTR-3B summary</h2>
        <p className={layout.subtitle} style={{ marginBottom: 12 }}>
          Not a filing submission — and not every box on the government form: only outward taxable supplies (3.1) and
          input tax credit on purchases (4(A)(5)) are shown, since those are the only figures this app can compute
          from your finalized sales and purchases. Reverse charge, imports, and ITC reversals aren't tracked and are
          left off rather than shown as a guessed zero — bring those to your CA separately.
        </p>
        <ReportTable path="/reports/tax/gstr3b?format=json" />
      </div>
    </div>
  );
}
