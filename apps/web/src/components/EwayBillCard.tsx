import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError } from "../lib/api-client";
import ui from "./ui.module.css";
import styles from "./EwayBillCard.module.css";

type Requirement = "NOT_REQUIRED" | "READY" | "NEEDS_INFORMATION" | "REQUIRED";

interface MissingInfo {
  Field: string;
  Reason: string;
}

interface EligibilityResult {
  Requirement: Requirement;
  Missing: MissingInfo[] | null;
  RecordID: string;
}

interface EwayBillRecord {
  Status: string;
  EWBNumber: string | null;
  ValidFrom: string | null;
  ValidUntil: string | null;
  PreparedFileName: string | null;
  TransporterName: string;
  VehicleNumber: string;
}

interface StatusResponse {
  eligibility: EligibilityResult;
  record: EwayBillRecord | null;
}

interface Vehicle {
  ID: string;
  RegistrationNumber: string;
  Nickname: string;
}

interface Transporter {
  ID: string;
  Name: string;
}

function capitalize(s: string) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

/** The invoice-screen e-Way Bill card (docs/architecture.md §9b, brief's
 * source spec for this stage). Deliberately plain-language throughout —
 * no "JSON"/"API"/technical terms ever shown here, this is a small
 * business owner's screen, not a developer's. Folds the "portal
 * assistant" experience (prepare → open the real government portal in a
 * new tab, unproxied → come back and record the result) into this one
 * card rather than a separate screen, since the full multi-screen task
 * center / bulk-prepare queue described in the source spec is real,
 * larger scope left for a follow-up pass (see docs/TODO.md) — this card
 * is the fully-functional core of that experience, not a placeholder.
 */
export function EwayBillCard({ documentId }: { documentId: string }) {
  const queryClient = useQueryClient();
  const [showManualForm, setShowManualForm] = useState(false);
  const [ewbNumber, setEwbNumber] = useState("");
  const [validFrom, setValidFrom] = useState("");
  const [validUntil, setValidUntil] = useState("");
  const [vehicleNumber, setVehicleNumber] = useState("");
  const [transporterName, setTransporterName] = useState("");
  const [distanceKm, setDistanceKm] = useState("");

  const status = useQuery({
    queryKey: ["ewaybill-status", documentId],
    queryFn: () => api.get<StatusResponse>(`/sales/documents/${documentId}/ewaybill`),
  });

  const portalUrl = useQuery({
    queryKey: ["ewaybill-portal-url"],
    queryFn: () => api.get<{ url: string }>("/ewaybill/portal-url"),
    staleTime: Infinity,
  });

  // Saved vehicles/transporters (Settings → GST → Vehicles/Transporters)
  // surfaced here as autocomplete suggestions, so filling in missing
  // e-Way Bill info reuses what's already on file instead of the user
  // retyping a registration number from memory every time.
  const vehicles = useQuery({
    queryKey: ["logistics-vehicles"],
    queryFn: () => api.getListField<Vehicle>("/logistics/vehicles", "vehicles"),
    staleTime: 60_000,
  });
  const transporters = useQuery({
    queryKey: ["logistics-transporters"],
    queryFn: () => api.getListField<Transporter>("/logistics/transporters", "transporters"),
    staleTime: 60_000,
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["ewaybill-status", documentId] });

  const prepare = useMutation({
    mutationFn: async () => {
      const res = await fetch(`/api/v1/sales/documents/${documentId}/ewaybill/prepare`, {
        method: "POST",
        credentials: "include",
      });
      if (!res.ok) throw new Error("Could not prepare the e-Way Bill file.");
      const disposition = res.headers.get("Content-Disposition") ?? "";
      const match = /filename="(.+)"/.exec(disposition);
      const fileName = match?.[1] ?? `ewaybill-${documentId}.json`;
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = fileName;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    },
    onSuccess: () => {
      invalidate();
      // Opened as a direct, unproxied new tab to the real government
      // site — docs/architecture.md §9b's hard constraint: never wrap,
      // proxy, or auto-authenticate this. The user logs in themselves.
      if (portalUrl.data?.url) {
        const opened = window.open(portalUrl.data.url, "_blank", "noopener,noreferrer");
        if (!opened) {
          // Popup-blocked fallback — leave the portalUrl visible as a
          // plain link in the UI below instead of failing silently.
        }
      }
    },
  });

  const updateTransportInfo = useMutation({
    mutationFn: () =>
      api.post(`/sales/documents/${documentId}/ewaybill/transport-info`, {
        vehicle_number: vehicleNumber || null,
        transporter_name: transporterName || null,
        distance_km: distanceKm || null,
      }),
    onSuccess: () => {
      invalidate();
      setVehicleNumber("");
      setTransporterName("");
      setDistanceKm("");
    },
  });

  const manualResult = useMutation({
    mutationFn: () =>
      api.post(`/sales/documents/${documentId}/ewaybill/manual-result`, {
        ewb_number: ewbNumber,
        valid_from: new Date(validFrom).toISOString(),
        valid_until: new Date(validUntil).toISOString(),
      }),
    onSuccess: () => {
      invalidate();
      setShowManualForm(false);
      setEwbNumber("");
      setValidFrom("");
      setValidUntil("");
    },
  });

  if (status.isPending) {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Way Bill</h2>
        </div>
        <p className={styles.explainer}>Checking whether this sale needs an e-Way Bill…</p>
      </div>
    );
  }

  if (status.isError) {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Way Bill</h2>
        </div>
        <p className={styles.errorText} role="alert">
          Couldn't check the e-Way Bill status for this sale.
        </p>
      </div>
    );
  }

  const { eligibility, record } = status.data;
  const requirement = eligibility.Requirement;

  if (requirement === "NOT_REQUIRED" && !record?.EWBNumber) {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Way Bill</h2>
          <span className={ui.badge} data-tone="neutral">
            Not needed
          </span>
        </div>
        <p className={styles.explainer}>This sale doesn't need an e-Way Bill.</p>
      </div>
    );
  }

  if (record?.EWBNumber) {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Way Bill</h2>
          <span className={ui.badge} data-tone="positive">
            Complete
          </span>
        </div>
        <p className={styles.explainer}>
          e-Way Bill <strong>{record.EWBNumber}</strong> is on file for this sale.
        </p>
        {record.ValidUntil ? (
          <p className={styles.detail}>Valid until {new Date(record.ValidUntil).toLocaleString()}</p>
        ) : null}
      </div>
    );
  }

  if (requirement === "NEEDS_INFORMATION" || requirement === "REQUIRED") {
    return (
      <div className={styles.card}>
        <div className={styles.header}>
          <h2>e-Way Bill</h2>
          <span className={ui.badge} data-tone="warning">
            Needs information
          </span>
        </div>
        <p className={styles.explainer}>A few more details are needed before this can be prepared.</p>
        {eligibility.Missing?.length ? (
          <ul className={styles.missingList}>
            {eligibility.Missing.map((m, i) => (
              <li key={i}>{capitalize(m.Reason)}</li>
            ))}
          </ul>
        ) : null}
        <div className={styles.form}>
          <div className={ui.field}>
            <label htmlFor="ewb-vehicle">Vehicle number</label>
            <input
              id="ewb-vehicle"
              className={ui.input}
              list="ewb-vehicle-options"
              value={vehicleNumber}
              onChange={(e) => setVehicleNumber(e.target.value)}
              placeholder="e.g. MH12AB1234"
              autoComplete="off"
            />
            <datalist id="ewb-vehicle-options">
              {vehicles.data?.map((v) => (
                <option key={v.ID} value={v.RegistrationNumber}>
                  {v.Nickname ? `${v.RegistrationNumber} (${v.Nickname})` : v.RegistrationNumber}
                </option>
              ))}
            </datalist>
          </div>
          <div className={ui.field}>
            <label htmlFor="ewb-transporter">Transporter name</label>
            <input
              id="ewb-transporter"
              className={ui.input}
              list="ewb-transporter-options"
              value={transporterName}
              onChange={(e) => setTransporterName(e.target.value)}
              autoComplete="off"
            />
            <datalist id="ewb-transporter-options">
              {transporters.data?.map((t) => (
                <option key={t.ID} value={t.Name} />
              ))}
            </datalist>
          </div>
          <div className={ui.field}>
            <label htmlFor="ewb-distance">Transport distance (km)</label>
            <input
              id="ewb-distance"
              className={ui.input}
              inputMode="decimal"
              value={distanceKm}
              onChange={(e) => setDistanceKm(e.target.value)}
              placeholder="e.g. 42"
            />
          </div>
          <div className={ui.formActions}>
            <button
              type="button"
              className={ui.btnPrimary}
              disabled={updateTransportInfo.isPending || (!vehicleNumber && !transporterName && !distanceKm)}
              onClick={() => updateTransportInfo.mutate()}
            >
              Save details
            </button>
          </div>
          {updateTransportInfo.isError ? (
            <p className={styles.errorText} role="alert">
              {updateTransportInfo.error instanceof ApiError ? updateTransportInfo.error.message : "Could not save these details."}
            </p>
          ) : null}
        </div>
      </div>
    );
  }

  // requirement === "READY"
  return (
    <div className={styles.card}>
      <div className={styles.header}>
        <h2>e-Way Bill</h2>
        <span className={ui.badge} data-tone={record?.Status === "AWAITING_PORTAL_COMPLETION" ? "warning" : "neutral"}>
          {record?.Status === "AWAITING_PORTAL_COMPLETION" ? "Waiting for portal" : "Ready"}
        </span>
      </div>
      {record?.Status === "AWAITING_PORTAL_COMPLETION" ? (
        <>
          <p className={styles.explainer}>
            We prepared your file{record.PreparedFileName ? ` (${record.PreparedFileName})` : ""} and opened the
            official government e-Way Bill portal in a new tab. On that site, go to{" "}
            <strong>e-Waybill → Generate Bulk</strong>, choose the file we prepared, then come back here and enter
            the e-Way Bill number you were given.
          </p>
          {portalUrl.data?.url ? (
            <p className={styles.detail}>
              Portal didn't open?{" "}
              <a href={portalUrl.data.url} target="_blank" rel="noopener noreferrer">
                Open it here
              </a>
              .
            </p>
          ) : null}
        </>
      ) : (
        <p className={styles.explainer}>
          This sale is ready. We'll prepare a file for the official government e-Way Bill portal and open the portal
          for you in a new tab. Once there, go to <strong>e-Waybill → Generate Bulk</strong> and upload the file we
          prepared — you complete that last step yourself, in your own browser tab. We never see your government
          portal login.
        </p>
      )}
      <div className={styles.actions}>
        <button type="button" className={ui.btnPrimary} disabled={prepare.isPending} onClick={() => prepare.mutate()}>
          {prepare.isPending ? "Preparing…" : record?.Status === "AWAITING_PORTAL_COMPLETION" ? "Prepare again" : "Prepare & open portal"}
        </button>
        <button type="button" className={ui.btnSecondary} onClick={() => setShowManualForm((v) => !v)}>
          I already have a number
        </button>
      </div>
      {prepare.isError ? (
        <p className={styles.errorText} role="alert">
          {prepare.error instanceof Error ? prepare.error.message : "Could not prepare the e-Way Bill."}
        </p>
      ) : null}
      {showManualForm ? (
        <div className={styles.form}>
          <div className={ui.field}>
            <label htmlFor="ewb-number">e-Way Bill number</label>
            <input id="ewb-number" className={ui.input} value={ewbNumber} onChange={(e) => setEwbNumber(e.target.value)} />
          </div>
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="ewb-valid-from">Valid from</label>
              <input
                id="ewb-valid-from"
                type="datetime-local"
                className={ui.input}
                value={validFrom}
                onChange={(e) => setValidFrom(e.target.value)}
              />
            </div>
            <div className={ui.field}>
              <label htmlFor="ewb-valid-until">Valid until</label>
              <input
                id="ewb-valid-until"
                type="datetime-local"
                className={ui.input}
                value={validUntil}
                onChange={(e) => setValidUntil(e.target.value)}
              />
            </div>
          </div>
          <div className={ui.formActions}>
            <button
              type="button"
              className={ui.btnPrimary}
              disabled={!ewbNumber || !validFrom || !validUntil || manualResult.isPending}
              onClick={() => manualResult.mutate()}
            >
              Save e-Way Bill number
            </button>
          </div>
          {manualResult.isError ? (
            <p className={styles.errorText} role="alert">
              {manualResult.error instanceof ApiError ? manualResult.error.message : "Could not save this e-Way Bill number."}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
