import React, { useState } from "react";
import { Alert, FlatList, Modal, ScrollView, TextInput, View } from "react-native";
import { discardSale, getSale, listSales, recentServerDocs, retrySale, recordSalePayment, updateSaleLines, type PaymentEntry, type Sale, type SaleLine, type ServerDoc } from "../db/repo";
import { useLocal } from "../lib/hooks";
import { formatMoney, lineAmount, num, round2 } from "../lib/money";
import { shareInvoice } from "../lib/share";
import { calcTotals, exclusiveUnitPrice } from "../lib/tax";
import { outstandingAmount, saleAmount } from "../lib/receivables";
import { useApp } from "../state/app";
import { fonts, space, usePalette } from "../theme";
import { Badge, Body, Button, Card, Chip, Divider, Empty, Field, Header, Label, Screen, Tap } from "../ui/kit";
import { T } from "../ui/text";

type Tone = "good" | "warn" | "bad" | "info" | "neutral";
export function stateBadge(s: Sale, linked: boolean): { text: string; tone: Tone } {
  if (!linked && s.state !== "synced") return { text: s.payload.paymentMethod ? "Paid" : "Unpaid", tone: s.payload.paymentMethod ? "good" : "warn" };
  if (s.state === "synced") return { text: s.doc_number ?? "Synced", tone: "good" };
  if (s.state === "failed") return { text: "Needs attention", tone: "bad" };
  if (s.state === "syncing") return { text: "Syncing…", tone: "info" };
  return { text: "Waiting to sync", tone: "warn" };
}
const saleTotal = (s: Sale): number => saleAmount(s);

export function SaleRow({ sale, last, onPress }: { sale: Sale; last?: boolean; onPress: () => void }) {
  const { currency, linked } = useApp();
  const c = usePalette();
  const b = stateBadge(sale, linked);
  return (
    <View>
      <Tap label={`${sale.payload.customerName} ${formatMoney(saleTotal(sale), currency)}`} onPress={onPress} style={{ minHeight: 72, padding: space.lg, gap: 6 }}>
        <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
          <T v="strong" numberOfLines={1} style={{ flex: 1 }}>{sale.payload.customerName}</T>
          <T v="money" style={{ fontFamily: fonts.monoBold }}>{formatMoney(saleTotal(sale), currency)}</T>
        </View>
        <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
          <Badge text={b.text} tone={b.tone} />
          <T v="small" muted>{sale.local_number && !sale.doc_number ? `${sale.local_number} · ` : ""}{new Date(sale.created_at).toLocaleString(undefined, { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" })}</T>
        </View>
      </Tap>
      {last ? null : <Divider />}
    </View>
  );
}

type Filter = "all" | "unpaid" | "waiting" | "attention";

export function Sales({ open }: { open: (id: string) => void }) {
  const { currency, counts, linked } = useApp();
  const c = usePalette();
  const [f, setF] = useState<Filter>("all");
  const local = useLocal((db) => listSales(db), []);
  const server = useLocal((db) => recentServerDocs(db), [] as ServerDoc[]);
  const mine = new Set(local.map((s) => s.progress.docId).filter(Boolean));
  const shown = local.filter((s) => f === "all" ? true : f === "unpaid" ? outstandingAmount(s) > 0.004 : f === "waiting" ? s.state === "pending" || s.state === "syncing" : s.state === "failed");
  const others = f === "all" ? server.filter((d) => !mine.has(d.id)) : [];
  return (
    <Screen>
      <Header title="Sales" />
      <View style={{ paddingHorizontal: space.lg, paddingBottom: space.md }}>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8 }}>
          <Chip title="All" active={f === "all"} onPress={() => setF("all")} />
          <Chip title="To collect" active={f === "unpaid"} onPress={() => setF("unpaid")} />
          {linked ? <Chip title={`Waiting${counts.pending + counts.syncing ? ` (${counts.pending + counts.syncing})` : ""}`} active={f === "waiting"} onPress={() => setF("waiting")} /> : null}
          {linked ? <Chip title={`Attention${counts.failed ? ` (${counts.failed})` : ""}`} active={f === "attention"} onPress={() => setF("attention")} /> : null}
        </ScrollView>
      </View>
      <FlatList
        data={[...shown.map((s) => ({ kind: "local" as const, s })), ...others.map((d) => ({ kind: "server" as const, d }))]}
        keyExtractor={(x) => (x.kind === "local" ? x.s.local_id : x.d.id)}
        contentContainerStyle={{ paddingHorizontal: space.lg, paddingBottom: 32 }}
        ListEmptyComponent={<Empty icon="receipt-outline" title={f === "all" ? "No sales yet" : "Nothing here"} hint={f === "all" ? "Invoices you make appear here." : undefined} />}
        renderItem={({ item }) =>
          item.kind === "local" ? (
            <Card style={{ padding: 0, marginBottom: 10 }}><SaleRow sale={item.s} last onPress={() => open(item.s.local_id)} /></Card>
          ) : (
            <Card style={{ marginBottom: 10, gap: 4 }}>
              <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
                <T v="strong">{item.d.number}</T>
                <T v="money" style={{ fontFamily: fonts.monoBold }}>{item.d.total ? formatMoney(num(item.d.total), currency) : "—"}</T>
              </View>
              <T v="small" muted>From the server · {item.d.issue_date.slice(0, 10)} · {item.d.status.toLowerCase()}</T>
            </Card>
          )
        }
      />
    </Screen>
  );
}

export function SaleDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const { db, syncNow, syncing, online, linked, currency, profile, changed } = useApp();
  const c = usePalette();
  const sale = useLocal((d) => getSale(d, id), null as Sale | null, [id]);
  const party = useLocal((d) => d.first<{ gstin: string }>("SELECT gstin FROM parties WHERE id=(SELECT json_extract(payload,'$.customerId') FROM sales WHERE local_id=?)", [id]), null as { gstin: string } | null, [id]);
  const [sharing, setSharing] = useState(false);
  const [editing, setEditing] = useState(false);
  const [editLines, setEditLines] = useState<SaleLine[]>([]);
  const [paymentOpen, setPaymentOpen] = useState(false);
  const [paymentAmount, setPaymentAmount] = useState("");
  const [paymentMethod, setPaymentMethod] = useState("CASH");
  if (!sale) return <Screen><Header title="Sale" onBack={onClose} /><Empty title="Sale not found" /></Screen>;
  const b = stateBadge(sale, linked);
  const t = calcTotals(sale.payload.lines);
  const total = saleAmount(sale);
  const due = outstandingAmount(sale);
  const canEdit = !linked && sale.state !== "synced" && !sale.progress.docId;
  const editInvalid = editLines.some((l) => !/^\\d+(\\.\\d{1,4})?$/.test(l.qty) || num(l.qty) <= 0 || num(l.grossPrice ?? l.price) <= 0);

  async function share() {
    if (!profile || !sale) return;
    setSharing(true);
    try {
      await shareInvoice({ number: sale.doc_number ?? sale.local_number ?? "Invoice", date: sale.created_at, business: profile, customerName: sale.payload.customerName, customerGstin: party?.gstin, lines: sale.payload.lines, paymentMethod: sale.payload.paymentMethod, currency, serverTotal: sale.total, payments: sale.payload.payments, theme: profile.invoiceTheme });
    } finally { setSharing(false); }
  }

  return (
    <Screen>
      <ScrollView contentContainerStyle={{ paddingBottom: 40 }}>
        <Header title={sale.payload.customerName} onBack={onClose} />
        <View style={{ padding: space.lg, gap: space.lg, paddingTop: space.xs }}>
          <View style={{ flexDirection: "row", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
            <Badge text={b.text} tone={b.tone} />
            <T v="small" muted>{sale.doc_number ?? sale.local_number}</T>
          </View>

          {sale.state === "failed" ? (
            <Card style={{ backgroundColor: c.negativeSoft, borderColor: c.negative, gap: 6 }}>
              <T v="strong" color={c.negative}>The server could not accept this sale</T>
              <Body>{sale.error ?? "Unknown error."}</Body>
              <Body small muted>Nothing was lost: the sale is still on this phone. Fix the cause (for example stock), then retry.</Body>
            </Card>
          ) : null}
          {sale.state === "pending" && sale.error ? <T v="small" color={c.warning}>Last attempt: {sale.error}</T> : null}

          <Card style={{ padding: 0 }}>
            {(editing ? editLines : sale.payload.lines).map((l, i) => (
              <View key={l.variantId}>
                <View style={{ padding: space.lg, flexDirection: "row", justifyContent: "space-between", gap: 12, alignItems: "center" }}>
                  <View style={{ flex: 1, gap: 6 }}><T v="strong">{l.name}</T>
                    {editing ? <View style={{ flexDirection: "row", gap: 8 }}>
                      <TextInput accessibilityLabel={`Quantity of ${l.name}`} keyboardType="decimal-pad" value={l.qty} onChangeText={(v) => setEditLines((xs) => xs.map((x) => x.variantId === l.variantId ? { ...x, qty: v.replace(/[^0-9.]/g, "") } : x))} style={{ width: 76, minHeight: 44, borderWidth: 1, borderColor: c.border, color: c.text, borderRadius: 8, paddingHorizontal: 8 }} />
                      <TextInput accessibilityLabel={`Price of ${l.name}`} keyboardType="decimal-pad" value={l.grossPrice ?? l.price} onChangeText={(v) => setEditLines((xs) => xs.map((x) => x.variantId === l.variantId ? (x.grossPrice != null ? { ...x, grossPrice: v.replace(/[^0-9.]/g, "") } : { ...x, price: v.replace(/[^0-9.]/g, "") }) : x))} style={{ flex: 1, minHeight: 44, borderWidth: 1, borderColor: c.border, color: c.text, borderRadius: 8, paddingHorizontal: 8 }} />
                    </View> : <T v="small" muted>{l.qty} × {formatMoney(num(l.grossPrice ?? l.price), currency)}{l.gstRate ? ` · GST ${l.gstRate}%` : ""}</T>}
                  </View>
                  <T v="money">{formatMoney(l.grossPrice != null ? round2(num(l.qty) * num(l.grossPrice) - num(l.discount)) : lineAmount(l), currency)}</T>
                </View>
                {i < (editing ? editLines : sale.payload.lines).length - 1 ? <Divider /> : null}
              </View>
            ))}
          </Card>
          {editing ? <View style={{ flexDirection: "row", gap: 10 }}><Button title="Save invoice changes" icon="checkmark" disabled={editInvalid} style={{ flex: 1 }} onPress={async () => { try { await updateSaleLines(db!, sale.local_id, editLines.map((l) => l.grossPrice != null ? { ...l, price: String(exclusiveUnitPrice(l.grossPrice, l.gstRate)) } : l)); changed(); setEditing(false); } catch (e) { Alert.alert("Could not save", e instanceof Error ? e.message : "Try again."); } }} /><Button title="Cancel" kind="quiet" style={{ flex: 1 }} onPress={() => setEditing(false)} /></View> : canEdit ? <Button title="Edit invoice" kind="quiet" icon="create-outline" onPress={() => { setEditLines(sale.payload.lines.map((l) => ({ ...l }))); setEditing(true); }} /> : null>

          <Card style={{ gap: 8 }}>
            <KV k="Subtotal" v={formatMoney(t.subtotal, currency)} />
            {t.tax > 0 && !sale.total ? <KV k="GST" v={formatMoney(t.tax, currency)} /> : null}
            <KV k={sale.total ? "Total with tax" : "Total"} v={formatMoney(total, currency)} strong />
            <KV k="Paid" v={formatMoney(total - due, currency)} />
            <KV k="Amount due" v={formatMoney(due, currency)} strong={due > 0} />
            <KV k="Payment" v={sale.payload.paymentMethod ? sale.payload.paymentMethod.toLowerCase() : "credit"} />
            <KV k="Made" v={new Date(sale.created_at).toLocaleString()} />
            {(sale.payload.payments ?? []).map((p, i) => <KV key={i} k={`Payment ${i + 1} · ${p.method}`} v={formatMoney(num(p.amount), currency)} />)}
          </Card>
          {due > 0.004 ? <Button title="Record a payment" icon="wallet-outline" kind="quiet" onPress={() => { setPaymentAmount(due.toFixed(2)); setPaymentOpen(true); }} /> : null}
          {linked && sale.state === "synced" && (sale.payload.payments?.length ?? 0) > 0 ? <T v="small" muted>Payments are added to this invoice and synced to your Rechvix server.</T> : null}

          <Button title="Share invoice (PDF)" icon="share-outline" onPress={() => void share()} busy={sharing} />
          <Modal visible={paymentOpen} animationType="slide" transparent onRequestClose={() => setPaymentOpen(false)}>
            <View style={{ flex: 1, justifyContent: "flex-end", backgroundColor: "rgba(0,0,0,0.35)" }}>
              <View style={{ backgroundColor: c.bg, borderTopLeftRadius: 24, borderTopRightRadius: 24, padding: space.lg, gap: space.lg }}>
                <Header title="Record payment" onBack={() => setPaymentOpen(false)} />
                <T v="small" muted>Outstanding balance: {formatMoney(due, currency)}</T>
                <Field label="Amount received" value={paymentAmount} onChangeText={setPaymentAmount} keyboardType="decimal-pad" />
                <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>{["CASH", "UPI", "CARD", "BANK_TRANSFER"].map((m) => <Chip key={m} title={m === "BANK_TRANSFER" ? "Bank transfer" : m} active={paymentMethod === m} onPress={() => setPaymentMethod(m)} />)}</View>
                <Button title="Save payment" disabled={num(paymentAmount) <= 0 || num(paymentAmount) > due + 0.004} onPress={async () => { const entry: PaymentEntry = { amount: round2(num(paymentAmount)).toFixed(2), method: paymentMethod, recordedAt: Date.now() }; await recordSalePayment(db!, sale.local_id, entry); changed(); setPaymentOpen(false); }} />
              </View>
            </View>
          </Modal>

          {linked && sale.state === "failed" ? (
            <View style={{ gap: 10 }}>
              <Button title="Retry" kind="quiet" busy={syncing} onPress={async () => { await retrySale(db!, sale.local_id); void syncNow(); }} />
              <Button title="Discard this sale" kind="danger" onPress={() => Alert.alert("Discard this sale?", "It will be removed from this phone and will not be sent to the server.", [{ text: "Keep", style: "cancel" }, { text: "Discard", style: "destructive", onPress: async () => { await discardSale(db!, sale.local_id); onClose(); } }])} />
            </View>
          ) : linked && sale.state === "pending" ? (
            <Button title={online ? "Sync now" : "Waiting for a connection"} kind="quiet" disabled={!online} busy={syncing} onPress={() => void syncNow()} />
          ) : null}
          <Label>Reference</Label><T v="small" muted>{`mobile:${sale.local_id}`}</T>
        </View>
      </ScrollView>
    </Screen>
  );
}

function KV({ k, v, strong }: { k: string; v: string; strong?: boolean }) {
  return <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}><T v="small" muted>{k}</T><T v={strong ? "moneyLg" : "money"} style={{ fontSize: strong ? 18 : 14, flexShrink: 1, textAlign: "right" }}>{v}</T></View>;
}
