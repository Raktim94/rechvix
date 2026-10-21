import { useQuery } from "@tanstack/react-query";
import { api } from "./api-client";

export interface Country {
  code: string;
  name: string;
  default_currency?: string;
  eu_member: boolean;
  tax_pack: boolean;
}
export interface Currency {
  code: string;
  name: string;
  numeric: string;
  minor_digits: number;
}
export interface TaxCategory {
  code: string;
  rate?: string;
  kind: "STANDARD" | "REDUCED" | "ZERO" | "EXEMPT" | "TAXABLE";
}
export interface TaxReference {
  country: string;
  supported: boolean;
  rate_pack_version: string;
  effective_from: string;
  categories: TaxCategory[] | null;
  state_base_rates?: Record<string, string>;
  note?: string;
}

// Reference data changes with a release, not between requests.
const STATIC = { staleTime: Infinity, gcTime: Infinity } as const;

export function useCountries() {
  return useQuery({ queryKey: ["reference", "countries"], queryFn: () => api.get<Country[]>("/reference/countries"), ...STATIC });
}
export function useCurrencies() {
  return useQuery({ queryKey: ["reference", "currencies"], queryFn: () => api.get<Currency[]>("/reference/currencies"), ...STATIC });
}
export function useTaxReference(country: string | undefined) {
  return useQuery({
    queryKey: ["reference", "tax", country],
    queryFn: () => api.get<TaxReference>(`/reference/tax?country=${encodeURIComponent(country ?? "")}`),
    enabled: !!country,
    ...STATIC,
  });
}
