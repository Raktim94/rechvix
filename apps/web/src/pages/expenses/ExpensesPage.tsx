import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { StatCard } from "../../components/StatCard";
import { WalletIcon } from "../../components/icons";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import { formatMoney } from "../../lib/money";
import layout from "../DashboardPage.module.css";
import { ALLOWED_TYPES, ExpenseAttachments, MAX_ATTACHMENT_BYTES, readFileAsBase64 } from "./ExpenseAttachments";

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
// "General Expenses" (accounting.domain.CodeGeneralExpenses) is the
// default chart's own catch-all EXPENSE account -- exactly the "Other"
// category a shop owner needs when nothing else fits, it just wasn't
// labeled that way here. Relabeling it in this one picker (not renaming
// the underlying account, which other screens/reports already reference
// by its real name) and requiring the Note field once it's picked is
// what actually makes "type your own expense" work, without inventing a
// second, ad-hoc "free-text category" concept alongside a real
// double-entry chart of accounts.
const OTHER_CATEGORY_CODE = "5990";

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
  const [attachFile, setAttachFile] = useState<File | null>(null);
  const [attachError, setAttachError] = useState<string | null>(null);

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
    mutationFn: async () => {
      const journal = await api.post<{ ID: string }>("/accounting/journals", {
        source_type: "manual_expense",
        journal_date: new Date(date).toISOString(),
        description: note,
        lines: [
          { account_code: category, debit: amount, credit: "0", description: note },
          { account_code: paidVia, debit: "0", credit: amount, description: note },
        ],
      });
      if (attachFile) {
        try {
          const base64 = await readFileAsBase64(attachFile);
          await api.post(`/accounting/expenses/${journal.ID}/attachments`, {
            filename: attachFile.name,
            content_type: attachFile.type,
            data_base64: base64,
          });
        } catch (err) {
          // The expense itself is already recorded at this point --
          // don't make a receipt-photo upload failure look like the
          // whole entry failed. Surfaced separately so the shop owner
          // knows to attach it again from the row below instead.
          setAttachError(err instanceof ApiError ? err.message : "Expense recorded, but the attachment could not be uploaded.");
        }
      }
      return journal;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["expenses"] });
      setAmount("");
      setAttachFile(null);
      setNote("");
    },
  });

  const isOther = category === OTHER_CATEGORY_CODE;
  const ready = !!category && !!paidVia && Number(amount) > 0 && (!isOther || note.trim().length > 0);

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
                      {a.Code === OTHER_CATEGORY_CODE ? "Other (describe below)" : a.Name}
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
                <label htmlFor="expense-note">{isOther ? "What was this expense for? (required)" : "Note"}</label>
                <input
                  id="expense-note"
                  className={ui.input}
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  placeholder={isOther ? "e.g. Diwali decorations for the shop front" : "e.g. Electricity bill for August"}
                  required={isOther}
                />
              </div>
              <div className={ui.field} style={{ gridColumn: "span 2" }}>
                <label htmlFor="expense-attachment">Attach a receipt or bill (optional)</label>
                <input
                  id="expense-attachment"
                  type="file"
                  className={ui.input}
                  accept="image/png,image/jpeg,image/webp,application/pdf"
                  onChange={(e) => {
                    setAttachError(null);
                    const file = e.target.files?.[0] ?? null;
                    if (file && file.size > MAX_ATTACHMENT_BYTES) {
                      setAttachError("File is too large — please use one under 8MB.");
                      e.target.value = "";
                      return;
                    }
                    if (file && !ALLOWED_TYPES.has(file.type)) {
                      setAttachError("Please attach a PNG, JPEG, WEBP, or PDF file.");
                      e.target.value = "";
                      return;
                    }
                    setAttachFile(file);
                  }}
                />
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
            {attachError ? (
              <p role="alert" style={{ color: "var(--color-negative)", marginTop: 8 }}>
                {attachError}
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
                  <th scope="col">Documents</th>
                </tr>
              </thead>
              <tbody>
                {(expenses.data ?? []).map((e) => (
                  <tr key={e.JournalID}>
                    <td>{new Date(e.JournalDate).toLocaleDateString()}</td>
                    <td>{e.AccountName}</td>
                    <td>{e.Description || "—"}</td>
                    <td className="num">{formatMoney(e.Amount)}</td>
                    <td>
                      <ExpenseAttachments journalId={e.JournalID} />
                    </td>
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
