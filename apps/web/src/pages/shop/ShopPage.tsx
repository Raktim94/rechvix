import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import ui from "../../components/ui.module.css";
import { api, ApiError } from "../../lib/api-client";
import layout from "../DashboardPage.module.css";

interface Product { variant_id: string; sku: string; title: string; description?: string; price: string; currency: string; in_stock: boolean }
interface Shop { title: string; intro?: string; currency: string; products: Product[] }

function money(v: number, cur: string) {
  try { return new Intl.NumberFormat(undefined, { style: "currency", currency: cur }).format(v); } catch { return `${v.toFixed(2)} ${cur}`; }
}

/** The public catalogue and order form. No sign-in: a visitor picks products and sends an order the team then confirms. */
export function ShopPage({ slug }: { slug: string }) {
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  const shop = useQuery({ queryKey: ["shop", slug, search], queryFn: () => api.get<Shop>(`/shop/${slug}?q=${encodeURIComponent(search)}`), retry: false });
  const [cart, setCart] = useState<Record<string, number>>({});
  const [f, setF] = useState({ name: "", email: "", phone: "", address: "", notes: "" });
  const order = useMutation({
    mutationFn: () => api.post<{ reference: string; total: string; currency: string }>(`/shop/${slug}/orders`, { ...f, lines: Object.entries(cart).filter(([, n]) => n > 0).map(([variant_id, quantity]) => ({ variant_id, quantity })) }),
    onSuccess: () => setCart({}),
  });
  if (shop.isPending) return <main className={layout.page}><p>Loading…</p></main>;
  if (shop.isError || !shop.data) return <main className={layout.page}><h1>Shop not found</h1><p>This shop does not exist or is closed.</p></main>;
  const s = shop.data;
  const byId = new Map(s.products.map((p) => [p.variant_id, p]));
  const lines = Object.entries(cart).filter(([, n]) => n > 0);
  const total = lines.reduce((n, [id, qty]) => n + Number(byId.get(id)?.price ?? 0) * qty, 0);
  const set = (id: string, n: number) => setCart((c) => ({ ...c, [id]: Math.max(0, Math.min(1000, n)) }));

  if (order.isSuccess) {
    return (
      <main className={layout.page} style={{ maxWidth: 720, margin: "0 auto" }}>
        <h1>Thank you</h1>
        <p role="status">Your order <strong>{order.data.reference}</strong> for {money(Number(order.data.total), order.data.currency)} was received. We will confirm it by email.</p>
        <p><a href={`/portal/${slug}`}>Go to your client area</a></p>
      </main>
    );
  }
  return (
    <main className={layout.page} style={{ maxWidth: 1100, margin: "0 auto" }}>
      <header>
        <h1>{s.title}</h1>
        {s.intro ? <p>{s.intro}</p> : null}
        <p><a href={`/portal/${slug}`}>Client area: your invoices and quotations</a></p>
      </header>
      <form role="search" onSubmit={(e) => { e.preventDefault(); setSearch(q); }} style={{ margin: "12px 0" }}>
        <label htmlFor="shop-q" className="srOnly">Search products</label>
        <input id="shop-q" className={ui.input} value={q} placeholder="Search products" onChange={(e) => setQ(e.target.value)} style={{ maxWidth: 360 }} />{" "}
        <button type="submit" className={ui.btnSecondary}>Search</button>
      </form>
      {s.products.length === 0 ? <p className={layout.emptyState}>No products to show.</p> : (
        <ul style={{ listStyle: "none", padding: 0, margin: 0, display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))", gap: 12 }}>
          {s.products.map((p) => (
            <li key={p.variant_id} className={layout.panel} style={{ margin: 0 }}>
              <h2 style={{ fontSize: "var(--text-base)" }}>{p.title}</h2>
              {p.description ? <p className={ui.muted}>{p.description}</p> : null}
              <p><strong>{money(Number(p.price), p.currency)}</strong> · <span className={ui.badge} data-tone={p.in_stock ? "positive" : "warning"}>{p.in_stock ? "In stock" : "On request"}</span></p>
              <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
                <button type="button" className={ui.btnSecondary} aria-label={`Fewer ${p.title}`} onClick={() => set(p.variant_id, (cart[p.variant_id] ?? 0) - 1)}>−</button>
                <span aria-live="polite" style={{ minWidth: 24, textAlign: "center" }}>{cart[p.variant_id] ?? 0}</span>
                <button type="button" className={ui.btnSecondary} aria-label={`More ${p.title}`} onClick={() => set(p.variant_id, (cart[p.variant_id] ?? 0) + 1)}>+</button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {lines.length > 0 ? (
        <section className={layout.panel} aria-labelledby="cart-heading" style={{ marginTop: 16 }}>
          <h2 id="cart-heading">Your order</h2>
          <ul>{lines.map(([id, n]) => <li key={id}>{n} × {byId.get(id)?.title}</li>)}</ul>
          <p><strong>Estimated total: {money(total, s.currency)}</strong> <span className={ui.muted}>(taxes and delivery are confirmed by the seller)</span></p>
          <form onSubmit={(e) => { e.preventDefault(); order.mutate(); }}>
            <div className={ui.formGrid}>
              <div className={ui.field}><label htmlFor="o-name">Your name</label><input id="o-name" className={ui.input} autoComplete="name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} /></div>
              <div className={ui.field}><label htmlFor="o-email">Email</label><input id="o-email" type="email" className={ui.input} autoComplete="email" value={f.email} onChange={(e) => setF({ ...f, email: e.target.value })} /></div>
              <div className={ui.field}><label htmlFor="o-phone">Phone (optional)</label><input id="o-phone" className={ui.input} autoComplete="tel" value={f.phone} onChange={(e) => setF({ ...f, phone: e.target.value })} /></div>
              <div className={ui.field}><label htmlFor="o-addr">Delivery address</label><input id="o-addr" className={ui.input} autoComplete="street-address" value={f.address} onChange={(e) => setF({ ...f, address: e.target.value })} /></div>
            </div>
            <div className={ui.field}><label htmlFor="o-notes">Notes (optional)</label><input id="o-notes" className={ui.input} maxLength={1000} value={f.notes} onChange={(e) => setF({ ...f, notes: e.target.value })} /></div>
            <div className={ui.formActions} style={{ justifyContent: "flex-start" }}><button type="submit" className={ui.btnPrimary} disabled={order.isPending || f.name.trim().length < 2 || !f.email.includes("@")}>{order.isPending ? "Sending…" : "Send the order"}</button></div>
            {order.isError ? <p role="alert">{order.error instanceof ApiError ? order.error.message : "Could not send the order."}</p> : null}
          </form>
        </section>
      ) : null}
    </main>
  );
}
