import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { GST_STATE_CODES } from "../../lib/gstStateCodes";
import { useOrgContext, type LegalEntity } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";

/** Mirrors app.TeamMember (internal/modules/identity/app/service.go) as
 * serialized by httpapi's teamMemberDTO — no password hash ever crosses
 * this boundary. */
interface TeamMember {
  id: string;
  email: string;
  full_name: string;
  status: "ACTIVE" | "DISABLED";
  mfa_enabled: boolean;
  last_login_at?: string;
  created_at: string;
}

const addMemberSchema = z
  .object({
    fullName: z.string().min(1, "Name is required"),
    email: z.string().min(1, "Email is required").email("Enter a valid email address"),
    password: z.string().min(12, "Use at least 12 characters"),
    confirmPassword: z.string().min(1, "Confirm the password"),
  })
  .refine((v) => v.password === v.confirmPassword, {
    message: "Passwords do not match",
    path: ["confirmPassword"],
  });
type AddMemberValues = z.infer<typeof addMemberSchema>;

/** Settings > Team — the only way, beyond the one-time /setup bootstrap,
 * to give someone else at this business their own login
 * (internal/modules/identity's POST /users, Stage 12). Every member
 * added here is a full Owner-equivalent peer, not a restricted role —
 * see app.Service.CreateTeamMember's doc comment for why v1 has no
 * lesser role to assign yet. */
function TeamPanel() {
  const queryClient = useQueryClient();
  const [showAddForm, setShowAddForm] = useState(false);

  const members = useQuery({
    queryKey: ["team-members"],
    queryFn: () => api.getListField<TeamMember>("/users", "users"),
  });

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isSubmitting },
  } = useForm<AddMemberValues>({ resolver: zodResolver(addMemberSchema) });

  const [serverError, setServerError] = useState<string | null>(null);

  const onSubmit = async (values: AddMemberValues) => {
    setServerError(null);
    try {
      await api.post("/users", {
        full_name: values.fullName,
        email: values.email,
        password: values.password,
      });
      await queryClient.invalidateQueries({ queryKey: ["team-members"] });
      reset();
      setShowAddForm(false);
    } catch (err) {
      setServerError(err instanceof ApiError ? err.message : "Could not add this team member. Please try again.");
    }
  };

  return (
    <div className={layout.panel}>
      <div className={ui.toolbar}>
        <h2 style={{ margin: 0 }}>Team</h2>
        <div className={ui.toolbarSpacer} />
        {!showAddForm ? (
          <button type="button" className={ui.btnPrimary} onClick={() => setShowAddForm(true)}>
            + Add team member
          </button>
        ) : null}
      </div>

      {showAddForm ? (
        // eslint-disable-next-line @typescript-eslint/no-misused-promises
        <form onSubmit={handleSubmit(onSubmit)} noValidate style={{ marginTop: 12, marginBottom: 20 }}>
          {serverError ? (
            <div className={ui.muted} role="alert" style={{ color: "var(--color-negative)", marginBottom: 8 }}>
              {serverError}
            </div>
          ) : null}
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="member-name">Full name</label>
              <input id="member-name" className={ui.input} autoComplete="name" {...register("fullName")} />
              {errors.fullName ? <p className={ui.muted}>{errors.fullName.message}</p> : null}
            </div>
            <div className={ui.field}>
              <label htmlFor="member-email">Email</label>
              <input id="member-email" type="email" className={ui.input} autoComplete="username" {...register("email")} />
              {errors.email ? <p className={ui.muted}>{errors.email.message}</p> : null}
            </div>
            <div className={ui.field}>
              <label htmlFor="member-password">Password</label>
              <input id="member-password" type="password" className={ui.input} autoComplete="new-password" {...register("password")} />
              {errors.password ? <p className={ui.muted}>{errors.password.message}</p> : null}
            </div>
            <div className={ui.field}>
              <label htmlFor="member-confirm">Confirm password</label>
              <input id="member-confirm" type="password" className={ui.input} autoComplete="new-password" {...register("confirmPassword")} />
              {errors.confirmPassword ? <p className={ui.muted}>{errors.confirmPassword.message}</p> : null}
            </div>
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            <button
              type="button"
              className={ui.btnSecondary}
              onClick={() => {
                setShowAddForm(false);
                setServerError(null);
                reset();
              }}
            >
              Cancel
            </button>
            <button type="submit" className={ui.btnPrimary} disabled={isSubmitting}>
              {isSubmitting ? "Adding…" : "Add team member"}
            </button>
          </div>
        </form>
      ) : null}

      {members.isPending ? (
        <div className={layout.skeleton} style={{ height: 120 }} aria-hidden="true" />
      ) : members.isError ? (
        <p className={layout.errorState} role="alert">
          {members.error instanceof ApiError ? members.error.message : "Couldn't load your team."}
        </p>
      ) : (
        <div className={ui.tableScroll}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th>Name</th>
                <th>Email</th>
                <th>Status</th>
                <th>2FA</th>
                <th>Last login</th>
              </tr>
            </thead>
            <tbody>
              {members.data?.map((m) => (
                <tr key={m.id}>
                  <td>{m.full_name}</td>
                  <td>{m.email}</td>
                  <td>
                    <span className={ui.badge} data-tone={m.status === "ACTIVE" ? "positive" : "neutral"}>
                      {m.status === "ACTIVE" ? "Active" : "Disabled"}
                    </span>
                  </td>
                  <td>
                    <span className={ui.badge} data-tone={m.mfa_enabled ? "positive" : "neutral"}>
                      {m.mfa_enabled ? "Enabled" : "Off"}
                    </span>
                  </td>
                  <td>{m.last_login_at ? new Date(m.last_login_at).toLocaleString() : "Never"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

/** Recovery path for a legal entity that was bootstrapped before it had a
 * state set (docs/adr/0007) — without one, that entity can never finalize
 * a single invoice. Also lets a business add/correct its GSTIN later. */
function GSTDetailsForm({ legalEntityId, currentGSTIN, currentStateCode }: { legalEntityId: string; currentGSTIN: string; currentStateCode: string }) {
  const queryClient = useQueryClient();
  const [gstin, setGstin] = useState(currentGSTIN);
  const [stateCode, setStateCode] = useState(currentStateCode);

  const save = useMutation({
    mutationFn: () => api.put(`/legal-entities/${legalEntityId}/gst`, { gstin, gst_state_code: stateCode }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["legal-entities"] });
    },
  });

  const dirty = gstin !== currentGSTIN || stateCode !== currentStateCode;

  return (
    <div className={ui.formGrid} style={{ marginTop: 12 }}>
      <div className={ui.field}>
        <label htmlFor="settings-gst-state">Business state</label>
        <select id="settings-gst-state" className={ui.select} value={stateCode} onChange={(e) => setStateCode(e.target.value)}>
          <option value="" disabled>
            Select a state…
          </option>
          {GST_STATE_CODES.map((s) => (
            <option key={s.code} value={s.code}>
              {s.name}
            </option>
          ))}
        </select>
        {!currentStateCode ? (
          <p className={ui.muted} style={{ marginTop: 4 }}>
            No state set yet — you cannot finalize any invoice until this is saved.
          </p>
        ) : null}
      </div>
      <div className={ui.field}>
        <label htmlFor="settings-gstin">GSTIN (optional)</label>
        <input id="settings-gstin" className={ui.input} placeholder="Leave blank if not GST-registered" value={gstin} onChange={(e) => setGstin(e.target.value)} />
      </div>
      <button type="button" className={ui.btnPrimary} disabled={!dirty || !stateCode || save.isPending} onClick={() => save.mutate()}>
        {save.isPending ? "Saving…" : "Save GST details"}
      </button>
      {save.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)" }}>
          {save.error instanceof ApiError ? save.error.message : "Could not save GST details."}
        </p>
      ) : null}
      {save.isSuccess ? <p style={{ color: "var(--color-positive)" }}>Saved.</p> : null}
    </div>
  );
}

const MAX_LOGO_BYTES = 2_000_000;

interface InvoiceBrandingFields {
  phone: string;
  email: string;
  website: string;
  address: string;
  bankName: string;
  bankAccountNumber: string;
  bankIfsc: string;
  upiId: string;
  authorizedSignatoryName: string;
  defaultTermsAndConditions: string;
}

function invoiceBrandingFieldsFrom(le: LegalEntity): InvoiceBrandingFields {
  return {
    phone: le.Phone,
    email: le.Email,
    website: le.Website,
    address: le.Address,
    bankName: le.BankName,
    bankAccountNumber: le.BankAccountNumber,
    bankIfsc: le.BankIFSC,
    upiId: le.UPIID,
    authorizedSignatoryName: le.AuthorizedSignatoryName,
    defaultTermsAndConditions: le.DefaultTermsAndConditions,
  };
}

/** Everything a printed invoice/quotation/receipt can show beyond GSTIN —
 * logo, contact details, bank account, UPI ID, signatory, and a default
 * terms-and-conditions text. The print templates
 * (internal/modules/sales/printing) have supported rendering all of this
 * since Stage 5b; this is the first screen that lets anyone actually set
 * it (migrations/0034). */
function InvoiceBrandingForm({ legalEntity }: { legalEntity: LegalEntity }) {
  const queryClient = useQueryClient();
  const [fields, setFields] = useState<InvoiceBrandingFields>(() => invoiceBrandingFieldsFrom(legalEntity));
  const [logoFile, setLogoFile] = useState<{ base64: string; previewUrl: string } | null>(null);
  const [removeLogo, setRemoveLogo] = useState(false);
  const [logoError, setLogoError] = useState<string | null>(null);

  const currentLogoUrl = legalEntity.LogoPNG ? `data:image/png;base64,${legalEntity.LogoPNG}` : null;
  const previewUrl = logoFile ? logoFile.previewUrl : removeLogo ? null : currentLogoUrl;

  const save = useMutation({
    mutationFn: () =>
      api.put(`/legal-entities/${legalEntity.ID}/invoice-branding`, {
        phone: fields.phone,
        email: fields.email,
        website: fields.website,
        address: fields.address,
        bank_name: fields.bankName,
        bank_account_number: fields.bankAccountNumber,
        bank_ifsc: fields.bankIfsc,
        upi_id: fields.upiId,
        authorized_signatory_name: fields.authorizedSignatoryName,
        default_terms_and_conditions: fields.defaultTermsAndConditions,
        ...(logoFile ? { logo_png_base64: logoFile.base64 } : {}),
        ...(removeLogo && !logoFile ? { remove_logo: true } : {}),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["legal-entities"] });
      setLogoFile(null);
      setRemoveLogo(false);
    },
  });

  const setField = (key: keyof InvoiceBrandingFields) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setFields((f) => ({ ...f, [key]: e.target.value }));

  const onLogoChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setLogoError(null);
    const file = e.target.files?.[0];
    e.target.value = ""; // clear so re-selecting the same file still fires onChange
    if (!file) return;
    if (file.size > MAX_LOGO_BYTES) {
      setLogoError("Logo image is too large — please use a file under 2MB.");
      return;
    }
    const reader = new FileReader();
    reader.onload = () => {
      const dataUrl = reader.result as string;
      setLogoFile({ base64: dataUrl.slice(dataUrl.indexOf(",") + 1), previewUrl: dataUrl });
      setRemoveLogo(false);
    };
    reader.onerror = () => setLogoError("Could not read this file.");
    reader.readAsDataURL(file);
  };

  const dirty = JSON.stringify(fields) !== JSON.stringify(invoiceBrandingFieldsFrom(legalEntity)) || !!logoFile || removeLogo;

  return (
    <div className={layout.panel}>
      <h2>Invoice branding</h2>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>
        Shown on every printed invoice, quotation, and receipt.
      </p>

      <div style={{ display: "flex", gap: 16, alignItems: "center", marginBottom: 20 }}>
        <div
          style={{
            width: 72,
            height: 72,
            borderRadius: 8,
            border: "1px dashed var(--color-border-strong)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            overflow: "hidden",
            flexShrink: 0,
            background: "var(--color-surface-alt)",
          }}
        >
          {previewUrl ? (
            <img src={previewUrl} alt="Business logo" style={{ maxWidth: "100%", maxHeight: "100%" }} />
          ) : (
            <span className={ui.muted} style={{ fontSize: 11 }}>
              No logo
            </span>
          )}
        </div>
        <div>
          <label className={ui.btnSecondary} style={{ cursor: "pointer" }}>
            {previewUrl ? "Change logo" : "Upload logo"}
            <input type="file" accept="image/png,image/jpeg,image/gif" onChange={onLogoChange} style={{ display: "none" }} />
          </label>
          {previewUrl ? (
            <button
              type="button"
              className={ui.btnSecondary}
              style={{ marginLeft: 8 }}
              onClick={() => {
                setLogoFile(null);
                setRemoveLogo(true);
              }}
            >
              Remove
            </button>
          ) : null}
          <p className={ui.muted} style={{ marginTop: 6 }}>
            PNG, JPEG, or GIF. Max 2MB, 1000×1000px.
          </p>
          {logoError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 4 }}>
              {logoError}
            </p>
          ) : null}
        </div>
      </div>

      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="ib-phone">Phone</label>
          <input id="ib-phone" className={ui.input} value={fields.phone} onChange={setField("phone")} />
        </div>
        <div className={ui.field}>
          <label htmlFor="ib-email">Email</label>
          <input id="ib-email" type="email" className={ui.input} value={fields.email} onChange={setField("email")} />
        </div>
        <div className={ui.field}>
          <label htmlFor="ib-website">Website</label>
          <input id="ib-website" className={ui.input} value={fields.website} onChange={setField("website")} />
        </div>
        <div className={ui.field} style={{ gridColumn: "1 / -1" }}>
          <label htmlFor="ib-address">Business address</label>
          <textarea
            id="ib-address"
            className={ui.input}
            rows={2}
            value={fields.address}
            onChange={setField("address")}
            placeholder="Shown under your business name on every printed document"
          />
        </div>
        <div className={ui.field}>
          <label htmlFor="ib-bank-name">Bank name</label>
          <input id="ib-bank-name" className={ui.input} value={fields.bankName} onChange={setField("bankName")} />
        </div>
        <div className={ui.field}>
          <label htmlFor="ib-bank-account">Account number</label>
          <input id="ib-bank-account" className={ui.input} value={fields.bankAccountNumber} onChange={setField("bankAccountNumber")} />
        </div>
        <div className={ui.field}>
          <label htmlFor="ib-bank-ifsc">IFSC</label>
          <input id="ib-bank-ifsc" className={ui.input} value={fields.bankIfsc} onChange={setField("bankIfsc")} />
        </div>
        <div className={ui.field}>
          <label htmlFor="ib-upi">UPI ID</label>
          <input id="ib-upi" className={ui.input} placeholder="yourshop@bank" value={fields.upiId} onChange={setField("upiId")} />
        </div>
        <div className={ui.field}>
          <label htmlFor="ib-signatory">Authorized signatory name</label>
          <input id="ib-signatory" className={ui.input} value={fields.authorizedSignatoryName} onChange={setField("authorizedSignatoryName")} />
        </div>
        <div className={ui.field} style={{ gridColumn: "1 / -1" }}>
          <label htmlFor="ib-terms">Default terms &amp; conditions</label>
          <textarea
            id="ib-terms"
            className={ui.input}
            rows={2}
            value={fields.defaultTermsAndConditions}
            onChange={setField("defaultTermsAndConditions")}
            placeholder="Printed on every invoice unless a specific one overrides it"
          />
        </div>
      </div>

      <div className={ui.formActions} style={{ marginTop: 16 }}>
        <button type="button" className={ui.btnPrimary} disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? "Saving…" : "Save invoice branding"}
        </button>
      </div>
      {save.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {save.error instanceof ApiError ? save.error.message : "Could not save invoice branding."}
        </p>
      ) : null}
      {save.isSuccess && !dirty ? <p style={{ color: "var(--color-positive)", marginTop: 8 }}>Saved.</p> : null}
    </div>
  );
}

export function SettingsPage() {
  const org = useOrgContext();

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Settings</h1>
          <p className={layout.subtitle}>Your business, legal entity, branch, and warehouse.</p>
        </div>
      </div>

      <div className={layout.panel}>
        {org.isPending ? (
          <div className={layout.skeleton} style={{ height: 160 }} aria-hidden="true" />
        ) : org.isError ? (
          <p className={layout.errorState} role="alert">
            Couldn't load your business details.
          </p>
        ) : (
          <>
            <dl style={{ display: "grid", gridTemplateColumns: "180px 1fr", rowGap: 12 }}>
              <dt className={layout.subtitle}>Business</dt>
              <dd>{org.organisation?.Name}</dd>
              <dt className={layout.subtitle}>Legal entity</dt>
              <dd>{org.legalEntity?.LegalName}</dd>
              <dt className={layout.subtitle}>Branch</dt>
              <dd>{org.branch?.Name}</dd>
              <dt className={layout.subtitle}>Warehouse</dt>
              <dd>{org.warehouse?.Name}</dd>
              <dt className={layout.subtitle}>Currency</dt>
              <dd>{org.organisation?.DefaultCurrencyCode}</dd>
            </dl>
            {org.legalEntity ? (
              <GSTDetailsForm
                // Keyed by the values themselves so the form's local
                // draft state remounts fresh after a successful save
                // (queryClient.invalidateQueries refetches org.legalEntity)
                // instead of needing an effect to resync it — avoids the
                // cascading-render pattern an effect-based sync creates.
                key={`${org.legalEntity.ID}-${org.legalEntity.GSTIN}-${org.legalEntity.GSTStateCode}`}
                legalEntityId={org.legalEntity.ID}
                currentGSTIN={org.legalEntity.GSTIN}
                currentStateCode={org.legalEntity.GSTStateCode}
              />
            ) : null}
          </>
        )}
      </div>

      {org.legalEntity ? (
        // Same remount-on-save-success key technique as GSTDetailsForm
        // above — UpdatedAt changes on every successful save, so a fresh
        // save invalidates the local draft state instead of needing an
        // effect to resync ~10 fields plus the logo preview.
        <InvoiceBrandingForm key={`${org.legalEntity.ID}-${org.legalEntity.UpdatedAt}`} legalEntity={org.legalEntity} />
      ) : null}

      <div className={layout.panel}>
        <h2>GST &amp; e-Way Bill</h2>
        <p className={layout.emptyState} style={{ textAlign: "left", padding: 0 }}>
          Tax rates, e-Way Bill mode, vehicles, and transporters live on the{" "}
          <Link to="/gst">GST / Tax</Link> page.
        </p>
      </div>

      <TeamPanel />
    </div>
  );
}
