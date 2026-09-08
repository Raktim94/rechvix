import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { GST_STATE_CODES } from "../../lib/gstStateCodes";
import { formatMoney, type Money } from "../../lib/money";
import type { Party } from "../../lib/partyTypes";
import layout from "../DashboardPage.module.css";

/** Mirrors internal/modules/contacts/domain.Address (no json tags). */
interface Address {
  ID: string;
  AddressType: "BILLING" | "SHIPPING" | "WAREHOUSE" | "REGISTERED_OFFICE";
  Line1: string;
  Line2: string;
  City: string;
  State: string;
  PostalCode: string;
  CountryCode: string;
  IsDefault: boolean;
}
/** Mirrors internal/modules/contacts/domain.TaxRegistration. */
interface TaxRegistration {
  ID: string;
  RegistrationNumber: string;
  StateCode: string;
  IsPrimary: boolean;
}
/** Mirrors internal/modules/accounting/domain.LedgerEntry — GetPartyLedger
 * returns a raw array, not a {key: [...]}-wrapped object, so this uses a
 * plain api.get, not getListField. */
interface LedgerEntry {
  JournalID: string;
  JournalDate: string;
  Description: string;
  Debit: Money;
  Credit: Money;
  RunningBalance: Money;
}
/** Mirrors internal/modules/accounting/domain.AgeingBucket. */
interface AgeingBucket {
  Current: Money;
  Days1To30: Money;
  Days31To60: Money;
  Days61To90: Money;
  Days90Plus: Money;
  Total: Money;
}

const ADDRESS_TYPE_LABELS: Record<Address["AddressType"], string> = {
  BILLING: "Billing",
  SHIPPING: "Shipping",
  WAREHOUSE: "Warehouse",
  REGISTERED_OFFICE: "Registered office",
};

function TaxRegistrationsPanel({ partyId }: { partyId: string }) {
  const queryClient = useQueryClient();
  const [stateCode, setStateCode] = useState("");
  const [number, setNumber] = useState("");

  const regs = useQuery({
    queryKey: ["party-tax-registrations", partyId],
    queryFn: () => api.getListField<TaxRegistration>(`/contacts/parties/${partyId}/tax-registrations`, "tax_registrations"),
  });

  const add = useMutation({
    mutationFn: () =>
      api.post(`/contacts/parties/${partyId}/tax-registrations`, {
        country_code: "IN",
        registration_number: number,
        state_code: stateCode,
        is_primary: (regs.data?.length ?? 0) === 0,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["party-tax-registrations", partyId] });
      setStateCode("");
      setNumber("");
    },
  });

  return (
    <div className={layout.panel}>
      <h2>GST / tax registration</h2>
      {regs.data?.length ? (
        <div className={ui.tableScroll} style={{ marginTop: 8, marginBottom: 16 }}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">GSTIN</th>
                <th scope="col">State</th>
                <th scope="col"></th>
              </tr>
            </thead>
            <tbody>
              {regs.data.map((r) => (
                <tr key={r.ID}>
                  <td>{r.RegistrationNumber}</td>
                  <td>{r.StateCode}</td>
                  <td>{r.IsPrimary ? <span className={ui.badge} data-tone="positive">primary</span> : null}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className={layout.emptyState} style={{ marginBottom: 16 }}>
          No GSTIN on file — add one below to place this contact's state for GST purposes.
        </p>
      )}
      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="party-tax-state">State</label>
          <select id="party-tax-state" className={ui.select} value={stateCode} onChange={(e) => setStateCode(e.target.value)}>
            <option value="" disabled>
              Select a state…
            </option>
            {GST_STATE_CODES.map((s) => (
              <option key={s.code} value={s.code}>
                {s.name}
              </option>
            ))}
          </select>
        </div>
        <div className={ui.field}>
          <label htmlFor="party-tax-number">GSTIN</label>
          <input id="party-tax-number" className={ui.input} value={number} onChange={(e) => setNumber(e.target.value)} />
        </div>
        <button type="button" className={ui.btnPrimary} disabled={!stateCode || !number || add.isPending} onClick={() => add.mutate()}>
          {add.isPending ? "Adding…" : "Add GSTIN"}
        </button>
      </div>
      {add.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {add.error instanceof ApiError ? add.error.message : "Could not add this registration."}
        </p>
      ) : null}
    </div>
  );
}

function AddressesPanel({ partyId }: { partyId: string }) {
  const queryClient = useQueryClient();
  const [type, setType] = useState<Address["AddressType"]>("BILLING");
  const [line1, setLine1] = useState("");
  const [line2, setLine2] = useState("");
  const [city, setCity] = useState("");
  const [state, setState] = useState("");
  const [postalCode, setPostalCode] = useState("");

  const addresses = useQuery({
    queryKey: ["party-addresses", partyId],
    queryFn: () => api.getListField<Address>(`/contacts/parties/${partyId}/addresses`, "addresses"),
  });

  const add = useMutation({
    mutationFn: () =>
      api.post(`/contacts/parties/${partyId}/addresses`, {
        address_type: type,
        line1,
        line2,
        city,
        state,
        postal_code: postalCode,
        country_code: "IN",
        is_default: (addresses.data?.length ?? 0) === 0,
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["party-addresses", partyId] });
      setLine1("");
      setLine2("");
      setCity("");
      setState("");
      setPostalCode("");
    },
  });

  return (
    <div className={layout.panel}>
      <h2>Addresses</h2>
      {addresses.data?.length ? (
        <div className={ui.tableScroll} style={{ marginTop: 8, marginBottom: 16 }}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Type</th>
                <th scope="col">Address</th>
                <th scope="col"></th>
              </tr>
            </thead>
            <tbody>
              {addresses.data.map((a) => (
                <tr key={a.ID}>
                  <td>{ADDRESS_TYPE_LABELS[a.AddressType]}</td>
                  <td>{[a.Line1, a.Line2, a.City, a.State, a.PostalCode].filter(Boolean).join(", ")}</td>
                  <td>{a.IsDefault ? <span className={ui.badge} data-tone="positive">default</span> : null}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className={layout.emptyState} style={{ marginBottom: 16 }}>
          No addresses saved yet.
        </p>
      )}
      <div className={ui.formGrid}>
        <div className={ui.field}>
          <label htmlFor="addr-type">Type</label>
          <select id="addr-type" className={ui.select} value={type} onChange={(e) => setType(e.target.value as Address["AddressType"])}>
            {(Object.keys(ADDRESS_TYPE_LABELS) as Address["AddressType"][]).map((t) => (
              <option key={t} value={t}>
                {ADDRESS_TYPE_LABELS[t]}
              </option>
            ))}
          </select>
        </div>
        <div className={ui.field}>
          <label htmlFor="addr-line1">Address line 1</label>
          <input id="addr-line1" className={ui.input} value={line1} onChange={(e) => setLine1(e.target.value)} />
        </div>
        <div className={ui.field}>
          <label htmlFor="addr-line2">Address line 2</label>
          <input id="addr-line2" className={ui.input} value={line2} onChange={(e) => setLine2(e.target.value)} />
        </div>
        <div className={ui.field}>
          <label htmlFor="addr-city">City</label>
          <input id="addr-city" className={ui.input} value={city} onChange={(e) => setCity(e.target.value)} />
        </div>
        <div className={ui.field}>
          <label htmlFor="addr-state">State</label>
          <input id="addr-state" className={ui.input} value={state} onChange={(e) => setState(e.target.value)} />
        </div>
        <div className={ui.field}>
          <label htmlFor="addr-postal">PIN code</label>
          <input id="addr-postal" className={ui.input} value={postalCode} onChange={(e) => setPostalCode(e.target.value)} />
        </div>
      </div>
      <div className={ui.formActions} style={{ marginTop: 12 }}>
        <button type="button" className={ui.btnPrimary} disabled={!line1 || !city || add.isPending} onClick={() => add.mutate()}>
          {add.isPending ? "Adding…" : "Add address"}
        </button>
      </div>
      {add.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {add.error instanceof ApiError ? add.error.message : "Could not add this address."}
        </p>
      ) : null}
    </div>
  );
}

/** Records a receipt (money IN, from a customer) or a payment (money OUT,
 * to a supplier) against this party — internal/modules/accounting's
 * POST /accounting/receipts / POST /accounting/payments, fully built and
 * tested server-side (Stage 6) but never reachable from any screen until
 * now. A BOTH-type party can go either direction; CUSTOMER/SUPPLIER only
 * offer the one direction that makes sense for them. */
function RecordPaymentPanel({ party }: { party: Party }) {
  const queryClient = useQueryClient();
  const canReceive = party.PartyType === "CUSTOMER" || party.PartyType === "BOTH";
  const canPay = party.PartyType === "SUPPLIER" || party.PartyType === "BOTH";
  const [direction, setDirection] = useState<"RECEIVE" | "PAY">(canReceive ? "RECEIVE" : "PAY");
  const [amount, setAmount] = useState("");
  const [method, setMethod] = useState("CASH");
  const [reference, setReference] = useState("");
  // Without a real bank_account_id, RecordReceipt/RecordPayment both
  // default to the Cash ledger account regardless of Method — same gap
  // components/PaymentPanel.tsx was fixed for; this party-level form
  // (no specific invoice/bill to attach to, so PaymentPanel itself
  // doesn't fit here) needed the identical fix.
  const [bankAccountId, setBankAccountId] = useState("");

  const bankAccounts = useQuery({
    queryKey: ["bank-accounts"],
    queryFn: () => api.get<{ ID: string; Name: string; Kind: string; IsActive: boolean }[]>("/accounting/bank-accounts"),
  });
  const activeBankAccounts = (bankAccounts.data ?? []).filter((a) => a.IsActive && a.Kind === "BANK");

  const record = useMutation({
    mutationFn: () => {
      const path = direction === "RECEIVE" ? "/accounting/receipts" : "/accounting/payments";
      return api.post(path, {
        party_id: party.ID,
        amount,
        method,
        reference_number: reference,
        bank_account_id: bankAccountId || undefined,
      });
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["party-ledger", party.ID] });
      void queryClient.invalidateQueries({ queryKey: ["party-ageing", party.ID] });
      setAmount("");
      setReference("");
      setBankAccountId("");
    },
  });

  if (!canReceive && !canPay) return null;

  return (
    <div className={layout.panel}>
      <h2>Record a payment</h2>
      <div className={ui.formGrid}>
        {canReceive && canPay ? (
          <div className={ui.field}>
            <label htmlFor="pay-direction">Direction</label>
            <select id="pay-direction" className={ui.select} value={direction} onChange={(e) => setDirection(e.target.value as "RECEIVE" | "PAY")}>
              <option value="RECEIVE">Received from them</option>
              <option value="PAY">Paid to them</option>
            </select>
          </div>
        ) : null}
        <div className={ui.field}>
          <label htmlFor="pay-amount">Amount (₹)</label>
          <input id="pay-amount" className={ui.input} value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="0.00" />
        </div>
        <div className={ui.field}>
          <label htmlFor="pay-method">Method</label>
          <select id="pay-method" className={ui.select} value={method} onChange={(e) => setMethod(e.target.value)}>
            <option value="CASH">Cash</option>
            <option value="UPI">UPI</option>
            <option value="CARD">Card</option>
            <option value="BANK_TRANSFER">Bank transfer</option>
            <option value="CHEQUE">Cheque</option>
            <option value="OTHER">Other</option>
          </select>
        </div>
        <div className={ui.field}>
          <label htmlFor="pay-reference">Reference (optional)</label>
          <input id="pay-reference" className={ui.input} value={reference} onChange={(e) => setReference(e.target.value)} placeholder="UTR / cheque no. / note" />
        </div>
        {activeBankAccounts.length > 0 ? (
          <div className={ui.field}>
            <label htmlFor="pay-bank-account">{direction === "RECEIVE" ? "Deposited to" : "Paid from"}</label>
            <select id="pay-bank-account" className={ui.select} value={bankAccountId} onChange={(e) => setBankAccountId(e.target.value)}>
              <option value="">Cash</option>
              {activeBankAccounts.map((a) => (
                <option key={a.ID} value={a.ID}>
                  {a.Name}
                </option>
              ))}
            </select>
          </div>
        ) : null}
      </div>
      <div className={ui.formActions} style={{ marginTop: 12 }}>
        <button type="button" className={ui.btnPrimary} disabled={!amount || record.isPending} onClick={() => record.mutate()}>
          {record.isPending ? "Recording…" : direction === "RECEIVE" ? "Record receipt" : "Record payment"}
        </button>
      </div>
      {record.isError ? (
        <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
          {record.error instanceof ApiError ? record.error.message : "Could not record this."}
        </p>
      ) : null}
      {record.isSuccess ? <p style={{ color: "var(--color-positive)", marginTop: 8 }}>Recorded.</p> : null}
    </div>
  );
}

function LedgerPanel({ partyId }: { partyId: string }) {
  const ledger = useQuery({
    queryKey: ["party-ledger", partyId],
    queryFn: () => api.get<LedgerEntry[] | null>(`/accounting/parties/${partyId}/ledger`),
  });
  const ageing = useQuery({
    queryKey: ["party-ageing", partyId],
    queryFn: () => api.get<AgeingBucket>(`/accounting/parties/${partyId}/ageing`),
  });
  const entries = ledger.data ?? [];

  return (
    <div className={layout.panel}>
      <h2>Ledger</h2>
      {ageing.data ? (
        <div className={ui.formGrid} style={{ marginBottom: 16 }}>
          <div>
            <span className={ui.muted}>Current</span>
            <div className="num">{formatMoney(ageing.data.Current)}</div>
          </div>
          <div>
            <span className={ui.muted}>1–30 days</span>
            <div className="num">{formatMoney(ageing.data.Days1To30)}</div>
          </div>
          <div>
            <span className={ui.muted}>31–60 days</span>
            <div className="num">{formatMoney(ageing.data.Days31To60)}</div>
          </div>
          <div>
            <span className={ui.muted}>61–90 days</span>
            <div className="num">{formatMoney(ageing.data.Days61To90)}</div>
          </div>
          <div>
            <span className={ui.muted}>90+ days</span>
            <div className="num">{formatMoney(ageing.data.Days90Plus)}</div>
          </div>
          <div>
            <strong className={ui.muted}>Total outstanding</strong>
            <div className="num">
              <strong>{formatMoney(ageing.data.Total)}</strong>
            </div>
          </div>
        </div>
      ) : null}

      {ledger.isPending ? (
        <div className={layout.skeleton} style={{ height: 160 }} aria-hidden="true" />
      ) : ledger.isError ? (
        <p className={layout.errorState} role="alert">
          Couldn't load this ledger.
        </p>
      ) : entries.length === 0 ? (
        <p className={layout.emptyState}>No transactions yet.</p>
      ) : (
        <div className={ui.tableScroll}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th scope="col">Date</th>
                <th scope="col">Description</th>
                <th scope="col">Debit</th>
                <th scope="col">Credit</th>
                <th scope="col">Balance</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e, i) => (
                <tr key={`${e.JournalID}-${i}`}>
                  <td>{new Date(e.JournalDate).toLocaleDateString()}</td>
                  <td>{e.Description}</td>
                  <td className="num">{formatMoney(e.Debit)}</td>
                  <td className="num">{formatMoney(e.Credit)}</td>
                  <td className="num">{formatMoney(e.RunningBalance)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

export function ContactDetailPage({ id }: { id: string }) {
  const party = useQuery({
    queryKey: ["party", id],
    queryFn: () => api.get<Party>(`/contacts/parties/${id}`),
  });

  if (party.isPending) {
    return (
      <div className={layout.page}>
        <div className={layout.skeleton} style={{ height: 240 }} aria-hidden="true" />
      </div>
    );
  }
  if (party.isError || !party.data) {
    return (
      <div className={layout.page}>
        <p className={layout.errorState} role="alert">
          Couldn't load this contact.
        </p>
      </div>
    );
  }
  const p = party.data;

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>{p.LegalName}</h1>
          <p className={layout.subtitle}>
            <span className={ui.badge} data-tone="neutral">
              {p.PartyType === "BOTH" ? "Customer & supplier" : p.PartyType === "CUSTOMER" ? "Customer" : "Supplier"}
            </span>
            {p.Phone ? ` · ${p.Phone}` : ""}
            {p.Email ? ` · ${p.Email}` : ""}
          </p>
        </div>
        <Link to="/contacts" className={ui.btnSecondary}>
          Back to contacts
        </Link>
      </div>

      <LedgerPanel partyId={p.ID} />
      <RecordPaymentPanel party={p} />
      <TaxRegistrationsPanel partyId={p.ID} />
      <AddressesPanel partyId={p.ID} />
    </div>
  );
}
