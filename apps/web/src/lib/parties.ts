import { api } from "./api-client";
import type { Party, PartyType } from "./partyTypes";

export interface NewPartyDetails {
  partyType: PartyType;
  legalName: string;
  phone?: string;
  currencyCode: string;
  gstin?: string;
  addressLine1?: string;
  city?: string;
  state?: string;
  postalCode?: string;
}

/** The three-call sequence (party, then optionally a tax registration
 * and an address — contacts/httpapi has no single combined endpoint)
 * shared by QuickAddPartyModal and PurchaseScanReviewModal's "create new
 * distributor" path, so both do the exact same thing rather than two
 * slightly-different reimplementations. Best-effort on the optional
 * calls: a bad GSTIN/address shouldn't undo the party that already
 * succeeded, since there's no cross-resource transaction here. */
export async function createPartyWithDetails(details: NewPartyDetails): Promise<Party> {
  const party = await api.post<Party>("/contacts/parties", {
    party_type: details.partyType,
    legal_name: details.legalName.trim(),
    trade_name: "",
    phone: (details.phone ?? "").trim(),
    email: "",
    currency_code: details.currencyCode,
  });

  const trimmedGstin = (details.gstin ?? "").trim().toUpperCase();
  if (trimmedGstin) {
    await api.post(`/contacts/parties/${party.ID}/tax-registrations`, {
      country_code: "IN",
      registration_number: trimmedGstin,
      state_code: trimmedGstin.slice(0, 2),
      is_primary: true,
    });
  }

  const line1 = (details.addressLine1 ?? "").trim();
  const city = (details.city ?? "").trim();
  const state = (details.state ?? "").trim();
  const postalCode = (details.postalCode ?? "").trim();
  if (line1 || city || state || postalCode) {
    await api.post(`/contacts/parties/${party.ID}/addresses`, {
      address_type: details.partyType === "SUPPLIER" ? "REGISTERED_OFFICE" : "BILLING",
      line1,
      line2: "",
      city,
      state,
      postal_code: postalCode,
      country_code: "IN",
      is_default: true,
    });
  }

  return party;
}
