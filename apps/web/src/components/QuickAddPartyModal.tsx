import { useMutation } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import modal from "./Modal.module.css";
import ui from "./ui.module.css";
import { ApiError } from "../lib/api-client";
import { createPartyWithDetails } from "../lib/parties";
import { countryName, sortByName } from "../lib/names";
import { useCountries } from "../lib/reference";
import { useOrgContext } from "../lib/useOrgContext";
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
  const { t } = useTranslation();
  // The party defaults to the company's own country; India keeps its GSTIN/PIN-code form.
  const companyCountry = useOrgContext().legalEntity?.CountryCode ?? "IN";
  const countries = useCountries();
  const [partyCountry, setPartyCountry] = useState(companyCountry);
  const isIndia = partyCountry === "IN";
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
    setPartyCountry(companyCountry);
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
        countryCode: partyCountry,
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

  const isSupplier = partyType === "SUPPLIER";
  const title = t(isSupplier ? "party.newDistributor" : "party.newCustomer");

  return (
    <div className={modal.overlay} onClick={() => onOpenChange(false)}>
      <div className={modal.dialog} role="dialog" aria-modal="true" aria-label={title} onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>{title}</h2>
          <button type="button" className={modal.closeButton} aria-label={t("common.close")} onClick={() => onOpenChange(false)}>
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
              <label htmlFor="qap-name">{t("party.name")}*</label>
              <input id="qap-name" className={ui.input} autoFocus value={legalName} onChange={(e) => setLegalName(e.target.value)} required />
            </div>
            <div className={ui.field}>
              <label htmlFor="qap-phone">{t("party.phone")}</label>
              <input
                id="qap-phone"
                className={ui.input}
                type="tel"
                inputMode={isIndia ? "numeric" : "tel"}
                maxLength={isIndia ? 10 : 20}
                value={phone}
                // India: a 10-digit mobile number. Elsewhere phone formats vary (+, spaces, area codes), so only strip letters.
                onChange={(e) => setPhone(isIndia ? e.target.value.replace(/\D/g, "").slice(0, 10) : e.target.value.replace(/[^\d+()\-\s]/g, "").slice(0, 20))}
                placeholder={t(isIndia ? "party.phoneIn" : "party.phoneOther")}
              />
            </div>

            {!showMore ? (
              <button type="button" className={modal.disclosure} onClick={() => setShowMore(true)}>
                <span className={modal.disclosureChevron} data-open="false" aria-hidden="true">
                  ›
                </span>
                {t(isIndia ? "party.addDetailsIn" : "party.addDetailsOther")}
              </button>
            ) : (
              <div className={modal.disclosureBody}>
                <div className={ui.field}>
                  <label htmlFor="qap-country">{t("party.country")}</label>
                  <select id="qap-country" className={ui.select} value={partyCountry} onChange={(e) => setPartyCountry(e.target.value)}>
                    {sortByName<{ code: string; name: string }>(countries.data ?? [{ code: partyCountry, name: partyCountry }], (c) => countryName(c.code, c.name)).map((c) => (
                      <option key={c.code} value={c.code}>
                        {countryName(c.code, c.name)}
                      </option>
                    ))}
                  </select>
                </div>
                <div className={ui.field}>
                  <label htmlFor="qap-gstin">{t(isIndia ? "party.gstin" : "party.taxId")}</label>
                  <input
                    id="qap-gstin"
                    className={ui.input}
                    value={gstin}
                    onChange={(e) => setGstin(e.target.value)}
                    placeholder={t(isIndia ? "party.gstinHint" : "party.taxIdHint")}
                    maxLength={isIndia ? 15 : 30}
                  />
                </div>
                <div className={ui.field}>
                  <label htmlFor="qap-line1">{t("party.address")}</label>
                  <input id="qap-line1" className={ui.input} value={line1} onChange={(e) => setLine1(e.target.value)} placeholder={t("party.addressHint")} />
                </div>
                <div className={ui.formGrid}>
                  <div className={ui.field}>
                    <label htmlFor="qap-city">{t("party.city")}</label>
                    <input id="qap-city" className={ui.input} value={city} onChange={(e) => setCity(e.target.value)} />
                  </div>
                  <div className={ui.field}>
                    <label htmlFor="qap-state">{t(isIndia ? "party.state" : "party.region")}</label>
                    <input id="qap-state" className={ui.input} value={state} onChange={(e) => setState(e.target.value)} />
                  </div>
                  <div className={ui.field}>
                    <label htmlFor="qap-postal">{t(isIndia ? "party.pin" : "party.postal")}</label>
                    <input id="qap-postal" className={ui.input} value={postalCode} onChange={(e) => setPostalCode(e.target.value)} />
                  </div>
                </div>
              </div>
            )}

            {create.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)", margin: 0 }}>
                {create.error instanceof ApiError ? create.error.message : t(isSupplier ? "party.couldNotCreateDistributor" : "party.couldNotCreateCustomer")}
              </p>
            ) : null}
          </div>
          <div className={modal.footer}>
            <button type="button" className={ui.btnSecondary} onClick={() => onOpenChange(false)}>
              {t("common.cancel")}
            </button>
            <button type="submit" className={ui.btnPrimary} disabled={!legalName.trim() || create.isPending}>
              {create.isPending ? t("party.adding") : t(isSupplier ? "party.addDistributor" : "party.addCustomer")}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
