import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { GST_STATE_CODES } from "../../lib/gstStateCodes";
import { useOrgContext } from "../../lib/useOrgContext";
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
