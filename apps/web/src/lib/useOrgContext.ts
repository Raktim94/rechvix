import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "./api-client";

/** Mirrors internal/modules/organisation/domain.LegalEntity/Branch/Warehouse
 * (no json tags, so Go's exported field names serialize verbatim). */
export interface LegalEntity {
  ID: string;
  LegalName: string;
  GSTIN: string;
  GSTStateCode: string;
  // Invoice branding (migrations/0034) — see
  // internal/modules/organisation/domain.LegalEntity's own comment.
  Phone: string;
  Email: string;
  Website: string;
  Address: string;
  BankName: string;
  BankAccountNumber: string;
  BankIFSC: string;
  UPIID: string;
  AuthorizedSignatoryName: string;
  DefaultTermsAndConditions: string;
  /** Go's encoding/json marshals a []byte field as a base64 string — this
   * is that string, empty/absent when no logo is set, ready to drop
   * straight into `data:image/png;base64,${LogoPNG}`. */
  LogoPNG: string | null;
  UpdatedAt: string;
}
export interface Branch {
  ID: string;
  LegalEntityID: string;
  Code: string;
  Name: string;
}
export interface Warehouse {
  ID: string;
  BranchID: string;
  Code: string;
  Name: string;
}
export interface Organisation {
  ID: string;
  Name: string;
  DefaultCurrencyCode: string;
  EWayBillMode: "FREE_PORTAL" | "AUTOMATIC_API";
  EWayBillThresholdOverride: string | null;
}

/** Which company (legal entity) is active is a per-browser UI
 * preference, not server state — same convention as the theme toggle.
 * Kept outside React state too, as a plain module-level fallback read
 * once at hook-init, so every component calling useOrgContext in the
 * same render agrees on the initially-selected id before any effect has
 * had a chance to run. */
const SELECTED_COMPANY_STORAGE_KEY = "rechvix:selected-company";

function readStoredCompanyId(): string | null {
  try {
    return localStorage.getItem(SELECTED_COMPANY_STORAGE_KEY);
  } catch {
    return null;
  }
}

function writeStoredCompanyId(id: string) {
  try {
    localStorage.setItem(SELECTED_COMPANY_STORAGE_KEY, id);
  } catch {
    // Private browsing / storage disabled — the selection just won't
    // survive a reload, same degraded-but-working fallback the theme
    // toggle already accepts.
  }
}

/** Every screen that creates or lists a document (Sales, Purchases,
 * Inventory, GST, Reports, ...) needs a legal entity ("company") /
 * branch / warehouse to post against or filter by. Most self-hosted
 * installs have exactly one company, in which case this hook silently
 * always resolves it and nothing below is ever visible — but a business
 * with more than one (each with its own GSTIN) picks the active one via
 * the company switcher in AppShell's topbar, which calls selectCompany.
 * The selection is a plain id persisted in localStorage — every other
 * hook/query that needs "the active company's id" reads
 * legalEntity?.ID from here and includes it in its own query key, so
 * switching companies naturally refetches everything company-scoped
 * without this hook needing to know what depends on it.
 */
export function useOrgContext() {
  const organisation = useQuery({
    queryKey: ["organisation"],
    queryFn: () => api.get<Organisation>("/organisation"),
  });
  const legalEntities = useQuery({
    queryKey: ["legal-entities"],
    queryFn: () => api.getListField<LegalEntity>("/legal-entities", "legal_entities"),
  });
  const branches = useQuery({
    queryKey: ["branches"],
    queryFn: () => api.getListField<Branch>("/branches", "branches"),
  });

  const [selectedId, setSelectedId] = useState<string | null>(readStoredCompanyId);

  function selectCompany(id: string) {
    setSelectedId(id);
    writeStoredCompanyId(id);
  }

  // The stored id if it still names a company this user can see (an
  // employee's company access can shrink, or the stored id could be
  // stale from a different account on a shared browser), else just the
  // first one — same "there's almost always exactly one" fallback the
  // single-company version of this hook always used.
  const legalEntity = legalEntities.data?.find((le) => le.ID === selectedId) ?? legalEntities.data?.[0];
  const branch = branches.data?.find((b) => b.LegalEntityID === legalEntity?.ID);
  const warehouses = useQuery({
    queryKey: ["warehouses", branch?.ID],
    queryFn: () => api.getListField<Warehouse>(`/branches/${branch?.ID}/warehouses`, "warehouses"),
    enabled: !!branch,
  });

  const isPending = organisation.isPending || legalEntities.isPending || branches.isPending || (!!branch && warehouses.isPending);
  const isError = organisation.isError || legalEntities.isError || branches.isError || warehouses.isError;

  return {
    isPending,
    isError,
    organisation: organisation.data,
    legalEntities: legalEntities.data,
    legalEntity,
    branch,
    warehouse: warehouses.data?.[0],
    selectCompany,
  };
}
