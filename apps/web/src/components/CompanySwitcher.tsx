import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import styles from "./AppShell.module.css";
import { BuildingIcon, CheckIcon, ChevronDownIcon, PlusIcon } from "./icons";
import modal from "./Modal.module.css";
import ui from "./ui.module.css";
import { api, ApiError } from "../lib/api-client";
import { GST_STATE_CODES } from "../lib/gstStateCodes";
import { useOrgContext } from "../lib/useOrgContext";

/** Same auto-slug idiom BootstrapPage/CataloguePage already use for an
 * unset code field — branch_code/warehouse_code are NOT NULL (and
 * warehouses.code is UNIQUE per organisation), but this form only
 * collects names, same reasoning as BootstrapPage's identical helper. */
function slugCode(name: string, maxLen: number): string {
  return name
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, maxLen);
}

interface LegalEntityResponse {
  ID: string;
}
interface BranchResponse {
  ID: string;
}

/** Legal entity + a default branch + a default warehouse, in one
 * sequential POST /legal-entities -> POST /branches -> POST /warehouses
 * — the same three calls Settings' own BranchesPanel/WarehousesPanel
 * already make individually, just chained here so "add a second
 * company" is one form instead of three separate screens. No new
 * backend endpoint — every call already existed. */
function NewCompanyModal({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (open: boolean) => void; onCreated: (legalEntityId: string) => void }) {
  const queryClient = useQueryClient();
  const [legalName, setLegalName] = useState("");
  const [stateCode, setStateCode] = useState("");
  const [gstin, setGstin] = useState("");
  const [branchName, setBranchName] = useState("Head Office");
  const [warehouseName, setWarehouseName] = useState("Main Warehouse");

  useEffect(() => {
    if (!open) return;
    setLegalName("");
    setStateCode("");
    setGstin("");
    setBranchName("Head Office");
    setWarehouseName("Main Warehouse");
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
    mutationFn: async () => {
      const legalEntity = await api.post<LegalEntityResponse>("/legal-entities", {
        legal_name: legalName,
        country_code: "IN",
        base_currency_code: "INR",
        gst_state_code: stateCode,
        gstin: gstin || undefined,
      });
      const branch = await api.post<BranchResponse>("/branches", {
        legal_entity_id: legalEntity.ID,
        code: slugCode(branchName, 16) || "BR1",
        name: branchName,
      });
      await api.post("/warehouses", {
        branch_id: branch.ID,
        code: slugCode(warehouseName, 16) || "WH1",
        name: warehouseName,
      });
      return legalEntity.ID;
    },
    onSuccess: async (legalEntityId) => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["legal-entities"] }),
        queryClient.invalidateQueries({ queryKey: ["branches"] }),
      ]);
      onCreated(legalEntityId);
      onOpenChange(false);
    },
  });

  if (!open) return null;

  return (
    <div className={modal.overlay} onClick={() => onOpenChange(false)}>
      <div className={modal.dialog} role="dialog" aria-modal="true" aria-label="New company" onClick={(e) => e.stopPropagation()}>
        <div className={modal.header}>
          <h2>New company</h2>
          <button type="button" className={modal.closeButton} aria-label="Close" onClick={() => onOpenChange(false)}>
            ×
          </button>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (legalName.trim() && stateCode) create.mutate();
          }}
        >
          <div className={modal.body}>
            <div className={ui.field}>
              <label htmlFor="new-company-name">Legal name*</label>
              <input id="new-company-name" className={ui.input} autoFocus value={legalName} onChange={(e) => setLegalName(e.target.value)} required />
            </div>
            <div className={ui.field}>
              <label htmlFor="new-company-state">Business state*</label>
              <select id="new-company-state" className={ui.select} value={stateCode} onChange={(e) => setStateCode(e.target.value)} required>
                <option value="" disabled>
                  Select a state…
                </option>
                {GST_STATE_CODES.map((s) => (
                  <option key={s.code} value={s.code}>
                    {s.name}
                  </option>
                ))}
              </select>
              <p className={ui.muted} style={{ marginTop: 4 }}>
                Determines intra- vs. inter-state tax on this company's invoices — required even if not GST-registered.
              </p>
            </div>
            <div className={ui.field}>
              <label htmlFor="new-company-gstin">GSTIN (optional)</label>
              <input id="new-company-gstin" className={ui.input} placeholder="Leave blank if not GST-registered" value={gstin} onChange={(e) => setGstin(e.target.value)} maxLength={15} />
            </div>
            <div className={ui.formGrid}>
              <div className={ui.field}>
                <label htmlFor="new-company-branch">First branch name</label>
                <input id="new-company-branch" className={ui.input} value={branchName} onChange={(e) => setBranchName(e.target.value)} />
              </div>
              <div className={ui.field}>
                <label htmlFor="new-company-warehouse">First warehouse name</label>
                <input id="new-company-warehouse" className={ui.input} value={warehouseName} onChange={(e) => setWarehouseName(e.target.value)} />
              </div>
            </div>
            <p className={ui.muted} style={{ margin: 0 }}>
              You can rename these, or add more branches/warehouses, later in Settings.
            </p>
            {create.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)", margin: 0 }}>
                {create.error instanceof ApiError ? create.error.message : "Could not create this company."}
              </p>
            ) : null}
          </div>
          <div className={modal.footer}>
            <button type="button" className={ui.btnSecondary} onClick={() => onOpenChange(false)}>
              Cancel
            </button>
            <button type="submit" className={ui.btnPrimary} disabled={!legalName.trim() || !stateCode || create.isPending}>
              {create.isPending ? "Creating…" : "Create company"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}

/** Topbar company switcher — invisible in effect for the overwhelming
 * majority of installs (exactly one legal entity, so this is just a
 * label), and the only place a second company (its own GSTIN, its own
 * Sales/Purchases/Inventory/GST data — see useOrgContext's own doc
 * comment) gets created or made active. */
export function CompanySwitcher() {
  const org = useOrgContext();
  const [open, setOpen] = useState(false);
  const [modalOpen, setModalOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open]);

  if (org.isPending || !org.legalEntity) return null;

  return (
    <>
      <div className={styles.userMenu} ref={ref}>
        <button
          type="button"
          className={styles.companyButton}
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
          aria-haspopup="menu"
          aria-label="Switch company"
          title={org.legalEntity.LegalName}
        >
          <BuildingIcon className={styles.companyButtonIcon} />
          <span className={styles.companyButtonLabel}>{org.legalEntity.LegalName}</span>
          <ChevronDownIcon className={styles.companyButtonChevron} />
        </button>
        {open ? (
          <div className={styles.userDropdown} style={{ minWidth: 260 }} role="menu">
            {(org.legalEntities ?? []).map((le) => (
              <button
                key={le.ID}
                type="button"
                role="menuitem"
                className={styles.companyOption}
                onClick={() => {
                  org.selectCompany(le.ID);
                  setOpen(false);
                }}
              >
                <span>{le.LegalName}</span>
                {le.ID === org.legalEntity?.ID ? <CheckIcon className={styles.companyOptionCheck} /> : null}
              </button>
            ))}
            <div className={styles.companyDropdownDivider} />
            <button
              type="button"
              role="menuitem"
              className={styles.companyOption}
              onClick={() => {
                setModalOpen(true);
                setOpen(false);
              }}
            >
              <PlusIcon /> New company
            </button>
          </div>
        ) : null}
      </div>
      <NewCompanyModal open={modalOpen} onOpenChange={setModalOpen} onCreated={(id) => org.selectCompany(id)} />
    </>
  );
}
