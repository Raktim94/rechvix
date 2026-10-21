import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import ui from "./ui.module.css";
import { api } from "../lib/api-client";
import { countryName, sortByName } from "../lib/names";
import { useCountries, useTaxReference } from "../lib/reference";

/** Tax facts for a sale outside India. They are fixed when the sale starts,
 * like the GST place of supply is, and feed the server's tax engine
 * (internal/modules/taxregimes). The server, not this form, decides the tax. */
export interface TaxDetails {
  customerCountry: string; // "" = domestic
  supplyRegion: string; // ISO 3166-2, e.g. US-CA
  taxExempt: boolean;
  surcharge: boolean;
  registrationId: string; // customer tax registration id, "" = none
  pricesIncludeTax: boolean; // prices entered are tax-inclusive (retail)
}

export const EMPTY_TAX_DETAILS: TaxDetails = { customerCountry: "", supplyRegion: "", taxExempt: false, surcharge: false, registrationId: "", pricesIncludeTax: false };

interface Registration {
  ID: string;
  CountryCode: string;
  RegistrationNumber: string;
}

/** True when the details are complete enough to start a sale. */
export function taxDetailsReady(supplierCountry: string, d: TaxDetails): boolean {
  const destination = d.customerCountry || supplierCountry;
  return !(supplierCountry === "US" && destination === "US" && !d.supplyRegion);
}

export function TaxDetailsFields({
  supplierCountry,
  customerId,
  value,
  onChange,
}: {
  supplierCountry: string;
  customerId: string;
  value: TaxDetails;
  onChange: (next: TaxDetails) => void;
}) {
  const { t } = useTranslation();
  const countries = useCountries();
  const ref = useTaxReference(supplierCountry);
  const destination = value.customerCountry || supplierCountry;
  const crossBorder = destination !== supplierCountry;

  const registrations = useQuery({
    queryKey: ["party-tax-registrations", customerId],
    queryFn: () => api.getListField<Registration>(`/contacts/parties/${customerId}/tax-registrations`, "tax_registrations"),
    enabled: true,
  });

  const set = (patch: Partial<TaxDetails>) => onChange({ ...value, ...patch });
  const usStates = Object.entries(ref.data?.state_base_rates ?? {}).sort(([a], [b]) => a.localeCompare(b));

  return (
    <fieldset className={ui.field} style={{ border: 0, padding: 0, margin: 0 }}>
      <legend style={{ fontWeight: 600, marginBottom: 4 }}>{t("tax.heading")}</legend>

      {ref.data && !ref.data.supported ? (
        <p className={ui.muted} role="alert">
          {t("tax.notSupported", { country: countryName(supplierCountry, countries.data?.find((c) => c.code === supplierCountry)?.name ?? supplierCountry) })}
        </p>
      ) : null}

      <div className={ui.field}>
        <label htmlFor="tax-customer-country">{t("tax.customerCountry")}</label>
        <select
          id="tax-customer-country"
          className={ui.select}
          value={value.customerCountry}
          onChange={(e) => set({ customerCountry: e.target.value, registrationId: "", supplyRegion: "" })}
        >
          <option value="">{t("tax.domestic")}</option>
          {sortByName(
            (countries.data ?? []).filter((c) => c.code !== supplierCountry),
            (c) => countryName(c.code, c.name),
          ).map((c) => (
            <option key={c.code} value={c.code}>
              {countryName(c.code, c.name)}
            </option>
          ))}
        </select>
        <p className={ui.muted} style={{ marginTop: 4 }}>
          {t("tax.customerCountryHelp")}
        </p>
      </div>

      {supplierCountry === "US" && destination === "US" ? (
        <div className={ui.field}>
          <label htmlFor="tax-region">{t("tax.supplyRegion")}</label>
          <select id="tax-region" className={ui.select} value={value.supplyRegion} onChange={(e) => set({ supplyRegion: e.target.value })}>
            <option value="" disabled>
              {t("tax.selectRegion")}
            </option>
            {usStates.map(([code, rate]) => (
              <option key={code} value={code}>
                {t("tax.rateLabel", { code, rate })}
              </option>
            ))}
          </select>
          <p className={ui.muted} style={{ marginTop: 4 }}>
            {t("tax.usEstimate")}
          </p>
        </div>
      ) : null}

      {crossBorder || (registrations.data?.length ?? 0) > 0 ? (
        <div className={ui.field}>
          <label htmlFor="tax-registration">{t("tax.customerTaxId")}</label>
          <select id="tax-registration" className={ui.select} value={value.registrationId} onChange={(e) => set({ registrationId: e.target.value })}>
            <option value="">{t("tax.noTaxId")}</option>
            {(registrations.data ?? []).map((r) => (
              <option key={r.ID} value={r.ID}>
                {r.CountryCode} {r.RegistrationNumber}
              </option>
            ))}
          </select>
          <p className={ui.muted} style={{ marginTop: 4 }}>
            {t("tax.customerTaxIdHelp")}
          </p>
        </div>
      ) : null}

      <label style={{ display: "flex", gap: 8, alignItems: "center", minHeight: 44 }}>
        <input type="checkbox" checked={value.pricesIncludeTax} onChange={(e) => set({ pricesIncludeTax: e.target.checked })} aria-describedby="tax-inclusive-help" />
        {t("tax.pricesIncludeTax")}
      </label>
      <p id="tax-inclusive-help" className={ui.muted} style={{ marginTop: 0 }}>
        {t("tax.pricesIncludeTaxHelp")}
      </p>
      {supplierCountry === "US" ? (
        <label style={{ display: "flex", gap: 8, alignItems: "center", minHeight: 44 }}>
          <input type="checkbox" checked={value.taxExempt} onChange={(e) => set({ taxExempt: e.target.checked })} />
          {t("tax.customerTaxExempt")}
        </label>
      ) : null}
      {supplierCountry === "ES" && !crossBorder ? (
        <label style={{ display: "flex", gap: 8, alignItems: "center", minHeight: 44 }}>
          <input type="checkbox" checked={value.surcharge} onChange={(e) => set({ surcharge: e.target.checked })} />
          {t("tax.equivalenceSurcharge")}
        </label>
      ) : null}
    </fieldset>
  );
}
