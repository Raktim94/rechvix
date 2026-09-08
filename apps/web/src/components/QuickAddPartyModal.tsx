import { useMutation } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import modal from "./Modal.module.css";
import ui from "./ui.module.css";
import { ApiError } from "../lib/api-client";
import { createPartyWithDetails } from "../lib/parties";
import type { Party, PartyType } from "../lib/partyTypes";

/** Shared by BillingPage's "New customer" quick-add and (planned)
 * PurchasesPage's "New distributor" flow — both need the exact same
 * three-call sequence (party, then optionally a tax registration and an
 * address), just with a different PartyType and a couple of labels. */
export function QuickAddPartyModal({
  open,
  onOpenChange,
  partyType,
  currencyCode,
  initialLegalName,
  onCreated,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  partyType: PartyType;
  currencyCode: string;
  initialLegalName?: string;
  onCreated: (party: Party) => void;
}) {
  const [legalName, setLegalName] = useState(initialLegalName ?? "");
  const [phone, setPhone] = useState("");
  const [showMore, setShowMore] = useState(false);
  const [gstin, setGstin] = useState("");
  const [line1, setLine1] = useState("");
  const [city, setCity] = useState("");
  const [state, setState] = useState("");
  const [postalCode, setPostalCode] = useState("");

  useEffect(() => {
    if (!open) return;
    setLegalName(initialLegalName ?? "");
    setPhone("");
    setShowMore(false);
    setGstin("");
    setLine1("");
    setCity("");
    setState("");
    setPostalCode("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") onOpenChange(false);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, onOpenChange]);

  const create = useMutation({
    mutationFn: () =>
      createPartyWithDetails({
        partyType,
        legalName,
        phone,
        currencyCode,
        gstin,
        addressLine1: line1,
        city,
        state,
        postalCode,
      }),
    onSuccess: (party) => {
      onCreated(party);
      onOpenChange(false);
    },
  });

  if (!open) return null;

  const label = partyType === "SUPPLIER" ? "distributor" : "customer";

  return (
    <div className={modal.overlay} onClick={() => onOpenChange(false)}>
      <div className={modal.dialog} role="dialog" aria-modal="true" aria-label={`New ${label}`} onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>New {label}</h2>
          <button type="button" className={modal.closeButton} aria-label="Close" onClick={() => onOpenChange(false)}>
            ×
          </button>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (legalName.trim()) create.mutate();
          }}
        >
          <div className={modal.body}>
            <div className={ui.field}>
              <label htmlFor="qap-name">Name*</label>
              <input id="qap-name" className={ui.input} autoFocus value={legalName} onChange={(e) => setLegalName(e.target.value)} required />
            </div>
            <div className={ui.field}>
              <label htmlFor="qap-phone">Phone</label>
              <input id="qap-phone" className={ui.input} type="tel" value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="10-digit mobile number" />
            </div>

            {!showMore ? (
              <button type="button" className={modal.disclosure} onClick={() => setShowMore(true)}>
                <span className={modal.disclosureChevron} data-open="false" aria-hidden="true">
                  ›
                </span>
                Add GSTIN &amp; address
              </button>
            ) : (
              <div className={modal.disclosureBody}>
                <div className={ui.field}>
                  <label htmlFor="qap-gstin">GSTIN</label>
                  <input id="qap-gstin" className={ui.input} value={gstin} onChange={(e) => setGstin(e.target.value)} placeholder="15-character GST number" maxLength={15} />
                </div>
                <div className={ui.field}>
                  <label htmlFor="qap-line1">Address</label>
                  <input id="qap-line1" className={ui.input} value={line1} onChange={(e) => setLine1(e.target.value)} placeholder="Street / shop address" />
                </div>
                <div className={ui.formGrid}>
                  <div className={ui.field}>
                    <label htmlFor="qap-city">City</label>
                    <input id="qap-city" className={ui.input} value={city} onChange={(e) => setCity(e.target.value)} />
                  </div>
                  <div className={ui.field}>
                    <label htmlFor="qap-state">State</label>
                    <input id="qap-state" className={ui.input} value={state} onChange={(e) => setState(e.target.value)} />
                  </div>
                  <div className={ui.field}>
                    <label htmlFor="qap-postal">PIN code</label>
                    <input id="qap-postal" className={ui.input} value={postalCode} onChange={(e) => setPostalCode(e.target.value)} />
                  </div>
                </div>
              </div>
            )}

            {create.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)", margin: 0 }}>
                {create.error instanceof ApiError ? create.error.message : `Could not create this ${label}.`}
              </p>
            ) : null}
          </div>
          <div className={modal.footer}>
            <button type="button" className={ui.btnSecondary} onClick={() => onOpenChange(false)}>
              Cancel
            </button>
            <button type="submit" className={ui.btnPrimary} disabled={!legalName.trim() || create.isPending}>
              {create.isPending ? "Adding…" : `Add ${label}`}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
