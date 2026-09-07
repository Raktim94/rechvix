import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import type { Party, PartyType } from "../../lib/partyTypes";
import layout from "../DashboardPage.module.css";

type TypeFilter = "ALL" | PartyType;

export function ContactsPage() {
  const queryClient = useQueryClient();
  const [query, setQuery] = useState("");
  const [typeFilter, setTypeFilter] = useState<TypeFilter>("ALL");
  const [showForm, setShowForm] = useState(false);
  const [legalName, setLegalName] = useState("");
  const [phone, setPhone] = useState("");
  const [email, setEmail] = useState("");
  const [partyType, setPartyType] = useState<PartyType>("CUSTOMER");
  const [creditLimit, setCreditLimit] = useState("");

  const parties = useQuery({
    queryKey: ["parties", query],
    queryFn: () => api.getListField<Party>(`/contacts/parties${query ? `?q=${encodeURIComponent(query)}` : ""}`, "parties"),
  });
  // A "BOTH" party counts as a match for either the Customers or
  // Suppliers filter — it genuinely is both, filtering it out of one
  // would hide it from someone specifically looking for it.
  const filteredParties = (parties.data ?? []).filter((p) => typeFilter === "ALL" || p.PartyType === typeFilter || p.PartyType === "BOTH");

  const createParty = useMutation({
    mutationFn: () =>
      api.post<Party>("/contacts/parties", {
        party_type: partyType,
        legal_name: legalName,
        trade_name: "",
        phone,
        email,
        currency_code: "INR",
        credit_limit_amount: creditLimit || null,
        payment_terms_days: null,
        notes: "",
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["parties"] });
      setLegalName("");
      setPhone("");
      setEmail("");
      setCreditLimit("");
      setShowForm(false);
    },
  });

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Contacts</h1>
          <p className={layout.subtitle}>Customers and suppliers.</p>
        </div>
        <button type="button" className={ui.btnPrimary} onClick={() => setShowForm((v) => !v)}>
          + New contact
        </button>
      </div>

      {showForm ? (
        <div className={layout.panel}>
          <div className={ui.formGrid}>
            <div className={ui.field}>
              <label htmlFor="party-type">Type</label>
              <select id="party-type" className={ui.select} value={partyType} onChange={(e) => setPartyType(e.target.value as PartyType)}>
                <option value="CUSTOMER">Customer</option>
                <option value="SUPPLIER">Supplier</option>
                <option value="BOTH">Both</option>
              </select>
            </div>
            <div className={ui.field}>
              <label htmlFor="party-name">Name</label>
              <input id="party-name" className={ui.input} value={legalName} onChange={(e) => setLegalName(e.target.value)} />
            </div>
            <div className={ui.field}>
              <label htmlFor="party-phone">Phone</label>
              <input id="party-phone" className={ui.input} value={phone} onChange={(e) => setPhone(e.target.value)} />
            </div>
            <div className={ui.field}>
              <label htmlFor="party-email">Email</label>
              <input id="party-email" type="email" className={ui.input} value={email} onChange={(e) => setEmail(e.target.value)} />
            </div>
            {partyType !== "SUPPLIER" ? (
              <div className={ui.field}>
                <label htmlFor="party-credit">Credit limit (₹)</label>
                <input id="party-credit" className={ui.input} value={creditLimit} onChange={(e) => setCreditLimit(e.target.value)} />
              </div>
            ) : null}
          </div>
          <div className={ui.formActions} style={{ marginTop: 12 }}>
            <button
              type="button"
              className={ui.btnPrimary}
              disabled={!legalName || createParty.isPending}
              onClick={() => createParty.mutate()}
            >
              Save contact
            </button>
          </div>
          {createParty.isError ? (
            <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
              {createParty.error instanceof ApiError ? createParty.error.message : "Could not save this contact."}
            </p>
          ) : null}
        </div>
      ) : null}

      <div className={layout.panel}>
        <div className={ui.toolbar} style={{ marginBottom: 12 }}>
          <input
            className={ui.input}
            placeholder="Search by name or phone…"
            aria-label="Search contacts"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            style={{ maxWidth: 360 }}
          />
          <select
            className={ui.select}
            aria-label="Filter by type"
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value as TypeFilter)}
          >
            <option value="ALL">All contacts</option>
            <option value="CUSTOMER">Customers</option>
            <option value="SUPPLIER">Suppliers</option>
          </select>
        </div>
        {parties.isError ? (
          <p className={layout.errorState} role="alert">
            Couldn't load contacts.
          </p>
        ) : parties.isPending ? (
          <div className={layout.skeleton} style={{ height: 200 }} aria-hidden="true" />
        ) : filteredParties.length === 0 ? (
          <p className={layout.emptyState}>{parties.data.length === 0 ? "No contacts yet." : "No contacts match this filter."}</p>
        ) : (
          <div className={ui.tableScroll}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Type</th>
                  <th scope="col">Phone</th>
                  <th scope="col">Email</th>
                </tr>
              </thead>
              <tbody>
                {filteredParties.map((p) => (
                  <tr key={p.ID}>
                    <td>
                      <Link to="/contacts/$id" params={{ id: p.ID }} className={ui.linkRow}>
                        {p.LegalName}
                      </Link>
                    </td>
                    <td>{p.PartyType}</td>
                    <td>{p.Phone}</td>
                    <td>{p.Email}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
