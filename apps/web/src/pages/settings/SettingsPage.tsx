import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import modal from "../../components/Modal.module.css";
import { PasswordInput } from "../../components/PasswordInput";
import ui from "../../components/ui.module.css";
import { useAuth } from "../../auth/AuthProvider";
import { api, ApiError } from "../../lib/api-client";
import { GST_STATE_CODES } from "../../lib/gstStateCodes";
import { getOcrProvider, setOcrProvider, type OcrProvider } from "../../lib/ocr";
import { useOrgContext, type Branch, type LegalEntity, type Warehouse } from "../../lib/useOrgContext";
import layout from "../DashboardPage.module.css";

/** Mirrors app.TeamMember (internal/modules/identity/app/service.go) as
 * serialized by httpapi's teamMemberDTO — no password hash ever crosses
 * this boundary. unrestricted/legal_entity_ids mirror that struct's own
 * doc comment: unrestricted true means every company, in which case
 * legal_entity_ids is omitted (never sent as a misleadingly empty
 * array). */
interface TeamMember {
  id: string;
  email: string;
  full_name: string;
  status: "ACTIVE" | "DISABLED";
  mfa_enabled: boolean;
  last_login_at?: string;
  created_at: string;
  unrestricted: boolean;
  legal_entity_ids?: string[];
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
/** One checkbox per company, "checked" meaning "this member can access
 * it" — shared by the add-member form and EditCompanyAccessModal below
 * so the two never drift into different interactions for the same
 * concept. uncheckedIds is the set of company ids the caller has
 * explicitly excluded; empty (the default) means every current AND
 * future company — sent to the backend as no restriction at all
 * (CreateTeamMemberParams.LegalEntityIDs/SetTeamMemberCompanyAccess's
 * own "empty means unrestricted" contract), not as an explicit list of
 * today's companies that a newly-added company wouldn't be in. */
function CompanyAccessCheckboxes({
  legalEntities,
  uncheckedIds,
  onToggle,
}: {
  legalEntities: LegalEntity[];
  uncheckedIds: Set<string>;
  onToggle: (id: string) => void;
}) {
  return (
    <div className={ui.field}>
      <span style={{ display: "block", marginBottom: 6 }}>Company access</span>
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        {legalEntities.map((le) => (
          <label key={le.ID} style={{ display: "flex", alignItems: "center", gap: 8, fontWeight: 400 }}>
            <input type="checkbox" checked={!uncheckedIds.has(le.ID)} onChange={() => onToggle(le.ID)} />
            {le.LegalName}
          </label>
        ))}
      </div>
      <p className={ui.muted} style={{ marginTop: 4 }}>
        {uncheckedIds.size === 0
          ? "Every company checked — this member sees all of them, including any company added later."
          : "Restricted to the checked companies only."}
      </p>
    </div>
  );
}

/** Edit-time counterpart to the add-member form's own company-access
 * checkboxes — same CompanyAccessCheckboxes, same
 * PUT /users/{id}/company-access this member's initial grant already
 * used under the hood (identity.Service.SetTeamMemberCompanyAccess
 * shares its write path with CreateTeamMember, see that method's own
 * doc comment for why). */
function EditCompanyAccessModal({
  member,
  legalEntities,
  onOpenChange,
}: {
  member: TeamMember | null;
  legalEntities: LegalEntity[];
  onOpenChange: (open: boolean) => void;
}) {
  const queryClient = useQueryClient();
  const [uncheckedIds, setUncheckedIds] = useState<Set<string>>(new Set());

  useEffect(() => {
    if (!member) return;
    setUncheckedIds(member.unrestricted ? new Set() : new Set(legalEntities.map((le) => le.ID).filter((id) => !member.legal_entity_ids?.includes(id))));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [member?.id]);

  const save = useMutation({
    mutationFn: () =>
      api.put(`/users/${member?.id}/company-access`, {
        legal_entity_ids: uncheckedIds.size === 0 ? [] : legalEntities.map((le) => le.ID).filter((id) => !uncheckedIds.has(id)),
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["team-members"] });
      onOpenChange(false);
    },
  });

  if (!member) return null;

  function toggle(id: string) {
    setUncheckedIds((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  return (
    <div className={modal.overlay} onClick={() => onOpenChange(false)}>
      <div className={modal.dialog} role="dialog" aria-modal="true" aria-label={`Company access for ${member.full_name}`} onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>Company access — {member.full_name}</h2>
          <button type="button" className={modal.closeButton} aria-label="Close" onClick={() => onOpenChange(false)}>
            ×
          </button>
        </div>
        <div className={modal.body}>
          <CompanyAccessCheckboxes legalEntities={legalEntities} uncheckedIds={uncheckedIds} onToggle={toggle} />
          {save.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", margin: 0 }}>
              {save.error instanceof ApiError ? save.error.message : "Could not save company access."}
            </p>
          ) : null}
        </div>
        <div className={modal.footer}>
          <button type="button" className={ui.btnSecondary} onClick={() => onOpenChange(false)}>
            Cancel
          </button>
          <button type="button" className={ui.btnPrimary} disabled={save.isPending} onClick={() => save.mutate()}>
            {save.isPending ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
}

function TeamPanel() {
  const queryClient = useQueryClient();
  const org = useOrgContext();
  const legalEntities = org.legalEntities ?? [];
  const legalEntityNameById = new Map(legalEntities.map((le) => [le.ID, le.LegalName]));
  const [showAddForm, setShowAddForm] = useState(false);
  const [uncheckedIds, setUncheckedIds] = useState<Set<string>>(new Set());
  const [editingMember, setEditingMember] = useState<TeamMember | null>(null);

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

  function toggleCompany(id: string) {
    setUncheckedIds((cur) => {
      const next = new Set(cur);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  const onSubmit = async (values: AddMemberValues) => {
    setServerError(null);
    try {
      await api.post("/users", {
        full_name: values.fullName,
        email: values.email,
        password: values.password,
        legal_entity_ids: uncheckedIds.size === 0 ? undefined : legalEntities.map((le) => le.ID).filter((id) => !uncheckedIds.has(id)),
      });
      await queryClient.invalidateQueries({ queryKey: ["team-members"] });
      reset();
      setUncheckedIds(new Set());
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
            {/* Only worth showing once there's an actual choice to make —
                a single-company install (the overwhelming majority) never
                sees this. */}
            {legalEntities.length > 1 ? (
              <CompanyAccessCheckboxes legalEntities={legalEntities} uncheckedIds={uncheckedIds} onToggle={toggleCompany} />
            ) : null}
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            <button
              type="button"
              className={ui.btnSecondary}
              onClick={() => {
                setShowAddForm(false);
                setServerError(null);
                reset();
                setUncheckedIds(new Set());
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
                {legalEntities.length > 1 ? <th>Companies</th> : null}
                <th>Last login</th>
                {legalEntities.length > 1 ? <th /> : null}
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
                  {legalEntities.length > 1 ? (
                    <td>
                      {m.unrestricted
                        ? "All companies"
                        : (m.legal_entity_ids ?? []).map((id) => legalEntityNameById.get(id) ?? "—").join(", ") || "None"}
                    </td>
                  ) : null}
                  <td>{m.last_login_at ? new Date(m.last_login_at).toLocaleString() : "Never"}</td>
                  {legalEntities.length > 1 ? (
                    <td>
                      <button type="button" className={ui.btnGhost} onClick={() => setEditingMember(m)}>
                        Edit companies
                      </button>
                    </td>
                  ) : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <EditCompanyAccessModal member={editingMember} legalEntities={legalEntities} onOpenChange={(open) => !open && setEditingMember(null)} />
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

/** A per-browser preference (localStorage, via lib/ocr.ts), not a
 * server-persisted org setting — deliberately, since which OCR engine
 * to use is about THIS device's own tradeoff (Puter.js needs a live
 * connection and sends the image out; the offline engine doesn't), not
 * a business-wide policy every counter has to share. */
function ScanningPanel() {
  const [provider, setProvider] = useState<OcrProvider>(getOcrProvider);

  function choose(p: OcrProvider) {
    setProvider(p);
    setOcrProvider(p);
  }

  return (
    <div className={layout.panel}>
      <h2>Scanning distributor bills</h2>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>
        Which OCR engine reads a scanned purchase bill on the Purchases page's "Scan bill" button. You still review and edit
        everything it finds before anything is created.
      </p>
      <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        <label style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
          <input type="radio" name="ocr-provider" checked={provider === "offline"} onChange={() => choose("offline")} style={{ marginTop: 4 }} />
          <span>
            <strong>Offline (recommended)</strong>
            <div className={ui.muted}>Runs in this browser, not on a server — nothing about the scan itself leaves this device.</div>
          </span>
        </label>
        <label style={{ display: "flex", gap: 10, alignItems: "flex-start" }}>
          <input type="radio" name="ocr-provider" checked={provider === "puter"} onChange={() => choose("puter")} style={{ marginTop: 4 }} />
          <span>
            <strong>Puter.js (online)</strong>
            <div className={ui.muted}>Often more accurate on messy or handwritten bills — sends the scanned image to Puter's free OCR service.</div>
          </span>
        </label>
      </div>
    </div>
  );
}

/** POST /auth/change-password existed with zero frontend callers — the
 * only way to change a password was to log out and go through
 * forgot-password, which is a strange thing to make someone do who is
 * already logged in and knows their current password. Clears the
 * session cookie server-side on success (identity/httpapi's own
 * doc comment), so this also clears local session state and returns to
 * Sign-in with the new password ready to use, rather than leaving a
 * stale "logged in" UI up against a session the server just revoked.
 */
function ChangePasswordPanel() {
  const { logout } = useAuth();
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");

  const change = useMutation({
    mutationFn: () => api.post("/auth/change-password", { current_password: currentPassword, new_password: newPassword, confirm_password: confirmPassword }),
    // logout() itself now navigates to /login (see AuthProvider) — this
    // used to also call navigate() here because logout() didn't yet.
    onSuccess: () => logout(),
  });

  const mismatch = confirmPassword.length > 0 && newPassword !== confirmPassword;

  return (
    <div className={layout.panel}>
      <h2>Change password</h2>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (currentPassword && newPassword && !mismatch) change.mutate();
        }}
      >
        <div className={ui.formGrid}>
          <div className={ui.field}>
            <label htmlFor="current-password">Current password</label>
            <PasswordInput id="current-password" autoComplete="current-password" value={currentPassword} onChange={(e) => setCurrentPassword(e.target.value)} />
          </div>
          <div className={ui.field}>
            <label htmlFor="new-password">New password</label>
            <PasswordInput id="new-password" autoComplete="new-password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} />
          </div>
          <div className={ui.field}>
            <label htmlFor="confirm-new-password">Confirm new password</label>
            <PasswordInput id="confirm-new-password" autoComplete="new-password" value={confirmPassword} onChange={(e) => setConfirmPassword(e.target.value)} />
            {mismatch ? (
              <p style={{ color: "var(--color-negative)", fontSize: "var(--text-xs)", margin: "4px 0 0" }}>Passwords don't match.</p>
            ) : null}
          </div>
        </div>
        <div className={ui.formActions} style={{ marginTop: 16 }}>
          <button type="submit" className={ui.btnPrimary} disabled={!currentPassword || !newPassword || mismatch || change.isPending}>
            {change.isPending ? "Changing…" : "Change password"}
          </button>
        </div>
        {change.isError ? (
          <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
            {change.error instanceof ApiError ? change.error.message : "Could not change your password."}
          </p>
        ) : null}
      </form>
    </div>
  );
}

interface BankAccount {
  ID: string;
  Name: string;
  Kind: "BANK" | "CASH";
  AccountNumber: string;
  BankName: string;
  IFSCCode: string;
  IsActive: boolean;
}

/** POST/GET /accounting/bank-accounts had zero frontend callers — every
 * receipt or payment recorded anywhere in the app (RecordReceipt/
 * RecordPayment's own BankAccountID plumbing) silently posted to the
 * Cash ledger account no matter what "method" (UPI, card, bank
 * transfer...) was picked in that form, since nothing ever supplied a
 * real bank_account_id for it to resolve instead. This panel is what
 * makes a real bank account existablE at all; PaymentPanel now lets one
 * be picked once this exists. */
function BankAccountsPanel({ legalEntityId }: { legalEntityId: string }) {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [kind, setKind] = useState<"BANK" | "CASH">("BANK");
  const [accountNumber, setAccountNumber] = useState("");
  const [bankName, setBankName] = useState("");
  const [ifsc, setIfsc] = useState("");

  const accounts = useQuery({
    queryKey: ["bank-accounts"],
    queryFn: () => api.get<BankAccount[]>("/accounting/bank-accounts"),
  });

  const create = useMutation({
    mutationFn: () =>
      api.post("/accounting/bank-accounts", {
        legal_entity_id: legalEntityId,
        name,
        kind,
        account_number: accountNumber,
        bank_name: bankName,
        ifsc_code: ifsc,
        currency_code: "INR",
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["bank-accounts"] });
      setName("");
      setAccountNumber("");
      setBankName("");
      setIfsc("");
      setShowForm(false);
    },
  });

  return (
    <div className={layout.panel}>
      <div className={ui.toolbar}>
        <h2 style={{ margin: 0 }}>Bank accounts</h2>
        <div className={ui.toolbarSpacer} />
        {!showForm ? (
          <button type="button" className={ui.btnPrimary} onClick={() => setShowForm(true)}>
            + New bank account
          </button>
        ) : null}
      </div>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>
        Lets a receipt or payment be tagged to the actual account the money moved through, instead of always assuming cash.
      </p>

      {showForm ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) create.mutate();
          }}
          style={{ marginBottom: 20 }}
        >
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="bank-name">Label</label>
              <input id="bank-name" className={ui.input} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. HDFC Current Account" required />
            </div>
            <div className={ui.field}>
              <label htmlFor="bank-kind">Type</label>
              <select id="bank-kind" className={ui.select} value={kind} onChange={(e) => setKind(e.target.value as "BANK" | "CASH")}>
                <option value="BANK">Bank</option>
                <option value="CASH">Cash drawer</option>
              </select>
            </div>
            {kind === "BANK" ? (
              <>
                <div className={ui.field}>
                  <label htmlFor="bank-institution">Bank name</label>
                  <input id="bank-institution" className={ui.input} value={bankName} onChange={(e) => setBankName(e.target.value)} />
                </div>
                <div className={ui.field}>
                  <label htmlFor="bank-account-number">Account number</label>
                  <input id="bank-account-number" className={ui.input} value={accountNumber} onChange={(e) => setAccountNumber(e.target.value)} />
                </div>
                <div className={ui.field}>
                  <label htmlFor="bank-ifsc">IFSC</label>
                  <input id="bank-ifsc" className={ui.input} value={ifsc} onChange={(e) => setIfsc(e.target.value)} />
                </div>
              </>
            ) : null}
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            <button type="button" className={ui.btnSecondary} onClick={() => setShowForm(false)}>
              Cancel
            </button>
            <button type="submit" className={ui.btnPrimary} disabled={!name.trim() || create.isPending}>
              {create.isPending ? "Adding…" : "Add"}
            </button>
          </div>
          {create.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {create.error instanceof ApiError ? create.error.message : "Could not add this bank account."}
            </p>
          ) : null}
        </form>
      ) : null}

      {accounts.isPending ? (
        <div className={layout.skeleton} style={{ height: 80 }} aria-hidden="true" />
      ) : (accounts.data ?? []).length === 0 ? (
        <p className={layout.emptyState}>No bank accounts yet — receipts and payments will post to Cash by default.</p>
      ) : (
        <div className={ui.tableScroll}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Label</th>
                <th scope="col">Type</th>
                <th scope="col">Bank</th>
                <th scope="col">Account no.</th>
              </tr>
            </thead>
            <tbody>
              {(accounts.data ?? []).map((a) => (
                <tr key={a.ID}>
                  <td>{a.Name}</td>
                  <td>
                    <span className={ui.badge} data-tone={a.Kind === "CASH" ? "neutral" : "positive"}>
                      {a.Kind === "CASH" ? "Cash" : "Bank"}
                    </span>
                  </td>
                  <td>{a.BankName || "—"}</td>
                  <td>{a.AccountNumber || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

/** A single branch's warehouse list plus its own "+ New warehouse" form
 * — nested inside BranchesPanel below, one per branch. Query key
 * ["warehouses", branchId] deliberately matches useOrgContext's own key
 * for the same endpoint, so adding a warehouse here also refreshes
 * whatever screen is using "the" default warehouse via that hook. */
function WarehousesForBranch({ branchId }: { branchId: string }) {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");

  const warehouses = useQuery({
    queryKey: ["warehouses", branchId],
    queryFn: () => api.getListField<Warehouse>(`/branches/${branchId}/warehouses`, "warehouses"),
  });

  const create = useMutation({
    mutationFn: () => api.post("/warehouses", { branch_id: branchId, code, name }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["warehouses", branchId] });
      setCode("");
      setName("");
      setShowForm(false);
    },
  });

  return (
    <div style={{ marginTop: 8, paddingLeft: 16, borderLeft: "2px solid var(--color-border)" }}>
      {warehouses.isPending ? (
        <div className={layout.skeleton} style={{ height: 40 }} aria-hidden="true" />
      ) : (warehouses.data ?? []).length === 0 ? (
        <p className={ui.muted} style={{ margin: "4px 0" }}>
          No warehouses in this branch yet.
        </p>
      ) : (
        <ul style={{ listStyle: "none", margin: "4px 0", padding: 0, display: "flex", flexDirection: "column", gap: 4 }}>
          {(warehouses.data ?? []).map((w) => (
            <li key={w.ID} className={ui.muted}>
              {w.Name} <span style={{ opacity: 0.7 }}>({w.Code})</span>
            </li>
          ))}
        </ul>
      )}
      {showForm ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (code.trim() && name.trim()) create.mutate();
          }}
          style={{ display: "flex", gap: 8, alignItems: "flex-end", marginTop: 8, flexWrap: "wrap" }}
        >
          <div className={ui.field}>
            <label htmlFor={`wh-code-${branchId}`}>Code</label>
            <input id={`wh-code-${branchId}`} className={ui.input} style={{ width: 100 }} value={code} onChange={(e) => setCode(e.target.value)} required />
          </div>
          <div className={ui.field}>
            <label htmlFor={`wh-name-${branchId}`}>Name</label>
            <input id={`wh-name-${branchId}`} className={ui.input} value={name} onChange={(e) => setName(e.target.value)} required />
          </div>
          <button type="submit" className={ui.btnPrimary} disabled={!code.trim() || !name.trim() || create.isPending}>
            {create.isPending ? "Adding…" : "Add"}
          </button>
          <button type="button" className={ui.btnSecondary} onClick={() => setShowForm(false)}>
            Cancel
          </button>
          {create.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", width: "100%", margin: 0 }}>
              {create.error instanceof ApiError ? create.error.message : "Could not add this warehouse."}
            </p>
          ) : null}
        </form>
      ) : (
        <button type="button" className={ui.btnGhost} style={{ marginTop: 4 }} onClick={() => setShowForm(true)}>
          + New warehouse
        </button>
      )}
    </div>
  );
}

/** Branches & Warehouses — internal/modules/organisation's Create/List
 * Branch/Warehouse endpoints existed with zero frontend callers.
 * useOrgContext.ts's own doc comment already explains why almost every
 * screen just resolves "the" first branch/warehouse rather than making
 * every document-creation screen pick one — that stays true here; this
 * panel only lets a genuinely multi-branch business SEE and ADD its
 * branches/warehouses, not choose which one a given sale posts against
 * (a separate, larger scope). Scoped to one legal entity (org.legalEntity)
 * — same assumption BankAccountsPanel above already makes. */
function BranchesPanel({ legalEntityId }: { legalEntityId: string }) {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [timezone, setTimezone] = useState("Asia/Kolkata");

  const branches = useQuery({
    queryKey: ["branches"],
    queryFn: () => api.getListField<Branch>("/branches", "branches"),
  });

  const create = useMutation({
    mutationFn: () => api.post("/branches", { legal_entity_id: legalEntityId, code, name, timezone }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["branches"] });
      setCode("");
      setName("");
      setShowForm(false);
    },
  });

  return (
    <div className={layout.panel}>
      <div className={ui.toolbar}>
        <h2 style={{ margin: 0 }}>Branches &amp; warehouses</h2>
        <div className={ui.toolbarSpacer} />
        {!showForm ? (
          <button type="button" className={ui.btnPrimary} onClick={() => setShowForm(true)}>
            + New branch
          </button>
        ) : null}
      </div>
      <p className={layout.subtitle} style={{ marginBottom: 16 }}>
        Most businesses only need one of each — add more here if you run multiple locations or stock points.
      </p>

      {showForm ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (code.trim() && name.trim()) create.mutate();
          }}
          style={{ marginBottom: 20 }}
        >
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="branch-code">Code</label>
              <input id="branch-code" className={ui.input} value={code} onChange={(e) => setCode(e.target.value)} placeholder="e.g. BLR" required />
            </div>
            <div className={ui.field}>
              <label htmlFor="branch-name">Name</label>
              <input id="branch-name" className={ui.input} value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Bengaluru store" required />
            </div>
            <div className={ui.field}>
              <label htmlFor="branch-timezone">Timezone</label>
              <input id="branch-timezone" className={ui.input} value={timezone} onChange={(e) => setTimezone(e.target.value)} placeholder="e.g. Asia/Kolkata" />
            </div>
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            <button type="button" className={ui.btnSecondary} onClick={() => setShowForm(false)}>
              Cancel
            </button>
            <button type="submit" className={ui.btnPrimary} disabled={!code.trim() || !name.trim() || create.isPending}>
              {create.isPending ? "Adding…" : "Add"}
            </button>
          </div>
          {create.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {create.error instanceof ApiError ? create.error.message : "Could not add this branch."}
            </p>
          ) : null}
        </form>
      ) : null}

      {branches.isPending ? (
        <div className={layout.skeleton} style={{ height: 80 }} aria-hidden="true" />
      ) : (branches.data ?? []).length === 0 ? (
        <p className={layout.emptyState}>No branches yet.</p>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
          {(branches.data ?? []).map((b) => (
            <div key={b.ID}>
              <strong>{b.Name}</strong> <span className={ui.muted}>({b.Code})</span>
              <WarehousesForBranch branchId={b.ID} />
            </div>
          ))}
        </div>
      )}
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

      {org.legalEntity ? <BranchesPanel legalEntityId={org.legalEntity.ID} /> : null}

      {org.legalEntity ? <BankAccountsPanel legalEntityId={org.legalEntity.ID} /> : null}

      <ScanningPanel />

      <ChangePasswordPanel />

      <TeamPanel />
    </div>
  );
}
