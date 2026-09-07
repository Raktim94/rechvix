import { useQuery } from "@tanstack/react-query";
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
}

/** Every screen that creates a document (Sales, Purchases, ...) needs a
 * legal entity / branch / warehouse to post against. Almost every
 * self-hosted install of this size has exactly one of each (single-branch
 * small business) — this hook resolves "the" one, so screens don't each
 * re-implement a picker for something that, for the overwhelming majority
 * of installs, is not actually a choice. A business that genuinely has
 * multiple branches/warehouses can still see and change them via
 * Settings (existing legal-entities/branches/warehouses endpoints); nothing
 * here prevents that, it just isn't this pass's UI.
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
  const firstBranch = branches.data?.[0];
  const warehouses = useQuery({
    queryKey: ["warehouses", firstBranch?.ID],
    queryFn: () => api.getListField<Warehouse>(`/branches/${firstBranch?.ID}/warehouses`, "warehouses"),
    enabled: !!firstBranch,
  });

  const isPending = organisation.isPending || legalEntities.isPending || branches.isPending || warehouses.isPending;
  const isError = organisation.isError || legalEntities.isError || branches.isError || warehouses.isError;

  return {
    isPending,
    isError,
    organisation: organisation.data,
    legalEntity: legalEntities.data?.[0],
    branch: firstBranch,
    warehouse: warehouses.data?.[0],
  };
}
