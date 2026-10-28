import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { api, ApiError } from "../lib/api-client";
import { useOrgContext } from "../lib/useOrgContext";
import { whatsAppShareUrl } from "../lib/whatsapp";
import layout from "../pages/DashboardPage.module.css";
import ui from "./ui.module.css";

interface Settings { enabled: boolean; slug: string; title: string; intro: string; legal_entity_id: string; branch_id: string; warehouse_id: string; configured: boolean }
interface Prod { variant_id: string; sku: string; name: string; published: boolean; title?: string; description?: string; price?: string }
interface ShopOrder { id: string; reference: string; customer_name: string; customer_email: string; total_estimate: string; currency: string; status: string; sales_document_id?: string; invoice_document_id?: string; customer_phone?: string; created_at: string; delivery_address?: string }

/** Settings: switch the online shop on, choose what is published, and see the orders visitors placed. */
export function ShopAdmin() {
  const qc = useQueryClient();
  const org = useOrgContext();
  const settings = useQuery({ queryKey: ["shop-settings"], queryFn: () => api.get<Settings>("/shop/settings") });
  const products = useQuery({ queryKey: ["shop-products"], queryFn: () => api.getListField<Prod>("/shop/products", "products") });
  const orders = useQuery({ queryKey: ["shop-orders"], queryFn: () => api.getListField<ShopOrder>("/shop/orders", "orders") });
  const [f, setF] = useState<Settings | null>(null);
  useEffect(() => { if (settings.data && typeof settings.data.title === "string") setF(settings.data); }, [settings.data]);
  const save = useMutation({
    mutationFn: (v: Settings) => api.put<Settings>("/shop/settings", { ...v, legal_entity_id: v.legal_entity_id || org.legalEntity?.ID, branch_id: v.branch_id || org.branch?.ID, warehouse_id: v.warehouse_id || org.warehouse?.ID }),
    onSuccess: (d) => qc.setQueryData(["shop-settings"], d),
  });
  const publish = useMutation({
    mutationFn: (v: { p: Prod; published: boolean }) => api.put(`/shop/products/${v.p.variant_id}`, { published: v.published, title: v.p.title ?? "", description: v.p.description ?? "" }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["shop-products"] }),
  });
  if (!f) return null;
  const url = f.slug ? `${window.location.origin}/shop/${f.slug}` : "";
  return (
    <section className={layout.panel} aria-labelledby="shop-heading">
      <h2 id="shop-heading">Online shop and client area</h2>
      <p className={ui.muted}>A public page where visitors browse the products you publish and send orders. Orders arrive here and as draft sales orders. Customers also get a client area where they open their documents, pay and sign.</p>
      <form onSubmit={(e) => { e.preventDefault(); save.mutate(f); }}>
        <div className={ui.formGrid}>
          <div className={ui.field}><label htmlFor="sh-title">Shop name</label><input id="sh-title" className={ui.input} maxLength={80} value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} /></div>
          <div className={ui.field}><label htmlFor="sh-slug">Web address</label><input id="sh-slug" className={ui.input} placeholder="my-shop" value={f.slug} onChange={(e) => setF({ ...f, slug: e.target.value.toLowerCase() })} /></div>
        </div>
        <div className={ui.field}><label htmlFor="sh-intro">Introduction (optional)</label><input id="sh-intro" className={ui.input} maxLength={1000} value={f.intro} onChange={(e) => setF({ ...f, intro: e.target.value })} /></div>
        <label style={{ display: "flex", gap: 8, alignItems: "center", margin: "6px 0" }}><input type="checkbox" checked={f.enabled} onChange={(e) => setF({ ...f, enabled: e.target.checked })} /> Shop is open to the public</label>
        <p className={ui.muted}>Orders are created for the current company, branch and warehouse. Prices come from your default price list.</p>
        <div className={ui.formActions} style={{ justifyContent: "flex-start" }}>
          <button type="submit" className={ui.btnPrimary} disabled={save.isPending || !f.title.trim() || !f.slug.trim()}>{save.isPending ? "Saving…" : "Save"}</button>
          {save.isSuccess ? <span role="status">Saved.</span> : null}
        </div>
        {save.isError ? <p role="alert">{save.error instanceof ApiError ? save.error.message : "Could not save."}</p> : null}
      </form>
      {f.enabled && url ? <p>Your shop: <a href={url} target="_blank" rel="noreferrer">{url}<span className="srOnly"> (opens in a new tab)</span></a></p> : null}

      <h3>Products</h3>
      <p className={ui.muted}>A product appears in the shop when it is ticked and has a price on the default price list.</p>
      {products.data && products.data.length > 0 ? (
        <div className={ui.tableScroll} tabIndex={0} style={{ maxHeight: 320, overflowY: "auto" }}>
          <table className={ui.table}>
            <caption className="srOnly">Products to publish</caption>
            <thead><tr><th scope="col">In the shop</th><th scope="col">Product</th><th scope="col">SKU</th><th scope="col">Price</th></tr></thead>
            <tbody>{products.data.map((p) => (
              <tr key={p.variant_id}>
                <td><input type="checkbox" aria-label={`Publish ${p.name}`} checked={p.published} disabled={publish.isPending || !p.price} onChange={(e) => publish.mutate({ p, published: e.target.checked })} /></td>
                <td>{p.name}</td><td>{p.sku}</td><td>{p.price || <span className={ui.muted}>no price</span>}</td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      ) : <p className={layout.emptyState}>No products yet.</p>}

      <h3>Online orders</h3>
      <ShopOrders orders={orders.data ?? []} loading={orders.isPending} />
    </section>
  );
}

const FLOW: { status: string; label: string; next?: { to: string; label: string } }[] = [
  { status: "NEW", label: "New", next: { to: "CONFIRMED", label: "Confirm" } },
  { status: "CONFIRMED", label: "Confirmed", next: { to: "PROCESSING", label: "Start processing" } },
  { status: "PROCESSING", label: "Processing", next: { to: "DISPATCHED", label: "Mark dispatched" } },
  { status: "DISPATCHED", label: "Dispatched", next: { to: "DELIVERED", label: "Mark delivered" } },
  { status: "DELIVERED", label: "Delivered" },
  { status: "CANCELLED", label: "Cancelled" },
];
const CANCELLABLE = new Set(["NEW", "CONFIRMED", "PROCESSING"]);

/** The orders inbox: one tab per stage, the next step on each order, and a one-click draft invoice. Finalizing that invoice
 * in Sales is what takes the goods out of stock. */
function ShopOrders({ orders, loading }: { orders: ShopOrder[]; loading: boolean }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [tab, setTab] = useState<string>("ALL");
  const refresh = () => void qc.invalidateQueries({ queryKey: ["shop-orders"] });
  const move = useMutation({ mutationFn: (v: { id: string; status: string }) => api.post(`/shop/orders/${v.id}/status`, { status: v.status }), onSuccess: refresh });
  const invoice = useMutation({
    mutationFn: (id: string) => api.post<{ invoice_document_id: string }>(`/shop/orders/${id}/invoice`),
    onSuccess: (r) => {
      refresh();
      void navigate({ to: "/sales/$id", params: { id: r.invoice_document_id } });
    },
  });
  const count = (st: string) => orders.filter((o) => o.status === st).length;
  const shown = tab === "ALL" ? orders : orders.filter((o) => o.status === tab);
  const err = move.error ?? invoice.error;

  if (loading) return <div className={layout.skeleton} style={{ height: 120 }} aria-hidden="true" />;
  if (orders.length === 0) return <p className={ui.muted}>No orders yet. Share your shop link and they will appear here.</p>;

  return (
    <>
      <div role="group" aria-label="Filter orders by stage" style={{ display: "flex", gap: 8, flexWrap: "wrap", marginBottom: 12 }}>
        {[{ status: "ALL", label: "All" }, ...FLOW].map((t) => (
          <button key={t.status} type="button" className={tab === t.status ? ui.btnPrimary : ui.btnSecondary} aria-pressed={tab === t.status} onClick={() => setTab(t.status)} style={{ minHeight: 44 }}>
            {t.label} ({t.status === "ALL" ? orders.length : count(t.status)})
          </button>
        ))}
      </div>
      {err ? <p role="alert" style={{ color: "var(--color-negative)" }}>{err instanceof ApiError ? err.message : "Could not update the order."}</p> : null}
      {shown.length === 0 ? <p className={ui.muted}>No orders in this stage.</p> : (
        <div className={ui.tableScroll} tabIndex={0}>
          <table className={ui.table}>
            <caption className="srOnly">Orders from the online shop</caption>
            <thead><tr><th scope="col">Reference</th><th scope="col">Customer</th><th scope="col">Total</th><th scope="col">Status</th><th scope="col">When</th><th scope="col">Actions</th></tr></thead>
            <tbody>{shown.map((o) => {
              const step = FLOW.find((f) => f.status === o.status);
              const wa = o.customer_phone ? whatsAppShareUrl(o.customer_phone, `Hello ${o.customer_name}, about your order ${o.reference}: it is now ${step?.label.toLowerCase() ?? o.status.toLowerCase()}. Thank you!`) : null;
              return (
                <tr key={o.id}>
                  <td>{o.reference}</td>
                  <td>{o.customer_name}<br /><span className={ui.muted}>{o.customer_email}</span>{o.delivery_address ? <><br /><span className={ui.muted}>{o.delivery_address}</span></> : null}</td>
                  <td className="num">{o.total_estimate} {o.currency}</td>
                  <td><span className={ui.badge} data-tone={o.status === "CANCELLED" ? "negative" : o.status === "DELIVERED" ? "positive" : "warning"}>{step?.label ?? o.status}</span></td>
                  <td>{new Date(o.created_at).toLocaleString()}</td>
                  <td style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                    {step?.next ? <button type="button" className={ui.btnSecondary} disabled={move.isPending} onClick={() => move.mutate({ id: o.id, status: step.next!.to })}>{step.next.label}</button> : null}
                    {o.invoice_document_id ? (
                      <button type="button" className={ui.btnGhost} onClick={() => void navigate({ to: "/sales/$id", params: { id: o.invoice_document_id! } })}>Open invoice</button>
                    ) : o.status !== "NEW" && o.status !== "CANCELLED" ? (
                      <button type="button" className={ui.btnGhost} disabled={invoice.isPending} onClick={() => invoice.mutate(o.id)}>Create invoice</button>
                    ) : null}
                    {wa ? <a className={ui.btnGhost} href={wa} target="_blank" rel="noopener noreferrer">WhatsApp</a> : null}
                    {CANCELLABLE.has(o.status) && !o.invoice_document_id ? <button type="button" className={ui.btnGhost} disabled={move.isPending} onClick={() => move.mutate({ id: o.id, status: "CANCELLED" })}>Cancel</button> : null}
                  </td>
                </tr>
              );
            })}</tbody>
          </table>
        </div>
      )}
    </>
  );
}
