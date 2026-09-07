import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ReportTable } from "../../components/ReportTable";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import type { Organisation } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";

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
        <button type="button" className={ui.btnSecondary} disabled={!reg || createVehicle.isPending} onClick={() => createVehicle.mutate()}>
          Add vehicle
        </button>
      </div>
      {vehicles.data?.length ? (
        <ul style={{ marginTop: 12 }}>
          {vehicles.data.map((v) => (
            <li key={v.ID}>
              {v.RegistrationNumber} {v.Nickname ? `(${v.Nickname})` : ""}
            </li>
          ))}
        </ul>
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
        <button type="button" className={ui.btnSecondary} disabled={!name || createTransporter.isPending} onClick={() => createTransporter.mutate()}>
          Add transporter
        </button>
      </div>
      {transporters.data?.length ? (
        <ul style={{ marginTop: 12 }}>
          {transporters.data.map((t) => (
            <li key={t.ID}>{t.Name}</li>
          ))}
        </ul>
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
          <ul style={{ marginBottom: 16 }}>
            {rates.data.map((r, i) => (
              <li key={i}>
                GST {r.GSTRate}% (from {new Date(r.ValidFrom).toLocaleDateString()})
              </li>
            ))}
          </ul>
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
        <button type="button" className={ui.btnSecondary} disabled={!hsn || !gstRate || createRate.isPending} onClick={() => createRate.mutate()}>
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
    </div>
  );
}
