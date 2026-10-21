import { useEffect, useMemo, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useTranslation } from "react-i18next";
import { useNavigate } from "@tanstack/react-router";
import styles from "./auth.module.css";
import { Logo } from "../components/Logo";
import { LanguageSelect } from "../components/LanguageSelect";
import { PasswordInput } from "../components/PasswordInput";
import { api, ApiError } from "../lib/api-client";
import { GST_STATE_CODES } from "../lib/gstStateCodes";
import { countryName, currencyName, sortByName } from "../lib/names";
import { useCountries, useCurrencies } from "../lib/reference";
import { slugCode } from "../lib/slug";

/**
 * First-run setup: creates the first organisation + owner user via
 * internal/modules/identity's POST /auth/bootstrap (Stage 2). That
 * endpoint has no permission check by design — it's meant for a fresh,
 * empty deployment only, and the composition root gates it behind
 * ENABLE_BOOTSTRAP (see deploy/compose/.env.example). This screen doesn't
 * try to detect whether bootstrap is still open; a deployment that has
 * disabled it will simply 404/error here, which is an acceptable outcome
 * for a screen nobody should be reaching post-setup anyway.
 *
 * Country, currency and timezone are chosen here (they used to be fixed to
 * India). The country selects the tax regime on the server; only India needs
 * a GST state and GSTIN (docs/adr/0007), so those fields appear for IN only.
 */
type FormValues = {
  organisationName: string;
  legalEntityName: string;
  countryCode: string;
  currencyCode: string;
  timezone: string;
  gstStateCode?: string;
  gstin?: string;
  branchName: string;
  warehouseName: string;
  ownerFullName: string;
  ownerEmail: string;
  ownerPassword: string;
  confirmPassword: string;
};

interface BootstrapResponse {
  organisation_id: string;
}

/** The browser's own region ("es-ES" -> "ES") as a starting guess; the user confirms it. */
function guessCountry(): string {
  for (const tag of navigator.languages ?? [navigator.language]) {
    const region = tag.split("-")[1];
    if (region && /^[A-Za-z]{2}$/.test(region)) return region.toUpperCase();
  }
  return "IN";
}

function timezones(): string[] {
  const current = Intl.DateTimeFormat().resolvedOptions().timeZone;
  try {
    const all = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.("timeZone") ?? [];
    return all.length ? all : [current];
  } catch {
    return [current];
  }
}

export function BootstrapPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [serverError, setServerError] = useState<string | null>(null);
  const countries = useCountries();
  const currencies = useCurrencies();
  const zones = useMemo(timezones, []);

  // Built per render so validation messages follow the selected language.
  const schema = z
    .object({
      organisationName: z.string().min(1, t("setup.businessNameRequired")),
      legalEntityName: z.string().min(1, t("setup.legalEntityRequired")),
      countryCode: z.string().min(1, t("setup.countryRequired")),
      currencyCode: z.string().min(1, t("setup.currencyRequired")),
      timezone: z.string().min(1),
      gstStateCode: z.string().optional(),
      gstin: z.string().optional(),
      branchName: z.string().min(1, t("setup.branchRequired")),
      warehouseName: z.string().min(1, t("setup.warehouseRequired")),
      ownerFullName: z.string().min(1, t("setup.nameRequired")),
      ownerEmail: z.string().min(1, t("auth.emailRequired")).email(t("auth.emailInvalid")),
      ownerPassword: z.string().min(12, t("setup.passwordMin")),
      confirmPassword: z.string().min(1, t("setup.confirmRequired")),
    })
    .refine((v) => v.ownerPassword === v.confirmPassword, { message: t("setup.passwordsMismatch"), path: ["confirmPassword"] })
    // Every Indian invoice needs its issuing state to choose intra- vs
    // inter-state tax, so India must supply one (docs/adr/0007). Other
    // countries have no GST state.
    .refine((v) => v.countryCode !== "IN" || !!v.gstStateCode, { message: t("setup.stateRequired"), path: ["gstStateCode"] });

  const initialCountry = useMemo(guessCountry, []);
  const {
    register,
    handleSubmit,
    setValue,
    watch,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: { countryCode: initialCountry, currencyCode: "", timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, gstStateCode: "" },
  });

  const countryCode = watch("countryCode");
  const country = countries.data?.find((c) => c.code === countryCode);
  const taxMissing = !!country && !country.tax_pack;

  // Start from the country's own currency; picking another one afterwards
  // sticks until the country changes again.
  const countryDefaultCurrency = country?.default_currency;
  useEffect(() => {
    if (countryDefaultCurrency) setValue("currencyCode", countryDefaultCurrency);
  }, [countryDefaultCurrency, countryCode, setValue]);

  const onSubmit = async (values: FormValues) => {
    setServerError(null);
    const isIndia = values.countryCode === "IN";
    try {
      await api.post<BootstrapResponse>("/auth/bootstrap", {
        organisation_name: values.organisationName,
        default_currency_code: values.currencyCode,
        default_timezone: values.timezone,
        legal_entity_name: values.legalEntityName,
        country_code: values.countryCode,
        gst_state_code: isIndia ? values.gstStateCode : undefined,
        gstin: isIndia ? values.gstin || undefined : undefined,
        branch_code: slugCode(values.branchName, 16) || "BR1",
        branch_name: values.branchName,
        warehouse_code: slugCode(values.warehouseName, 16) || "WH1",
        warehouse_name: values.warehouseName,
        owner_full_name: values.ownerFullName,
        owner_email: values.ownerEmail,
        owner_password: values.ownerPassword,
      });
      await navigate({ to: "/login", search: { created: true } });
    } catch (err) {
      setServerError(err instanceof ApiError ? err.message : t("setup.failed"));
    }
  };

  return (
    <main className={styles.page}>
      <div className={styles.card} style={{ maxWidth: 460 }}>
        <div className={styles.wordmark}>
          <Logo />
          rechvix
        </div>
        <h1 className={styles.title}>{t("setup.title")}</h1>
        <p className={styles.subtitle}>{t("setup.subtitle")}</p>

        {serverError ? (
          <div className={styles.formError} role="alert">
            {serverError}
          </div>
        ) : null}

        {/* eslint-disable-next-line @typescript-eslint/no-misused-promises */}
        <form onSubmit={handleSubmit(onSubmit)} noValidate>
          <div className={styles.field}>
            <label htmlFor="organisationName">{t("setup.businessName")}</label>
            <input id="organisationName" {...register("organisationName")} />
            {errors.organisationName ? <p className={styles.error}>{errors.organisationName.message}</p> : null}
          </div>
          <div className={styles.field}>
            <label htmlFor="legalEntityName">{t("setup.legalEntityName")}</label>
            <input id="legalEntityName" {...register("legalEntityName")} />
            {errors.legalEntityName ? <p className={styles.error}>{errors.legalEntityName.message}</p> : null}
          </div>
          {/* The selects mount only once their options exist: a native <select> cannot take
              a default value before its options are in the DOM, and would silently show the
              first entry (Andorra) while the form held another country. */}
          {countries.data && currencies.data ? (
          <div className={styles.grid2}>
            <div className={styles.field}>
              <label htmlFor="countryCode">{t("setup.country")}</label>
              <select id="countryCode" {...register("countryCode")}>
                {sortByName(countries.data ?? [], (c) => countryName(c.code, c.name)).map((c) => (
                  <option key={c.code} value={c.code}>
                    {countryName(c.code, c.name)}
                  </option>
                ))}
              </select>
              {errors.countryCode ? <p className={styles.error}>{errors.countryCode.message}</p> : null}
            </div>
            <div className={styles.field}>
              <label htmlFor="currencyCode">{t("setup.currency")}</label>
              <select id="currencyCode" {...register("currencyCode")}>
                {sortByName(currencies.data ?? [], (c) => c.code).map((c) => (
                  <option key={c.code} value={c.code}>
                    {c.code} — {currencyName(c.code, c.name)}
                  </option>
                ))}
              </select>
              {errors.currencyCode ? <p className={styles.error}>{errors.currencyCode.message}</p> : null}
            </div>
          </div>
          ) : (
            <p className={styles.subtitle} role="status">
              {t("common.loading")}
            </p>
          )}
          {taxMissing ? (
            <p className={styles.error} role="status">
              {t("tax.notSupported", { country: country ? countryName(country.code, country.name) : "" })}
            </p>
          ) : null}
          <div className={styles.field}>
            <label htmlFor="timezone">{t("setup.timezone")}</label>
            <select id="timezone" {...register("timezone")}>
              {zones.map((z) => (
                <option key={z} value={z}>
                  {z}
                </option>
              ))}
            </select>
          </div>
          {countryCode === "IN" ? (
            <div className={styles.grid2}>
              <div className={styles.field}>
                <label htmlFor="gstStateCode">{t("setup.businessState")}</label>
                <select id="gstStateCode" {...register("gstStateCode")}>
                  <option value="" disabled>
                    {t("setup.selectState")}
                  </option>
                  {GST_STATE_CODES.map((s) => (
                    <option key={s.code} value={s.code}>
                      {s.name}
                    </option>
                  ))}
                </select>
                {errors.gstStateCode ? <p className={styles.error}>{errors.gstStateCode.message}</p> : null}
              </div>
              <div className={styles.field}>
                <label htmlFor="gstin">{t("setup.gstin")}</label>
                <input id="gstin" placeholder={t("setup.gstinPlaceholder")} {...register("gstin")} />
              </div>
            </div>
          ) : null}
          <div className={styles.grid2}>
            <div className={styles.field}>
              <label htmlFor="branchName">{t("setup.firstBranch")}</label>
              <input id="branchName" {...register("branchName")} />
              {errors.branchName ? <p className={styles.error}>{errors.branchName.message}</p> : null}
            </div>
            <div className={styles.field}>
              <label htmlFor="warehouseName">{t("setup.firstWarehouse")}</label>
              <input id="warehouseName" {...register("warehouseName")} />
              {errors.warehouseName ? <p className={styles.error}>{errors.warehouseName.message}</p> : null}
            </div>
          </div>
          <div className={styles.field}>
            <label htmlFor="ownerFullName">{t("setup.yourName")}</label>
            <input id="ownerFullName" autoComplete="name" {...register("ownerFullName")} />
            {errors.ownerFullName ? <p className={styles.error}>{errors.ownerFullName.message}</p> : null}
          </div>
          <div className={styles.field}>
            <label htmlFor="ownerEmail">{t("setup.yourEmail")}</label>
            <input id="ownerEmail" type="email" autoComplete="username" {...register("ownerEmail")} />
            {errors.ownerEmail ? <p className={styles.error}>{errors.ownerEmail.message}</p> : null}
          </div>
          <div className={styles.grid2}>
            <div className={styles.field}>
              <label htmlFor="ownerPassword">{t("setup.password")}</label>
              <PasswordInput id="ownerPassword" autoComplete="new-password" {...register("ownerPassword")} />
              {errors.ownerPassword ? <p className={styles.error}>{errors.ownerPassword.message}</p> : null}
            </div>
            <div className={styles.field}>
              <label htmlFor="confirmPassword">{t("setup.confirmPassword")}</label>
              <PasswordInput id="confirmPassword" autoComplete="new-password" {...register("confirmPassword")} />
              {errors.confirmPassword ? <p className={styles.error}>{errors.confirmPassword.message}</p> : null}
            </div>
          </div>
          <button type="submit" className={styles.submit} disabled={isSubmitting}>
            {isSubmitting ? t("setup.creating") : t("setup.create")}
          </button>
        </form>
      </div>
      <p className={styles.brandFooter}>
        <LanguageSelect />
        <br />
        Built by{" "}
        <a href="https://www.nodedr.com/" target="_blank" rel="noopener noreferrer">
          NodeDR Infotech Private Limited
        </a>
      </p>
    </main>
  );
}
