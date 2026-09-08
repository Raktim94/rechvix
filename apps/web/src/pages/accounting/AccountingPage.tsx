import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ReportTable } from "../../components/ReportTable";
import ui from "../../components/ui.module.css";
import { api } from "../../lib/api-client";
import layout from "../DashboardPage.module.css";

interface Account {
  ID: string;
  Code: string;
  Name: string;
  AccountType: string;
  IsActive: boolean;
}

export function AccountingPage() {
  const accounts = useQuery({
    queryKey: ["accounts"],
    queryFn: () => api.get<Account[]>("/accounting/accounts"),
  });

  return (
    <div className={layout.page}>
      <div className={layout.heading}>
        <div>
          <h1>Accounting</h1>
          <p className={layout.subtitle}>Chart of accounts, trial balance, and who owes what.</p>
        </div>
        <Link to="/expenses" className={ui.btnPrimary}>
          + Record expense
        </Link>
      </div>

      <div className={layout.panel}>
        <h2>Trial balance</h2>
        <ReportTable path="/reports/accounting/trial-balance?format=json" />
      </div>

      <div className={layout.panel}>
        <h2>Receivables (who owes you)</h2>
        <ReportTable path="/reports/accounting/receivables?format=json" emptyLabel="Nobody owes you anything right now." />
      </div>

      <div className={layout.panel}>
        <h2>Payables (what you owe)</h2>
        <ReportTable path="/reports/accounting/payables?format=json" emptyLabel="You don't owe any suppliers right now." />
      </div>

      <div className={layout.panel}>
        <h2>Chart of accounts</h2>
        {accounts.isError ? (
          <p className={layout.errorState} role="alert">
            Couldn't load the chart of accounts.
          </p>
        ) : accounts.isPending ? (
          <div className={layout.skeleton} style={{ height: 200 }} aria-hidden="true" />
        ) : accounts.data.length === 0 ? (
          <p className={layout.emptyState}>No accounts set up yet.</p>
        ) : (
          <div className={ui.tableScroll}>
            <table className={ui.table}>
              <thead>
                <tr>
                  <th scope="col">Code</th>
                  <th scope="col">Name</th>
                  <th scope="col">Type</th>
                </tr>
              </thead>
              <tbody>
                {accounts.data.map((a) => (
                  <tr key={a.ID}>
                    <td className="num">{a.Code}</td>
                    <td>{a.Name}</td>
                    <td>{a.AccountType}</td>
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
