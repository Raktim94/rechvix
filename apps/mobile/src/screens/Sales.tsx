import React, { useState } from "react";
import { Alert, FlatList, Modal, ScrollView, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import {
  allPayments, cancelLocalSale, canEditSale, creditNotesForSale, discardSale, getSale, listSales, paymentsForSale, recentServerDocs, retrySale, saleRevisions,
  type CreditNote, type Payment, type Sale, type SaleRevision, type ServerDoc,
} from "../db/repo";
import { dec, decToString, minorToString, parseMinor } from "../lib/decimal";
import { useLocal } from "../lib/hooks";
import { TEMPLATES, invoiceTitle, type InvoiceTemplate } from "../lib/invoice";
import { formatMinor } from "../lib/money";
import { creditsBySale, payloadCalc, payStatus, saleTotalMinor, salePaidMinor, totalMismatch, livePayments } from "../lib/sale";
import { shareCredit, shareDeliveryNote, shareSale } from "../lib/share";
import { cartActions, getCart, cartIsEmpty } from "../state/cart";
import { uid, useApp } from "../state/app";
import { CorrectionError } from "../sync/correct";
import { CreditNoteError, creditedQty, type ReturnPick } from "../sync/creditNote";
import { fonts, radius, space, usePalette } from "../theme";
import { Badge, Body, Button, Card, Chip, Divider, Empty, Field, Header, Icon, Label, Screen, Tap } from "../ui/kit";
import { T } from "../ui/text";
import { PaymentSheet, useLedgers } from "./Dues";

type Tone = "good" | "warn" | "bad" | "info" | "neutral";
export interface Row { sale: Sale; total: bigint; paid: bigint; status: "PAID" | "PARTIAL" | "UNPAID"; payments: Payment[] }

function rowsFor(sales: Sale[], pays: Payment[]): Row[] {
  const by = new Map<string, Payment[]>();
  for (const p of pays) if (p.sale_local_id) by.set(p.sale_local_id, [...(by.get(p.sale_local_id) ?? []), p]);
  return sales.map((sale) => {
    const payments = by.get(sale.local_id) ?? [];
    const total = saleTotalMinor(sale), paid = salePaidMinor(sale, payments);
    return { sale, total, paid, status: payStatus(total, paid), payments };
  });
}
export function useSaleRows(limit = 2000): Row[] {
  const sales = useLocal((db) => listSales(db, limit), []);
  const pays = useLocal((db) => allPayments(db), [] as Payment[]);
  return rowsFor(sales, pays);
}

/** What the sync is doing with this invoice. Only shown when a server is linked; a standalone phone has nothing to sync to. */
export function syncBadge(s: Sale): { text: string; tone: Tone } {
  if (s.state === "synced") return { text: s.doc_number ?? "Synced", tone: "good" };
  if (s.state === "failed") return { text: "Needs attention", tone: "bad" };
  if (s.state === "syncing") return { text: "Syncing…", tone: "info" };
  return { text: "Waiting to sync", tone: "warn" };
}
const PAY_BADGE: Record<Row["status"], { text: string; tone: Tone }> = { PAID: { text: "Paid", tone: "good" }, PARTIAL: { text: "Part paid", tone: "warn" }, UNPAID: { text: "Unpaid", tone: "warn" } };

export function SaleRow({ row, last, onPress }: { row: Row; last?: boolean; onPress: () => void }) {
  const { currency, linked } = useApp();
  const { sale, total } = row;
  const pay = PAY_BADGE[row.status];
  const sb = syncBadge(sale);
  const number = sale.doc_number ?? sale.local_number ?? "";
  return (
    <View>
      <Tap label={`${sale.payload.customerName} ${formatMinor(total, currency)} ${sale.cancelled ? "cancelled" : pay.text}`} onPress={onPress} style={{ minHeight: 76, padding: space.lg, gap: 6 }}>
        <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
          <View style={{ flex: 1 }}>
            <T v="strong" numberOfLines={1}>{sale.payload.customerName}</T>
            <T v="small" muted>{number ? `${number} · ` : ""}{new Date(sale.created_at).toLocaleString(undefined, { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" })}</T>
          </View>
          <T v="money" style={{ fontFamily: fonts.monoBold, textDecorationLine: sale.cancelled ? "line-through" : "none" }}>{formatMinor(total, currency)}</T>
        </View>
        <View style={{ flexDirection: "row", gap: 6, flexWrap: "wrap" }}>
          {sale.cancelled ? <Badge text="Cancelled" tone="neutral" /> : <Badge text={row.status === "PARTIAL" ? `${formatMinor(row.paid, currency)} paid · ${formatMinor(total - row.paid, currency)} due` : pay.text} tone={pay.tone} />}
          {linked && !sale.cancelled && sale.state !== "synced" ? <Badge text={sb.text} tone={sb.tone} icon="cloud-upload-outline" /> : null}
          {sale.revision > 0 ? <Badge text="Edited" tone="info" /> : null}
        </View>
      </Tap>
      {last ? null : <Divider />}
    </View>
  );
}

type Filter = "all" | "due" | "paid" | "waiting" | "attention" | "cancelled";
type Span = "all" | "today" | "7d" | "30d";
const DAY = 86_400_000;

export function Sales({ open }: { open: (id: string) => void }) {
  const { counts, linked } = useApp();
  const c = usePalette();
  const [f, setF] = useState<Filter>("all");
  const [span, setSpan] = useState<Span>("all");
  const [q, setQ] = useState("");
  const rows = useSaleRows();
  const server = useLocal((db) => recentServerDocs(db), [] as ServerDoc[]);
  const mine = new Set(rows.map((r) => r.sale.progress.docId).filter(Boolean));
  const t0 = new Date(); t0.setHours(0, 0, 0, 0);
  const since = span === "today" ? t0.getTime() : span === "7d" ? t0.getTime() - 6 * DAY : span === "30d" ? t0.getTime() - 29 * DAY : 0;
  const needle = q.trim().toLowerCase();
  const shown = rows.filter((r) => {
    const s = r.sale;
    if (s.created_at < since) return false;
    if (needle && !`${s.payload.customerName} ${s.doc_number ?? ""} ${s.local_number ?? ""}`.toLowerCase().includes(needle)) return false;
    switch (f) {
      case "all": return true;
      case "cancelled": return !!s.cancelled;
      case "due": return !s.cancelled && r.status !== "PAID";
      case "paid": return !s.cancelled && r.status === "PAID";
      case "waiting": return !s.cancelled && (s.state === "pending" || s.state === "syncing");
      case "attention": return !s.cancelled && s.state === "failed";
    }
  });
  const others = f === "all" && !needle && span === "all" ? server.filter((d) => !mine.has(d.id)) : [];
  return (
    <Screen>
      <Header title="Invoices" />
      <View style={{ paddingHorizontal: space.lg, paddingBottom: space.sm, gap: 10 }}>
        <View style={{ flexDirection: "row", alignItems: "center", backgroundColor: c.surface, borderRadius: radius.md + 2, borderWidth: 1, borderColor: c.border, paddingLeft: 14 }}>
          <Icon name="search" size={18} color={c.faint} />
          <TextInput accessibilityLabel="Search invoices" value={q} onChangeText={setQ} placeholder="Customer or invoice number" placeholderTextColor={c.faint}
            style={{ flex: 1, minHeight: 48, paddingHorizontal: 10, fontSize: 16, color: c.text, fontFamily: fonts.body }} />
        </View>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8 }}>
          <Chip title="All" active={f === "all"} onPress={() => setF("all")} />
          <Chip title="To collect" active={f === "due"} onPress={() => setF("due")} />
          <Chip title="Paid" active={f === "paid"} onPress={() => setF("paid")} />
          {linked ? <Chip title={`Waiting${counts.pending + counts.syncing ? ` (${counts.pending + counts.syncing})` : ""}`} active={f === "waiting"} onPress={() => setF("waiting")} /> : null}
          {linked ? <Chip title={`Attention${counts.failed ? ` (${counts.failed})` : ""}`} active={f === "attention"} onPress={() => setF("attention")} /> : null}
          <Chip title="Cancelled" active={f === "cancelled"} onPress={() => setF("cancelled")} />
        </ScrollView>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8 }}>
          {([["all", "Any date"], ["today", "Today"], ["7d", "7 days"], ["30d", "30 days"]] as [Span, string][]).map(([k, t]) => <Chip key={k} title={t} active={span === k} onPress={() => setSpan(k)} />)}
        </ScrollView>
      </View>
      <FlatList
        data={[...shown.map((r) => ({ kind: "local" as const, r })), ...others.map((d) => ({ kind: "server" as const, d }))]}
        keyExtractor={(x) => (x.kind === "local" ? x.r.sale.local_id : x.d.id)}
        contentContainerStyle={{ paddingHorizontal: space.lg, paddingBottom: 32 }}
        ListEmptyComponent={<Empty icon="receipt-outline" title={f === "all" && !needle ? "No invoices yet" : "Nothing matches"} hint={f === "all" && !needle ? "Invoices you make appear here." : "Try a different filter or search."} />}
        renderItem={({ item }) =>
          item.kind === "local" ? (
            <Card style={{ padding: 0, marginBottom: 10 }}><SaleRow row={item.r} last onPress={() => open(item.r.sale.local_id)} /></Card>
          ) : (
            <Card style={{ marginBottom: 10, gap: 4 }}>
              <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
                <T v="strong">{item.d.number}</T>
                <T v="money" style={{ fontFamily: fonts.monoBold }}>{item.d.total ? formatMinor(parseMinor(item.d.total), "INR") : "—"}</T>
              </View>
              <T v="small" muted>From the server · {item.d.issue_date.slice(0, 10)} · {item.d.status.toLowerCase()}</T>
            </Card>
          )
        }
      />
    </Screen>
  );
}

export function SaleDetail({ id, onClose, goSell, openCustomer }: { id: string; onClose: () => void; goSell: () => void; openCustomer: (partyId: string) => void }) {
  const { db, org, syncNow, syncing, online, linked, currency, profile, changed, correctInvoice, creditInvoice } = useApp();
  const c = usePalette();
  const sale = useLocal((d) => getSale(d, id), null as Sale | null, [id]);
  const pays = useLocal((d) => paymentsForSale(d, id), [] as Payment[], [id]);
  const revisions = useLocal((d) => saleRevisions(d, id), [] as SaleRevision[], [id]);
  const notes = useLocal((d) => creditNotesForSale(d, id), [] as CreditNote[], [id]);
  const [crediting, setCrediting] = useState(false);
  const ledger = useLedgers().find((l) => l.partyId === sale?.payload.customerId);
  const [template, setTemplate] = useState<InvoiceTemplate | null>(null);
  const [sharing, setSharing] = useState(false);
  const [paying, setPaying] = useState(false);
  const [ask, setAsk] = useState<null | "correct" | "cancel">(null);
  const [msg, setMsg] = useState<string | null>(null);
  if (!sale) return <Screen><Header title="Invoice" onBack={onClose} /><Empty title="Invoice not found" /></Screen>;

  const calc = payloadCalc(sale.payload);
  const total = saleTotalMinor(sale);
  const paid = salePaidMinor(sale, pays);
  const credited = creditsBySale(notes).get(id) ?? 0n;
  const net = total > credited ? total - credited : 0n;
  const due = net > paid ? net - paid : 0n;
  const refund = paid > net ? paid - net : 0n;
  const status = payStatus(net, paid);
  const m = (x: bigint) => formatMinor(x, currency);
  const sb = syncBadge(sale);
  const tpl = template ?? profile?.invoiceTemplate ?? "classic";
  const editable = canEditSale(sale);
  const correctable = sale.state === "synced" && !!sale.progress.docId && !sale.cancelled;

  async function share() {
    if (!db || !profile || !sale) return;
    setSharing(true); setMsg(null);
    try { await shareSale(db, sale.local_id, profile, currency, tpl); } catch { setMsg("Couldn't create the PDF. Try again."); } finally { setSharing(false); }
  }
  function startEdit() {
    const go = () => { cartActions.load(sale!.payload, { editing: { localId: sale!.local_id, number: sale!.local_number ?? "" } }); goSell(); };
    if (cartIsEmpty(getCart())) go();
    else Alert.alert("Replace the bill you're working on?", "You have a bill in progress. Opening this invoice for editing will replace it.", [{ text: "Keep mine", style: "cancel" }, { text: "Replace", style: "destructive", onPress: go }]);
  }

  return (
    <Screen>
      <ScrollView contentContainerStyle={{ paddingBottom: 40 }}>
        <Header title={sale.payload.customerName} onBack={onClose} sub={sale.doc_number ?? sale.local_number ?? undefined} />
        <View style={{ padding: space.lg, gap: space.lg, paddingTop: space.xs }}>
          <View style={{ flexDirection: "row", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
            {sale.cancelled ? <Badge text="Cancelled" tone="neutral" /> : <Badge text={PAY_BADGE[status].text} tone={PAY_BADGE[status].tone} />}
            {linked && !sale.cancelled ? <Badge text={sb.text} tone={sb.tone} icon="cloud-upload-outline" /> : null}
            {sale.revision > 0 ? <Badge text={`Edited ${sale.revision}×`} tone="info" /> : null}
            {sale.payload.corrects ? <Badge text={`Corrects ${sale.payload.corrects}`} tone="info" /> : null}
          </View>

          {sale.cancelled ? <Card style={{ backgroundColor: c.surfaceAlt }}><T v="strong">This invoice was cancelled.</T><T v="small" muted>It stays here for the record and no longer counts towards sales or what&apos;s owed.</T></Card> : null}
          {sale.state === "failed" ? (
            <Card style={{ backgroundColor: c.negativeSoft, borderColor: c.negative, gap: 6 }}>
              <T v="strong" color={c.negative}>The server could not accept this invoice</T>
              <Body>{sale.error ?? "Unknown error."}</Body>
              <Body small muted>Nothing was lost: it is still on this phone. Fix the cause (for example stock, or edit the invoice), then retry.</Body>
            </Card>
          ) : null}
          {sale.state === "pending" && sale.error ? <T v="small" color={c.warning}>Last attempt: {sale.error}</T> : null}
          {totalMismatch(sale) ? <Card style={{ backgroundColor: c.warningSoft, borderColor: c.warning }}><T v="strong" color={c.warning}>Total differs from the server</T><T v="small" color={c.warning}>The server booked {m(parseMinor(sale.total!))}; this phone issued {m(parseMinor(sale.payload.calc!.total))}. The server&apos;s figure is the one that counts.</T></Card> : null}

          <Card style={{ padding: 0 }}>
            {sale.payload.lines.map((l, i) => {
              const cl = calc.lines[i];
              return (
                <View key={`${l.variantId}-${i}`}>
                  <View style={{ padding: space.lg, flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
                    <View style={{ flex: 1 }}>
                      <T v="strong">{l.name}</T>
                      <T v="small" muted>{l.qty} {l.unitLabel ?? ""} × {l.price}{calc.mode === "INCLUSIVE" ? " (incl. GST)" : ""}{l.gstRate && Number(l.gstRate) > 0 ? ` · GST ${l.gstRate}%` : ""}{Number(l.discount) > 0 ? ` · discount ${l.discount}` : ""}</T>
                    </View>
                    <T v="money">{m(cl?.grossMinor ?? 0n)}</T>
                  </View>
                  {i < sale.payload.lines.length - 1 ? <Divider /> : null}
                </View>
              );
            })}
          </Card>

          <Card style={{ gap: 8 }}>
            <KV k="Taxable value" v={m(calc.taxableMinor)} />
            {calc.components.map((x) => <KV key={x.label} k={x.label} v={m(x.minor)} />)}
            <KV k="Total" v={m(total)} strong />
            {credited > 0n ? <KV k="Credit notes" v={`-${m(credited)}`} /> : null}
            <KV k="Received" v={m(paid)} />
            <KV k="Balance due" v={m(due)} />
            {refund > 0n ? <KV k="Owed back to customer" v={m(refund)} /> : null}
            <KV k="Made" v={new Date(sale.created_at).toLocaleString()} />
            {sale.due_date ? <KV k="Due by" v={new Date(sale.due_date).toLocaleDateString()} /> : null}
            {sale.payload.note ? <KV k="Note" v={sale.payload.note} /> : null}
          </Card>

          {pays.length > 0 ? (
            <View style={{ gap: 8 }}>
              <Label>Payments on this invoice</Label>
              <Card style={{ padding: 0 }}>
                {pays.map((p, i) => {
                  const gone = p.reversed_at != null || p.state === "void";
                  return (
                    <View key={p.id}>
                      <View style={{ padding: space.lg, flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
                        <View style={{ flex: 1 }}>
                          <T v="strong" style={gone ? { textDecorationLine: "line-through", color: c.muted } : undefined}>{m(parseMinor(p.amount))} · {p.method.replace("_", " ").toLowerCase()}</T>
                          <T v="small" muted>{new Date(p.paid_at).toLocaleString()}{gone ? ` · reversed: ${p.reversal_reason}` : p.state === "pending" && linked ? " · waiting to sync" : ""}</T>
                        </View>
                      </View>
                      {i < pays.length - 1 ? <Divider /> : null}
                    </View>
                  );
                })}
              </Card>
              <Tap label="Manage payments for this customer" onPress={() => openCustomer(sale.payload.customerId)} style={{ minHeight: 40, justifyContent: "center" }}><T v="small" color={c.accent} style={{ fontFamily: fonts.bodySemi }}>Reverse a payment or see the customer&apos;s account →</T></Tap>
            </View>
          ) : null}

          {notes.length > 0 ? (
            <View style={{ gap: 8 }}>
              <Label>Credit notes on this invoice</Label>
              <Card style={{ padding: 0 }}>
                {notes.map((n, i) => (
                  <View key={n.local_id}>
                    <View style={{ padding: space.lg, flexDirection: "row", justifyContent: "space-between", alignItems: "center", gap: 12 }}>
                      <View style={{ flex: 1 }}>
                        <T v="strong">{n.state === "issued" ? `${n.number} · -${m(parseMinor(n.total))}` : "Not issued yet"}</T>
                        <T v="small" muted>{new Date(n.created_at).toLocaleDateString()} · {n.payload.goodsReturned ? "goods returned" : "money only"} · {n.reason}</T>
                        {n.state !== "issued" ? <T v="small" color={c.warning}>The server has not issued it. Open Credit / return and tap Issue again.</T> : null}
                      </View>
                      {n.state === "issued" ? <Tap label={`Share credit note ${n.number}`} onPress={() => { if (db && profile) void shareCredit(db, n.local_id, profile, currency).catch(() => setMsg("Couldn't create the PDF. Try again.")); }} style={{ width: 44, height: 44, alignItems: "center", justifyContent: "center" }}><Icon name="share-outline" size={22} color={c.accent} /></Tap> : null}
                    </View>
                    {i < notes.length - 1 ? <Divider /> : null}
                  </View>
                ))}
              </Card>
            </View>
          ) : null}

          <View style={{ gap: 8 }}>
            <Label>Invoice design</Label>
            <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8 }}>
              {TEMPLATES.map((t) => <Chip key={t.key} title={t.label} active={tpl === t.key} onPress={() => setTemplate(t.key)} />)}
            </ScrollView>
          </View>
          <Button title={`Share ${invoiceTitle({ ...(profile ?? {}), gstRegistered: !!org?.gstStateCode }) === "BILL OF SUPPLY" ? "bill of supply" : "invoice"} (PDF)`} icon="share-outline" onPress={() => void share()} busy={sharing} />
          {!sale.cancelled ? <Button title="Share delivery note (PDF, no prices)" icon="cube-outline" kind="quiet" onPress={() => { if (db && profile) void shareDeliveryNote(db, sale.local_id, profile, currency).catch(() => setMsg("Couldn't create the PDF. Try again.")); }} /> : null}
          {msg ? <T v="small" color={c.negative}>{msg}</T> : null}

          {!sale.cancelled && due > 0n && ledger ? <Button title="Record payment" icon="cash-outline" kind="quiet" onPress={() => setPaying(true)} /> : null}
          {editable ? <Button title="Edit invoice" icon="create-outline" kind="quiet" onPress={startEdit} /> : null}
          {correctable ? <Button title="Correct invoice" icon="swap-horizontal-outline" kind="quiet" disabled={!linked || !online} onPress={() => setAsk("correct")} /> : null}
          {correctable ? <Button title="Credit / return items" icon="return-down-back-outline" kind="quiet" disabled={!linked || !online} onPress={() => setCrediting(true)} /> : null}
          {correctable && (!linked || !online) ? <T v="small" muted>Correcting an invoice the server holds needs a connection, so the cancellation is recorded there too.</T> : null}
          {!sale.cancelled && !sale.progress.docId && sale.state !== "syncing" ? <Button title="Cancel invoice" icon="close-circle-outline" kind="danger" onPress={() => setAsk("cancel")} /> : null}

          {linked && sale.state === "failed" ? (
            <View style={{ gap: 10 }}>
              <Button title="Retry" kind="quiet" busy={syncing} onPress={async () => { await retrySale(db!, sale.local_id); void syncNow(); }} />
              <Button title="Discard this invoice" kind="danger" onPress={() => Alert.alert("Discard this invoice?", "It will be removed from this phone and will not be sent to the server.", [{ text: "Keep", style: "cancel" }, { text: "Discard", style: "destructive", onPress: async () => { await discardSale(db!, sale.local_id); changed(); onClose(); } }])} />
            </View>
          ) : linked && sale.state === "pending" && !sale.cancelled ? (
            <Button title={online ? "Sync now" : "Waiting for a connection"} kind="quiet" disabled={!online} busy={syncing} onPress={() => void syncNow()} />
          ) : null}

          {revisions.length > 0 ? (
            <View style={{ gap: 6 }}>
              <Label>Edit history</Label>
              {revisions.map((r) => <T key={r.id} v="small" muted>Version {r.revision + 1} replaced {new Date(r.edited_at).toLocaleString()} · {r.reason}</T>)}
            </View>
          ) : null}
          <Label>Reference</Label><T v="small" muted>{`mobile:${sale.local_id}`}</T>
        </View>
      </ScrollView>
      {paying && ledger ? <PaymentSheet ledger={ledger} invoiceId={sale.local_id} onClose={() => setPaying(false)} onSaved={() => { setPaying(false); changed(); }} /> : null}
      {crediting ? (
        <CreditSheet sale={sale} notes={notes} currency={currency} onClose={() => setCrediting(false)}
          onIssue={async (picks, goods, reason, retryId) => { await creditInvoice(sale.local_id, picks, goods, reason, retryId); changed(); }} />
      ) : null}
      {ask ? (
        <ReasonSheet
          title={ask === "correct" ? "Correct this invoice?" : "Cancel this invoice?"}
          body={ask === "correct"
            ? `${sale.doc_number ?? sale.local_number} will be cancelled on the server (its stock goes back and any payments are reversed, each with a record), and you'll issue a corrected one right away. The old invoice stays visible, marked cancelled.`
            : "It was never sent to the server. It will be marked cancelled, its stock put back and any payment on it voided."}
          action={ask === "correct" ? "Cancel and re-issue" : "Cancel invoice"}
          onClose={() => setAsk(null)}
          onConfirm={async (reason) => {
            if (!db) return;
            if (ask === "cancel") { await cancelLocalSale(db, sale.local_id, reason, Date.now()); changed(); setAsk(null); return; }
            try {
              const r = await correctInvoice(sale.local_id, reason);
              cartActions.load(sale.payload, { corrects: { number: r.number, owedBack: minorToString(r.receivedMinor) } });
              if (r.receivedMinor > 0n) cartActions.patch({ payMethod: r.method ?? "CASH", receivedText: minorToString(r.receivedMinor) });
              cartActions.patch({ customerId: sale.payload.customerId, customerName: sale.payload.customerName });
              changed(); setAsk(null); goSell();
            } catch (e) { throw new Error(e instanceof CorrectionError || e instanceof Error ? e.message : "Couldn't correct the invoice. Nothing was lost."); }
          }}
        />
      ) : null}
    </Screen>
  );
}

/** Pick what is being credited. Quantities start at 0; nothing is sent until "Issue credit note". */
function CreditSheet({ sale, notes, currency, onClose, onIssue }: { sale: Sale; notes: CreditNote[]; currency: string; onClose: () => void; onIssue: (picks: ReturnPick[], goods: boolean, reason: string, retryId: string) => Promise<void> }) {
  const c = usePalette();
  // A credit note the server has not issued yet is resumed (same id, same lines), never duplicated.
  const pending = notes.find((n) => n.state === "pending");
  const [retryId] = useState(() => pending?.local_id ?? uid());
  const done = creditedQty(notes);
  const [qty, setQty] = useState<Record<number, string>>(() => Object.fromEntries((pending?.payload.lines ?? []).map((l) => [l.lineIndex ?? 0, l.qty])));
  const [goods, setGoods] = useState(pending?.payload.goodsReturned ?? true);
  const [why, setWhy] = useState(pending?.reason ?? "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);
  const picks: ReturnPick[] = Object.entries(qty).filter(([, v]) => v && dec(v) > 0n).map(([i, v]) => ({ index: Number(i), qty: v }));
  return (
    <Modal visible animationType="slide" onRequestClose={onClose}>
      <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
        <Header title="Credit / return" onBack={onClose} sub={`${sale.doc_number ?? sale.local_number} · ${sale.payload.customerName}`} />
        <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: 60 }} keyboardShouldPersistTaps="handled">
          <T v="small" muted>The server issues a numbered credit note that lowers what this customer owes. The original invoice is not changed.</T>
          <Card style={{ padding: 0 }}>
            {sale.payload.lines.map((l, i) => {
              const left = dec(l.qty) - (done.get(i) ?? 0n);
              return (
                <View key={`${l.variantId}-${i}`}>
                  <View style={{ padding: space.lg, flexDirection: "row", alignItems: "center", gap: 12 }}>
                    <View style={{ flex: 1 }}>
                      <T v="strong">{l.name}</T>
                      <T v="small" muted>{left > 0n ? `${decToString(left)} of ${l.qty} ${l.unitLabel ?? ""} can still be credited` : "Fully credited"} · {l.price} each</T>
                    </View>
                    <TextInput accessibilityLabel={`Quantity of ${l.name} to credit`} editable={left > 0n} value={qty[i] ?? ""} placeholder="0" placeholderTextColor={c.faint} keyboardType="decimal-pad"
                      onChangeText={(t) => setQty({ ...qty, [i]: t.replace(",", ".").replace(/[^0-9.]/g, "") })}
                      style={{ width: 80, minHeight: 44, borderRadius: radius.sm + 2, borderWidth: 1, borderColor: c.border, color: c.text, textAlign: "center", fontSize: 17, fontFamily: fonts.mono }} />
                  </View>
                  {i < sale.payload.lines.length - 1 ? <Divider /> : null}
                </View>
              );
            })}
          </Card>
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Chip title="Goods came back" active={goods} onPress={() => setGoods(true)} />
            <Chip title="Money only" active={!goods} onPress={() => setGoods(false)} />
          </View>
          <T v="small" muted>{goods ? "Stock goes back up by the quantities above." : "Stock is not changed (for example a price adjustment or a damaged item you did not take back)."}</T>
          <Field label="Reason (required)" value={why} onChangeText={setWhy} />
          {err ? <T v="small" color={c.negative}>{err}</T> : null}
          {ok ? <Card style={{ backgroundColor: c.positiveSoft, borderColor: c.positive }}><T v="strong" color={c.positive}>{ok}</T></Card> : null}
          {ok ? <Button title="Done" onPress={onClose} /> : (
            <Button title={picks.length ? `Issue credit note (${picks.length} item${picks.length === 1 ? "" : "s"})` : "Issue credit note"} busy={busy} disabled={picks.length === 0 || !why.trim()}
              onPress={() => {
                setBusy(true); setErr(null);
                onIssue(picks, goods, why, retryId).then(() => setOk("Credit note issued by the server. The balance has been updated.")).catch((e: unknown) =>
                  setErr(e instanceof CreditNoteError || e instanceof Error ? e.message : "Couldn't issue it. Nothing was lost; try again.")).finally(() => setBusy(false));
              }} />
          )}
          <T v="small" muted>If the connection drops, tap again: the same request is resumed and cannot issue a second credit note.</T>
        </ScrollView>
      </SafeAreaView>
    </Modal>
  );
}

function ReasonSheet({ title, body, action, onClose, onConfirm }: { title: string; body: string; action: string; onClose: () => void; onConfirm: (reason: string) => Promise<void> }) {
  const c = usePalette();
  const [why, setWhy] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  return (
    <Modal visible transparent animationType="fade" onRequestClose={onClose}>
      <SafeAreaView style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.5)", justifyContent: "center", padding: space.lg }}>
        <View style={{ backgroundColor: c.surface, borderRadius: radius.lg + 4, padding: space.xl, gap: 14 }}>
          <T v="h2">{title}</T>
          <T v="body" muted>{body}</T>
          <Field label="Reason (required)" value={why} onChangeText={setWhy} autoFocus />
          {err ? <T v="small" color={c.negative}>{err}</T> : null}
          <View style={{ flexDirection: "row", gap: 10 }}>
            <Button style={{ flex: 1 }} kind="quiet" title="Back" onPress={onClose} />
            <Button style={{ flex: 1 }} kind="danger" title={action} busy={busy} disabled={!why.trim()} onPress={() => { setBusy(true); setErr(null); onConfirm(why).catch((e: unknown) => setErr(e instanceof Error ? e.message : "Failed.")).finally(() => setBusy(false)); }} />
          </View>
        </View>
      </SafeAreaView>
    </Modal>
  );
}

function KV({ k, v, strong }: { k: string; v: string; strong?: boolean }) {
  return <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}><T v="small" muted>{k}</T><T v={strong ? "moneyLg" : "money"} style={{ fontSize: strong ? 18 : 14, flexShrink: 1, textAlign: "right" }}>{v}</T></View>;
}
export { livePayments };
