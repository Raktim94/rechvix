import React, { useState } from "react";
import { Alert, FlatList, View } from "react-native";
import { getQuote, listQuotes, setQuoteStatus, type Quote, type QuoteStatus } from "../db/repo";
import { parseMinor } from "../lib/decimal";
import { useLocal, useNow } from "../lib/hooks";
import { formatMinor } from "../lib/money";
import { payloadCalc } from "../lib/sale";
import { shareQuote } from "../lib/share";
import { TEMPLATES, type InvoiceTemplate } from "../lib/invoice";
import { cartActions, cartIsEmpty, getCart } from "../state/cart";
import { useApp } from "../state/app";
import { space, usePalette } from "../theme";
import { Badge, Button, Card, Chip, Divider, Empty, Header, Icon, Label, Screen, Tap } from "../ui/kit";
import { T } from "../ui/text";

const TONE: Record<QuoteStatus, "info" | "good" | "neutral"> = { open: "info", converted: "good", declined: "neutral" };
const TEXT: Record<QuoteStatus, string> = { open: "Open", converted: "Became an invoice", declined: "Declined" };
const expired = (q: Quote, now: number) => q.status === "open" && q.valid_until != null && q.valid_until < now;

/** Quotations: priced offers kept on this phone. They never change stock or what anyone owes until they become an invoice. */
export function Quotes({ onBack, open }: { onBack: () => void; open: (id: string) => void }) {
  const { currency } = useApp();
  const c = usePalette();
  const quotes = useLocal((d) => listQuotes(d), [] as Quote[]);
  const [f, setF] = useState<"open" | "all">("open");
  const now = useNow();
  const shown = quotes.filter((q) => f === "all" || q.status === "open");
  return (
    <Screen>
      <Header title="Quotations" onBack={onBack} sub="Offers you have sent. Nothing here counts as a sale until it becomes an invoice." />
      <View style={{ flexDirection: "row", gap: 8, paddingHorizontal: space.lg, paddingBottom: space.sm }}>
        <Chip title="Open" active={f === "open"} onPress={() => setF("open")} />
        <Chip title="All" active={f === "all"} onPress={() => setF("all")} />
      </View>
      <FlatList data={shown} keyExtractor={(q) => q.local_id} contentContainerStyle={{ paddingBottom: 40 }}
        ListEmptyComponent={<Empty icon="document-text-outline" title={f === "open" ? "No open quotations" : "No quotations yet"} hint="On the New sale screen, add items and choose Save as quotation." />}
        ItemSeparatorComponent={Divider}
        renderItem={({ item: q }) => (
          <Tap label={`Quotation ${q.number} for ${q.payload.customerName}`} onPress={() => open(q.local_id)} style={{ minHeight: 68, flexDirection: "row", alignItems: "center", gap: 12, paddingHorizontal: space.lg }}>
            <View style={{ flex: 1 }}>
              <T v="strong" numberOfLines={1}>{q.payload.customerName}</T>
              <T v="small" muted>{q.number} · {new Date(q.created_at).toLocaleDateString()}{q.valid_until ? ` · valid until ${new Date(q.valid_until).toLocaleDateString()}` : ""}</T>
            </View>
            <View style={{ alignItems: "flex-end", gap: 4 }}>
              <T v="money">{formatMinor(parseMinor(q.total), currency)}</T>
              {expired(q, now) ? <Badge text="Expired" tone="warn" /> : <Badge text={TEXT[q.status]} tone={TONE[q.status]} />}
            </View>
            <Icon name="chevron-forward" size={18} color={c.faint} />
          </Tap>
        )} />
    </Screen>
  );
}

export function QuoteDetail({ id, onClose, goSell }: { id: string; onClose: () => void; goSell: () => void }) {
  const { db, profile, currency, changed } = useApp();
  const c = usePalette();
  const q = useLocal((d) => getQuote(d, id), null as Quote | null, [id]);
  const [tpl, setTpl] = useState<InvoiceTemplate | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<string | null>(null);
  const now = useNow();
  if (!q) return <Screen><Header title="Quotation" onBack={onClose} /><Empty title="Quotation not found" /></Screen>;
  const calc = payloadCalc(q.payload);
  const m = (x: bigint) => formatMinor(x, currency);
  const template = tpl ?? profile?.invoiceTemplate ?? "classic";

  async function share() {
    if (!db || !profile || !q) return;
    setBusy(true); setMsg(null);
    try { await shareQuote(db, q.local_id, profile, currency, template); } catch { setMsg("Couldn't create the PDF. Try again."); } finally { setBusy(false); }
  }
  function toInvoice() {
    const go = () => { cartActions.load(q!.payload, { fromQuote: { id: q!.local_id, number: q!.number } }); goSell(); };
    if (cartIsEmpty(getCart())) go();
    else Alert.alert("Replace the bill you're working on?", "You have a bill in progress. Making an invoice from this quotation will replace it.", [{ text: "Keep mine", style: "cancel" }, { text: "Replace", style: "destructive", onPress: go }]);
  }
  async function mark(status: QuoteStatus) { if (!db) return; await setQuoteStatus(db, q!.local_id, status); changed(); }

  return (
    <Screen>
      <FlatList data={[0]} keyExtractor={() => "x"} contentContainerStyle={{ paddingBottom: 40 }} renderItem={() => (
        <View>
          <Header title={q.payload.customerName} onBack={onClose} sub={q.number} />
          <View style={{ padding: space.lg, gap: space.lg, paddingTop: space.xs }}>
            <View style={{ flexDirection: "row", gap: 8 }}>
              <Badge text={expired(q, now) ? "Expired" : TEXT[q.status]} tone={expired(q, now) ? "warn" : TONE[q.status]} />
            </View>
            <Card style={{ padding: 0 }}>
              {q.payload.lines.map((l, i) => (
                <View key={`${l.variantId}-${i}`}>
                  <View style={{ padding: space.lg, flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
                    <View style={{ flex: 1 }}><T v="strong">{l.name}</T><T v="small" muted>{l.qty} {l.unitLabel ?? ""} × {l.price}{calc.mode === "INCLUSIVE" ? " (incl. GST)" : ""}</T></View>
                    <T v="money">{m(calc.lines[i]?.grossMinor ?? 0n)}</T>
                  </View>
                  {i < q.payload.lines.length - 1 ? <Divider /> : null}
                </View>
              ))}
            </Card>
            <Card style={{ gap: 8 }}>
              <Row k="Taxable value" v={m(calc.taxableMinor)} />
              {calc.components.map((x) => <Row key={x.label} k={x.label} v={m(x.minor)} />)}
              <Row k="Total" v={m(parseMinor(q.total))} strong />
              <Row k="Made" v={new Date(q.created_at).toLocaleDateString()} />
              {q.valid_until ? <Row k="Valid until" v={new Date(q.valid_until).toLocaleDateString()} /> : null}
            </Card>
            <Label>Design</Label>
            <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>{TEMPLATES.map((t) => <Chip key={t.key} title={t.label} active={template === t.key} onPress={() => setTpl(t.key)} />)}</View>
            <Button title="Share quotation (PDF)" icon="share-outline" onPress={() => void share()} busy={busy} />
            {q.status === "open" ? <Button title="Make an invoice from this" icon="receipt-outline" onPress={toInvoice} /> : null}
            {q.status === "open" ? <Button kind="quiet" title="Mark as declined" onPress={() => void mark("declined")} /> : null}
            {q.status === "declined" ? <Button kind="quiet" title="Reopen" onPress={() => void mark("open")} /> : null}
            {msg ? <T v="small" color={c.negative}>{msg}</T> : null}
          </View>
        </View>
      )} />
    </Screen>
  );
}

const Row = ({ k, v, strong }: { k: string; v: string; strong?: boolean }) => (
  <View style={{ flexDirection: "row", justifyContent: "space-between" }}><T v={strong ? "strong" : "small"} muted={!strong}>{k}</T><T v="money">{v}</T></View>
);

/** The moment after saving a quotation. */
export function QuoteDone({ info, currency, onNew }: { info: { id: string; number: string; totalMinor: bigint }; currency: string; onNew: () => void }) {
  const c = usePalette();
  const { db, profile } = useApp();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  async function share() {
    if (!db || !profile) return;
    setBusy(true); setErr(null);
    try { await shareQuote(db, info.id, profile, currency); } catch { setErr("Couldn't create the PDF. The quotation is saved; open it from More and try again."); } finally { setBusy(false); }
  }
  return (
    <Screen>
      <View style={{ flex: 1, padding: space.xl, justifyContent: "center", gap: space.xl }}>
        <View style={{ alignItems: "center", gap: 12 }}>
          <View style={{ width: 88, height: 88, borderRadius: 44, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center" }}><Icon name="document-text-outline" size={46} color={c.accent} /></View>
          <T v="h2">Quotation saved</T>
          <T v="moneyHero" style={{ fontSize: 34 }}>{formatMinor(info.totalMinor, currency)}</T>
          <T v="body" muted>{info.number} · valid for 30 days</T>
          <T v="small" muted style={{ textAlign: "center" }}>It is not a sale: stock and balances are unchanged until you make an invoice from it.</T>
        </View>
        {err ? <T v="small" color={c.negative} style={{ textAlign: "center" }}>{err}</T> : null}
        <View style={{ gap: 10 }}>
          <Button title="Share quotation (PDF)" icon="share-outline" onPress={() => void share()} busy={busy} />
          <Button title="New sale" kind="quiet" icon="add" onPress={onNew} />
        </View>
      </View>
    </Screen>
  );
}
