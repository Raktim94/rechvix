import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useNavigate } from "@tanstack/react-router";
import styles from "./auth.module.css";
import { Logo } from "../components/Logo";
import { api, ApiError } from "../lib/api-client";
import { GST_STATE_CODES } from "../lib/gstStateCodes";

/**
 * First-run setup: creates the first organisation + owner user via
 * internal/modules/identity's POST /auth/bootstrap (Stage 2). That
 * endpoint has no permission check by design — it's meant for a fresh,
 * empty deployment only, and the composition root gates it behind
 * ENABLE_BOOTSTRAP (see deploy/compose/.env.example). This screen doesn't
 * try to detect whether bootstrap is still open; a deployment that has
 * disabled it will simply 404/error here, which is an acceptable outcome
 * for a screen nobody should be reaching post-setup anyway.
 */
const schema = z
  .object({
    organisationName: z.string().min(1, "Business name is required"),
    legalEntityName: z.string().min(1, "Legal entity name is required"),
    // Required, not just GSTIN — every invoice needs its issuing state to
    // determine intra- vs. inter-state tax, regardless of whether the
    // business is GST-registered at all (docs/adr/0007). Never leave
    // this uncollected: a legal entity with no state cannot finalize a
    // single invoice, which is exactly the bug this form now closes.
    gstStateCode: z.string().min(1, "Select your business's state"),
    gstin: z.string().optional(),
    branchName: z.string().min(1, "Branch name is required"),
    warehouseName: z.string().min(1, "Warehouse name is required"),
    ownerFullName: z.string().min(1, "Your name is required"),
    ownerEmail: z.string().min(1, "Email is required").email("Enter a valid email address"),
    ownerPassword: z.string().min(12, "Use at least 12 characters"),
    confirmPassword: z.string().min(1, "Confirm your password"),
  })
  .refine((v) => v.ownerPassword === v.confirmPassword, {
    message: "Passwords do not match",
    path: ["confirmPassword"],
  });
type FormValues = z.infer<typeof schema>;

interface BootstrapResponse {
  organisation_id: string;
}

/** Same auto-slug idiom CataloguePage uses for an unset SKU code — branch_code/
 * warehouse_code are NOT NULL (warehouses.code is even UNIQUE per organisation)
 * but this form never collected them at all (docs/adr/0007's sibling gap). */
function slugCode(name: string, maxLen: number): string {
  return name
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, maxLen);
}

export function BootstrapPage() {
  const navigate = useNavigate();
  const [serverError, setServerError] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({ resolver: zodResolver(schema) });

  const onSubmit = async (values: FormValues) => {
    setServerError(null);
    try {
      await api.post<BootstrapResponse>("/auth/bootstrap", {
        organisation_name: values.organisationName,
        // default_currency_code has no database default (NOT NULL, no
        // DEFAULT) — was never sent here either, same unwired-field class
        // as gst_state_code (docs/adr/0007). INR is the only sensible
        // default for a platform whose target market is India-first.
        default_currency_code: "INR",
        default_timezone: "Asia/Kolkata",
        legal_entity_name: values.legalEntityName,
        country_code: "IN",
        gst_state_code: values.gstStateCode,
        gstin: values.gstin || undefined,
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
      setServerError(err instanceof ApiError ? err.message : "Could not complete setup. Please try again.");
    }
  };

  return (
    <div className={styles.page}>
      <div className={styles.card} style={{ maxWidth: 460 }}>
        <div className={styles.wordmark}>
          <Logo />
          rechvix
        </div>
        <h1 className={styles.title}>Set up your business</h1>
        <p className={styles.subtitle}>This runs once, on a brand-new installation.</p>

        {serverError ? (
          <div className={styles.formError} role="alert">
            {serverError}
          </div>
        ) : null}

        {/* eslint-disable-next-line @typescript-eslint/no-misused-promises */}
        <form onSubmit={handleSubmit(onSubmit)} noValidate>
          <div className={styles.field}>
            <label htmlFor="organisationName">Business name</label>
            <input id="organisationName" {...register("organisationName")} />
            {errors.organisationName ? <p className={styles.error}>{errors.organisationName.message}</p> : null}
          </div>
          <div className={styles.field}>
            <label htmlFor="legalEntityName">Legal entity name</label>
            <input id="legalEntityName" {...register("legalEntityName")} />
            {errors.legalEntityName ? <p className={styles.error}>{errors.legalEntityName.message}</p> : null}
          </div>
          <div className={styles.grid2}>
            <div className={styles.field}>
              <label htmlFor="gstStateCode">Business state</label>
              <select id="gstStateCode" {...register("gstStateCode")} defaultValue="">
                <option value="" disabled>
                  Select a state…
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
              <label htmlFor="gstin">GSTIN (optional)</label>
              <input id="gstin" placeholder="Leave blank if not GST-registered" {...register("gstin")} />
            </div>
          </div>
          <div className={styles.grid2}>
            <div className={styles.field}>
              <label htmlFor="branchName">First branch</label>
              <input id="branchName" {...register("branchName")} />
              {errors.branchName ? <p className={styles.error}>{errors.branchName.message}</p> : null}
            </div>
            <div className={styles.field}>
              <label htmlFor="warehouseName">First warehouse</label>
              <input id="warehouseName" {...register("warehouseName")} />
              {errors.warehouseName ? <p className={styles.error}>{errors.warehouseName.message}</p> : null}
            </div>
          </div>
          <div className={styles.field}>
            <label htmlFor="ownerFullName">Your name</label>
            <input id="ownerFullName" autoComplete="name" {...register("ownerFullName")} />
            {errors.ownerFullName ? <p className={styles.error}>{errors.ownerFullName.message}</p> : null}
          </div>
          <div className={styles.field}>
            <label htmlFor="ownerEmail">Your email</label>
            <input id="ownerEmail" type="email" autoComplete="username" {...register("ownerEmail")} />
            {errors.ownerEmail ? <p className={styles.error}>{errors.ownerEmail.message}</p> : null}
          </div>
          <div className={styles.grid2}>
            <div className={styles.field}>
              <label htmlFor="ownerPassword">Password</label>
              <input id="ownerPassword" type="password" autoComplete="new-password" {...register("ownerPassword")} />
              {errors.ownerPassword ? <p className={styles.error}>{errors.ownerPassword.message}</p> : null}
            </div>
            <div className={styles.field}>
              <label htmlFor="confirmPassword">Confirm password</label>
              <input
                id="confirmPassword"
                type="password"
                autoComplete="new-password"
                {...register("confirmPassword")}
              />
              {errors.confirmPassword ? <p className={styles.error}>{errors.confirmPassword.message}</p> : null}
            </div>
          </div>
          <button type="submit" className={styles.submit} disabled={isSubmitting}>
            {isSubmitting ? "Creating your business…" : "Create business and continue"}
          </button>
        </form>
      </div>
      <p className={styles.brandFooter}>Built by NodeDR Infotech Private Limited</p>
    </div>
  );
}
