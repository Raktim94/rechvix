import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { StatCard } from "../../components/StatCard";
import { WalletIcon } from "../../components/icons";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import layout from "../DashboardPage.module.css";

interface Account {
  ID: string;
  Code: string;
  Name: string;
  AccountType: "ASSET" | "LIABILITY" | "EQUITY" | "INCOME" | "EXPENSE";
  IsActive: boolean;
}

interface ExpenseEntry {
  JournalID: string;
  JournalDate: string;
  AccountCode: string;
  AccountName: string;
  Description: string;
  Amount: { amount: string; currency: string };
}

const CASH_BANK_CODES = new Set(["1000", "1010"]);

function todayIsoDate(): string {
  return new Date().toISOString().slice(0, 10);
}

/** A thin, shop-owner-friendly UI over accounting.Post — internally
 * these are ordinary two-line balanced journal entries (Debit an
 * expense account, Credit Cash/Bank), the exact "manual adjustment
 * journal" Service.Post's own doc comment already anticipated. Kept out
 * of AccountingPage (the raw chart-of-accounts view) since a shop owner
 * recording "paid ₹500 for electricity" shouldn't need to think in
 * debits/credits to do it. */
export function ExpensesPage() {
  const queryClient = useQueryClient();
  const [category, setCategory] = useState("");
  const [paidVia, setPaidVia] = useState("");
  const [amount, setAmount] = useState("");
  const [date, setDate] = useState(todayIsoDate);
  const [note, setNote] = useState("");

  const accounts = useQuery({
    queryKey: ["accounts"],
    queryFn: () => api.get<Account[]>("/accounting/accounts"),
  });
  const expenseAccounts = (accounts.data ?? []).filter((a) => a.AccountType === "EXPENSE" && a.IsActive);
  const paidViaAccounts = (accounts.data ?? []).filter((a) => CASH_BANK_CODES.has(a.Code) && a.IsActive);

  // Selects fall back to showing the first option before the user picks
  // one, so the state backing `ready`/submit must be set to match as
  // soon as the accounts they're drawn from load — otherwise the form
  // looks ready (a category is visibly selected) while `ready` is still
  // false because `category` itself is still "".
  useEffect(() => {
    if (!category && expenseAccounts[0]) setCategory(expenseAccounts[0].Code);
  }, [category, expenseAccounts]);
  useEffect(() => {
    if (!paidVia && paidViaAccounts[0]) setPaidVia(paidViaAccounts[0].Code);
  }, [paidVia, paidViaAccounts]);

  const expenses = useQuery({
    queryKey: ["expenses"],
    queryFn: () => api.getListField<ExpenseEntry>("/accounting/expenses?limit=200", "expenses"),
  });

  const todayTotal = (expenses.data ?? [])
    .filter((e) => e.JournalDate.slice(0, 10) === todayIsoDate())
    .reduce((sum, e) => sum + Number(e.Amount.amount), 0);
  const monthTotal = (expenses.data ?? [])
    .filter((e) => e.JournalDate.slice(0, 7) === todayIsoDate().slice(0, 7))
    .reduce((sum, e) => sum + Number(e.Amount.amount), 0);

  const record = useMutation({
    mutationFn: () =>
      api.post("/accounting/journals", {
        source_type: "manual_expense",
        journal_date: new Date(date).toISOString(),
        description: note,
        lines: [
          { account_code: category, debit: amount, credit: "0", description: note },
          { account_code: paidVia, debit: "0", credit: amount, description: note },
        ],
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["expenses"] });
      setAmount("");
      setNote("");
    },
  });

  const ready = !!category && !!paidVia && Number(amount) > 0;

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Expenses</h1>
          <p className={layout.subtitle}>Day-to-day shop expenses — rent, electricity, wages, delivery charges.</p>
        </div>
      </div>

      <div className={layout.cardGrid} style={{ gridTemplateColumns: "repeat(2, 1fr)" }}>
        <StatCard label="Today's expenses" value={formatMoney({ amount: String(todayTotal), currency: "INR" })} icon={<WalletIcon />} />
        <StatCard label="This month's expenses" value={formatMoney({ amount: String(monthTotal), currency: "INR" })} icon={<WalletIcon />} />
      </div>

      <div className={layout.panel}>
        <h2>Record an expense</h2>
        {accounts.isPending ? (
          <div className={layout.skeleton} style={{ height: 100 }} aria-hidden="true" />
        ) : expenseAccounts.length === 0 || paidViaAccounts.length === 0 ? (
          <p className={layout.emptyState}>
            Set up your chart of accounts on the <Link to="/accounting">Accounting</Link> page first.
          </p>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              if (ready) record.mutate();
            }}
          >
            <div className={ui.formGrid}>
              <div className={ui.field}>
                <label htmlFor="expense-category">Category</label>
                <select id="expense-category" className={ui.select} value={category || expenseAccounts[0]?.Code} onChange={(e) => setCategory(e.target.value)}>
                  {expenseAccounts.map((a) => (
                    <option key={a.ID} value={a.Code}>
                      {a.Name}
                    </option>
                  ))}
                </select>
              </div>
              <div className={ui.field}>
                <label htmlFor="expense-paid-via">Paid via</label>
                <select id="expense-paid-via" className={ui.select} value={paidVia || paidViaAccounts[0]?.Code} onChange={(e) => setPaidVia(e.target.value)}>
                  {paidViaAccounts.map((a) => (
                    <option key={a.ID} value={a.Code}>
                      {a.Name}
                    </option>
                  ))}
                </select>
              </div>
              <div className={ui.field}>
                <label htmlFor="expense-amount">Amount</label>
                <input id="expense-amount" className={ui.input} inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="0.00" />
              </div>
              <div className={ui.field}>
                <label htmlFor="expense-date">Date</label>
                <input id="expense-date" type="date" className={ui.input} value={date} onChange={(e) => setDate(e.target.value)} />
              </div>
              <div className={ui.field} style={{ gridColumn: "span 2" }}>
                <label htmlFor="expense-note">Note</label>
                <input id="expense-note" className={ui.input} value={note} onChange={(e) => setNote(e.target.value)} placeholder="e.g. Electricity bill for August" />
              </div>
            </div>
            <div className={ui.formActions} style={{ marginTop: 16 }}>
              <button type="submit" className={ui.btnPrimary} disabled={!ready || record.isPending}>
                {record.isPending ? "Saving…" : "Record expense"}
              </button>
            </div>
            {record.isError ? (
              <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
                {record.error instanceof ApiError ? record.error.message : "Could not record this expense."}
              </p>
            ) : null}
          </form>
        )}
      </div>

      <div className={layout.panel}>
        <h2>Recent expenses</h2>
        {expenses.isPending ? (
          <div className={layout.skeleton} style={{ height: 160 }} aria-hidden="true" />
        ) : expenses.isError ? (
          <p className={layout.errorState} role="alert">
            Couldn't load expenses.
          </p>
        ) : (expenses.data ?? []).length === 0 ? (
          <p className={layout.emptyState}>No expenses recorded yet.</p>
        ) : (
          <div className={ui.tableScroll}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">Date</th>
                  <th scope="col">Category</th>
                  <th scope="col">Note</th>
                  <th scope="col">Amount</th>
                </tr>
              </thead>
              <tbody>
                {(expenses.data ?? []).map((e) => (
                  <tr key={e.JournalID}>
                    <td>{new Date(e.JournalDate).toLocaleDateString()}</td>
                    <td>{e.AccountName}</td>
                    <td>{e.Description || "—"}</td>
                    <td className="num">{formatMoney(e.Amount)}</td>
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
