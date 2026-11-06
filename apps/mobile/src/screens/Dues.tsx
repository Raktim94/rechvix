import React, { useState } from "react";
import { Linking, Modal, ScrollView, Share, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import {
  addReminder, allPayments, getParty, insertPayments, listSales, remindersFor, reversePayment, serverBalances,
  type Party, type Payment,
} from "../db/repo";
import { minorToString, parseMinor, toMinor, dec } from "../lib/decimal";
import { nowMs, useLocal, useNow } from "../lib/hooks";
import { allocatePayment, ageBucket, daysOverdue, dueOf, dueState, reminderText } from "../lib/ledger";
import { formatMinor } from "../lib/money";
import { buildLedgers, owing, type CustomerLedger } from "../lib/receivables";
import { livePayments } from "../lib/sale";
import { uid, useApp } from "../state/app";
import { fonts, radius, space, usePalette } from "../theme";
import { Badge, Button, Card, Chip, Divider, Empty, Field, Header, Icon, Label, Screen, Tap } from "../ui/kit";
import { T } from "../ui/text";

const METHODS = [{ k: "CASH", t: "Cash" }, { k: "UPI", t: "UPI" }, { k: "CARD", t: "Card" }, { k: "BANK_TRANSFER", t: "Bank" }];

export function useLedgers(): CustomerLedger[] {
  const sales = useLocal((db) => listSales(db, 5000), []);
  const pays = useLocal((db) => allPayments(db), [] as Payment[]);
  const now = useNow();
  return buildLedgers(sales.filter((s) => !s.cancelled), pays, now);
}

type DueFilter = "due" | "overdue" | "settled" | "all";

/** "To collect": who owes what, oldest and most overdue first, with search and filters. */
export function Dues({ open }: { open: (partyId: string) => void }) {
  const { currency, linked } = useApp();
  const c = usePalette();
  const ledgers = useLedgers();
  const parties = useLocal((db) => db.all<Party>("SELECT * FROM parties"), [] as Party[]);
  const phoneOf = new Map(parties.map((p) => [p.id, p.phone]));
  const [f, setF] = useState<DueFilter>("due");
  const [q, setQ] = useState("");
  const [by, setBy] = useState<"amount" | "recent">("amount");
  const m = (x: bigint) => formatMinor(x, currency);
  const owes = owing(ledgers);
  const total = owes.reduce((n, l) => n + l.balance.owedMinor, 0n);
  const overdue = owes.reduce((n, l) => n + l.balance.overdueMinor, 0n);
  const server = useLocal((db) => serverBalances(db), []);
  const serverTotal = server.reduce((n, b) => n + parseMinor(b.total), 0n);
  const needle = q.trim().toLowerCase();
  const list = ledgers
    .filter((l) => f === "all" ? true : f === "due" ? l.balance.owedMinor > 0n : f === "overdue" ? l.balance.overdueMinor > 0n : l.balance.owedMinor === 0n)
    .filter((l) => !needle || l.name.toLowerCase().includes(needle) || (phoneOf.get(l.partyId) ?? "").includes(needle))
    .sort((a, z) => by === "recent" ? z.lastBilledAt - a.lastBilledAt : (z.balance.overdueMinor > 0n ? 1 : 0) - (a.balance.overdueMinor > 0n ? 1 : 0) || (z.balance.owedMinor > a.balance.owedMinor ? 1 : z.balance.owedMinor < a.balance.owedMinor ? -1 : 0));
  return (
    <Screen>
      <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: 40 }} keyboardShouldPersistTaps="handled">
        <T v="title">To collect</T>
        <View style={{ backgroundColor: total > 0n ? c.warningSoft : c.positiveSoft, borderRadius: radius.lg + 4, padding: space.xl, gap: 6 }}>
          <T v="label" color={total > 0n ? c.warning : c.positive}>Customers owe you</T>
          <T v="moneyHero" adjustsFontSizeToFit numberOfLines={1} accessibilityLabel={`Total to collect ${m(total)}`}>{m(total)}</T>
          <T v="small" muted>{owes.length} customer{owes.length === 1 ? "" : "s"}{overdue > 0n ? ` · ${m(overdue)} is past its due date` : ""}</T>
          <T v="small" muted>From invoices made on this phone.{linked && serverTotal > 0n ? ` Across the whole business, the server shows ${m(serverTotal)} outstanding.` : ""}</T>
        </View>
        <View style={{ flexDirection: "row", alignItems: "center", backgroundColor: c.surface, borderRadius: radius.md + 2, borderWidth: 1, borderColor: c.border, paddingLeft: 14 }}>
          <Icon name="search" size={18} color={c.faint} />
          <TextInput accessibilityLabel="Search customers" value={q} onChangeText={setQ} placeholder="Name or phone" placeholderTextColor={c.faint} style={{ flex: 1, minHeight: 48, paddingHorizontal: 10, fontSize: 16, color: c.text, fontFamily: fonts.body }} />
        </View>
        <ScrollView horizontal showsHorizontalScrollIndicator={false} contentContainerStyle={{ gap: 8 }}>
          {([["due", "Due"], ["overdue", "Overdue"], ["settled", "Settled"], ["all", "All"]] as [DueFilter, string][]).map(([k, t]) => <Chip key={k} title={t} active={f === k} onPress={() => setF(k)} />)}
          <Chip title={by === "amount" ? "Sort: biggest" : "Sort: recent"} onPress={() => setBy(by === "amount" ? "recent" : "amount")} />
        </ScrollView>
        {list.length === 0 ? (
          <Card><Empty icon="checkmark-done-outline" title={f === "due" && !needle ? "Nothing to collect" : "No customers match"} hint={f === "due" && !needle ? "When a sale isn't fully paid, it shows up here so you can follow up." : "Try another filter or search."} /></Card>
        ) : (
          <Card style={{ padding: 0 }}>
            {list.map((l, i) => {
              const st = dueState(l.balance);
              const phone = phoneOf.get(l.partyId);
              return (
                <View key={l.partyId}>
                  <Tap label={`${l.name} ${l.balance.owedMinor > 0n ? `owes ${m(l.balance.owedMinor)}` : "settled"}`} onPress={() => open(l.partyId)} style={{ minHeight: 72, flexDirection: "row", alignItems: "center", gap: 12, padding: space.lg }}>
                    <View style={{ width: 42, height: 42, borderRadius: 21, backgroundColor: st === "overdue" ? c.negativeSoft : st === "due" ? c.warningSoft : c.positiveSoft, alignItems: "center", justifyContent: "center" }}>
                      <T v="strong" color={st === "overdue" ? c.negative : st === "due" ? c.warning : c.positive}>{l.name.slice(0, 1).toUpperCase()}</T>
                    </View>
                    <View style={{ flex: 1 }}>
                      <T v="strong" numberOfLines={1}>{l.name}</T>
                      <T v="small" muted numberOfLines={1}>{phone ? `${phone} · ` : ""}last bill {new Date(l.lastBilledAt).toLocaleDateString()}</T>
                    </View>
                    <View style={{ alignItems: "flex-end", gap: 4 }}>
                      <T v="money" style={{ fontFamily: fonts.monoBold }}>{m(l.balance.owedMinor)}</T>
                      {st === "overdue" ? <Badge tone="bad" text={`${m(l.balance.overdueMinor)} overdue`} /> : st === "due" ? <Badge tone="warn" text="Due" /> : <Badge tone="good" text="Settled" />}
                    </View>
                  </Tap>
                  {i < list.length - 1 ? <Divider /> : null}
                </View>
              );
            })}
          </Card>
        )}
      </ScrollView>
    </Screen>
  );
}

/** One customer's account: invoices, payments, and the two things you do here, record a payment and send a reminder. */
export function CustomerLedgerScreen({ partyId, onBack, openSale }: { partyId: string; onBack: () => void; openSale: (id: string) => void }) {
  const { currency, businessName, changed, db, linked } = useApp();
  const c = usePalette();
  const ledger = useLedgers().find((l) => l.partyId === partyId);
  const party = useLocal((d) => getParty(d, partyId), null as Party | null, [partyId]);
  const pays = useLocal((d) => d.all<Payment>("SELECT * FROM payments WHERE party_id=? ORDER BY paid_at DESC, created_at DESC", [partyId]), [] as Payment[], [partyId]);
  const reminders = useLocal((d) => remindersFor(d, partyId), [] as { at: number; channel: string }[], [partyId]);
  const [paying, setPaying] = useState<{ invoiceId?: string } | null>(null);
  const [reversing, setReversing] = useState<Payment | null>(null);
  const m = (x: bigint) => formatMinor(x, currency);
  const now = useNow();
  if (!ledger) return <Screen><Header title="Customer" onBack={onBack} /><Empty title="No invoices for this customer yet" /></Screen>;
  const b = ledger.balance;
  const open = ledger.invoices.filter((i) => dueOf(i) > 0n).sort((a, z) => a.issuedAt - z.issuedAt);
  const settled = ledger.invoices.filter((i) => dueOf(i) === 0n && !i.cancelled).sort((a, z) => z.issuedAt - a.issuedAt);

  async function remind(channel: "whatsapp" | "share") {
    const text = reminderText({
      customer: ledger!.name, business: businessName, owedText: m(b.owedMinor), invoiceNumbers: open.map((i) => i.number),
      dueText: b.overdueMinor > 0n ? `${m(b.overdueMinor)} is past its due date` : undefined,
    });
    if (!db) return;
    if (channel === "whatsapp" && party?.phone) {
      const digits = party.phone.replace(/\D/g, "");
      const to = digits.length === 10 ? `91${digits}` : digits;
      try { await Linking.openURL(`whatsapp://send?phone=${to}&text=${encodeURIComponent(text)}`); } catch { await Linking.openURL(`https://wa.me/${to}?text=${encodeURIComponent(text)}`); }
      await addReminder(db, partyId, nowMs(), "whatsapp");
    } else {
      const r = await Share.share({ message: text });
      if (r.action === Share.sharedAction) await addReminder(db, partyId, nowMs(), "share");
    }
    changed();
  }

  return (
    <Screen>
      <ScrollView contentContainerStyle={{ paddingBottom: 40 }}>
        <Header title={ledger.name} onBack={onBack} sub={party?.phone || undefined} />
        <View style={{ padding: space.lg, gap: space.lg, paddingTop: space.xs }}>
          <View style={{ backgroundColor: b.owedMinor > 0n ? c.warningSoft : c.positiveSoft, borderRadius: radius.lg + 4, padding: space.xl, gap: 6 }}>
            <T v="label" color={b.owedMinor > 0n ? c.warning : c.positive}>{b.owedMinor > 0n ? "Owes you" : b.creditMinor > 0n ? "You owe them (advance)" : "All settled"}</T>
            <T v="moneyHero">{m(b.owedMinor > 0n ? b.owedMinor : b.creditMinor)}</T>
            {b.overdueMinor > 0n ? <T v="small" color={c.negative}>{m(b.overdueMinor)} is past its due date</T> : null}
            {ledger.creditMinor > 0n && b.owedMinor > 0n ? <T v="small" muted>After {m(ledger.creditMinor)} received in advance.</T> : null}
          </View>

          {b.owedMinor > 0n ? (
            <View style={{ gap: 10 }}>
              <Button title="Record payment" icon="cash-outline" onPress={() => setPaying({})} />
              <View style={{ flexDirection: "row", gap: 10 }}>
                {party?.phone ? <Button style={{ flex: 1 }} kind="quiet" title="WhatsApp" icon="logo-whatsapp" onPress={() => void remind("whatsapp")} /> : null}
                <Button style={{ flex: 1 }} kind="quiet" title="Share reminder" icon="share-outline" onPress={() => void remind("share")} />
              </View>
              <T v="small" muted>{reminders[0] ? `Last reminder opened ${new Date(reminders[0].at).toLocaleDateString()} (${reminders.length} so far). Rechvix can't tell whether it was delivered or read.` : "Reminders open WhatsApp or the share sheet with the message ready. Rechvix can't tell whether it was delivered or read."}</T>
            </View>
          ) : <Button title="Record an advance payment" kind="quiet" icon="cash-outline" onPress={() => setPaying({})} />}

          {open.length > 0 ? (
            <View style={{ gap: 8 }}>
              <Label>Open invoices</Label>
              <Card style={{ padding: 0 }}>
                {open.map((i, k) => {
                  const od = daysOverdue(i.dueAt, now);
                  return (
                    <View key={i.id}>
                      <Tap label={`Invoice ${i.number}, ${m(dueOf(i))} due`} onPress={() => openSale(i.id)} style={{ minHeight: 64, flexDirection: "row", alignItems: "center", gap: 12, padding: space.lg }}>
                        <View style={{ flex: 1 }}>
                          <T v="strong">{i.number}</T>
                          <T v="small" muted>{new Date(i.issuedAt).toLocaleDateString()} · total {m(i.totalMinor)}{i.paidMinor > 0n ? ` · paid ${m(i.paidMinor)}` : ""}</T>
                          {od > 0 ? <T v="small" color={c.negative}>{od} day{od === 1 ? "" : "s"} overdue ({ageBucket(i.dueAt, now)})</T> : i.dueAt ? <T v="small" muted>Due {new Date(i.dueAt).toLocaleDateString()}</T> : null}
                        </View>
                        <T v="money" style={{ fontFamily: fonts.monoBold }}>{m(dueOf(i))}</T>
                        <Tap label={`Pay ${i.number}`} onPress={() => setPaying({ invoiceId: i.id })} style={{ minHeight: 40, paddingHorizontal: 12, borderRadius: 20, backgroundColor: c.accentSoft, justifyContent: "center" }}><T v="small" color={c.accent} style={{ fontFamily: fonts.bodySemi }}>Pay</T></Tap>
                      </Tap>
                      {k < open.length - 1 ? <Divider /> : null}
                    </View>
                  );
                })}
              </Card>
            </View>
          ) : null}

          <View style={{ gap: 8 }}>
            <Label>Payments</Label>
            {pays.length === 0 ? <T v="small" muted>No payments recorded yet.</T> : (
              <Card style={{ padding: 0 }}>
                {pays.map((p, i) => {
                  const void_ = p.reversed_at != null || p.state === "void";
                  const inv = ledger.invoices.find((x) => x.id === p.sale_local_id);
                  return (
                    <View key={p.id}>
                      <View style={{ padding: space.lg, gap: 4 }}>
                        <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
                          <T v="strong" style={void_ ? { textDecorationLine: "line-through", color: c.muted } : undefined}>{m(parseMinor(p.amount))} · {p.method.replace("_", " ").toLowerCase()}</T>
                          {void_ ? <Badge tone="neutral" text="Reversed" /> : p.state === "pending" ? (linked ? <Badge tone="info" icon="cloud-upload-outline" text="Waiting to sync" /> : <Badge tone="good" text="Saved on phone" />) : p.state === "failed" ? <Badge tone="bad" text="Sync failed" /> : <Badge tone="good" text="Recorded" />}
                        </View>
                        <T v="small" muted>{new Date(p.paid_at).toLocaleString()} · {inv ? inv.number : "advance (not on an invoice)"}{p.reference ? ` · ref ${p.reference}` : ""}</T>
                        {void_ ? <T v="small" muted>Reversed: {p.reversal_reason}</T> : null}
                        {p.state === "failed" && p.error ? <T v="small" color={c.negative}>{p.error}</T> : null}
                        {!void_ ? <Tap label="Share receipt" onPress={() => void Share.share({ message: `Payment received: ${m(parseMinor(p.amount))} from ${ledger.name} on ${new Date(p.paid_at).toLocaleDateString()} (${p.method.replace("_", " ").toLowerCase()})${inv ? ` towards invoice ${inv.number}` : " as an advance"}${p.reference ? `, ref ${p.reference}` : ""}. Thank you! - ${businessName}` })} style={{ minHeight: 36, justifyContent: "center", alignSelf: "flex-start" }}><T v="small" color={c.accent} style={{ fontFamily: fonts.bodySemi }}>Share receipt</T></Tap> : null}
                        {!void_ ? <Tap label="Reverse this payment" onPress={() => setReversing(p)} style={{ minHeight: 36, justifyContent: "center", alignSelf: "flex-start" }}><T v="small" color={c.negative} style={{ fontFamily: fonts.bodySemi }}>Reverse</T></Tap> : null}
                      </View>
                      {i < pays.length - 1 ? <Divider /> : null}
                    </View>
                  );
                })}
              </Card>
            )}
          </View>

          {settled.length > 0 ? (
            <View style={{ gap: 8 }}>
              <Label>Settled invoices</Label>
              <Card style={{ padding: 0 }}>
                {settled.slice(0, 15).map((i, k, a) => (
                  <View key={i.id}>
                    <Tap label={`Invoice ${i.number}`} onPress={() => openSale(i.id)} style={{ minHeight: 56, flexDirection: "row", alignItems: "center", gap: 12, padding: space.lg }}>
                      <View style={{ flex: 1 }}><T v="strong">{i.number}</T><T v="small" muted>{new Date(i.issuedAt).toLocaleDateString()}</T></View>
                      <T v="money">{m(i.totalMinor)}</T><Badge tone="good" text="Paid" />
                    </Tap>
                    {k < a.length - 1 ? <Divider /> : null}
                  </View>
                ))}
              </Card>
            </View>
          ) : null}
        </View>
      </ScrollView>
      {paying ? <PaymentSheet ledger={ledger} invoiceId={paying.invoiceId} onClose={() => setPaying(null)} onSaved={() => { setPaying(null); changed(); }} /> : null}
      {reversing ? <ReverseSheet payment={reversing} onClose={() => setReversing(null)} onDone={() => { setReversing(null); changed(); }} /> : null}
    </Screen>
  );
}

/** Record money received. The seller picks the amount and, if they want, one invoice; otherwise it clears the oldest first.
 *  What each invoice will receive is shown before saving. Extra money is kept as the customer's advance, never lost. */
export function PaymentSheet({ ledger, invoiceId, onClose, onSaved }: { ledger: CustomerLedger; invoiceId?: string; onClose: () => void; onSaved: () => void }) {
  const c = usePalette();
  const { db, currency } = useApp();
  const m = (x: bigint) => formatMinor(x, currency);
  const target = invoiceId ? ledger.invoices.find((i) => i.id === invoiceId) : undefined;
  const defaultAmt = target ? dueOf(target) : ledger.balance.owedMinor;
  const [amount, setAmount] = useState(minorToString(defaultAmt > 0n ? defaultAmt : 0n));
  const [method, setMethod] = useState("CASH");
  const [ref, setRef] = useState("");
  const [only, setOnly] = useState<string | null>(invoiceId ?? null);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const amt = toMinor(dec(amount));
  let plan: ReturnType<typeof allocatePayment> | null = null; let planErr: string | null = null;
  if (amt > 0n) {
    try {
      const pool = only ? ledger.invoices.filter((i) => i.id === only) : ledger.invoices;
      plan = allocatePayment(amt, pool);
    } catch (e) { planErr = e instanceof Error ? e.message : "Check the amount."; }
  }
  const byId = new Map(ledger.invoices.map((i) => [i.id, i]));

  async function save() {
    if (!db || !plan || saving) return;
    setSaving(true); setErr(null);
    try {
      const parts: { id: string; saleLocalId: string | null; amount: string }[] = plan.allocations.map((a) => ({ id: uid(), saleLocalId: a.invoiceId, amount: minorToString(a.amountMinor) }));
      if (plan.creditMinor > 0n) parts.push({ id: uid(), saleLocalId: null, amount: minorToString(plan.creditMinor) });
      await insertPayments(db, { groupId: uid(), partyId: ledger.partyId, method, reference: ref.trim(), note: "Recorded from the Rechvix app", paidAt: nowMs(), parts }, nowMs());
      onSaved();
    } catch (e) { setErr(e instanceof Error ? e.message : "Couldn't save the payment."); } finally { setSaving(false); }
  }
  return (
    <Modal visible animationType="slide" onRequestClose={onClose}>
      <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
        <Header title="Record payment" onBack={onClose} sub={ledger.name} />
        <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg }} keyboardShouldPersistTaps="handled">
          <Field label={`Amount received (${currency})`} value={amount} onChangeText={(t) => setAmount(t.replace(/[^0-9.]/g, ""))} keyboardType="decimal-pad" autoFocus selectTextOnFocus />
          <View style={{ gap: 8 }}><Label>Paid by</Label><View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>{METHODS.map((x) => <Chip key={x.k} title={x.t} active={method === x.k} onPress={() => setMethod(x.k)} />)}</View></View>
          <Field label="Reference (UPI ID / cheque no., optional)" value={ref} onChangeText={setRef} autoCapitalize="characters" />
          <View style={{ gap: 8 }}>
            <Label>Apply to</Label>
            <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
              <Chip title="Oldest first" active={only === null} onPress={() => setOnly(null)} />
              {ledger.invoices.filter((i) => dueOf(i) > 0n).map((i) => <Chip key={i.id} title={i.number} active={only === i.id} onPress={() => { setOnly(i.id); setAmount(minorToString(dueOf(i))); }} />)}
            </View>
          </View>
          {plan ? (
            <Card style={{ gap: 6 }}>
              <T v="strong">This payment will</T>
              {plan.allocations.map((a) => <T key={a.invoiceId} v="small">• pay {m(a.amountMinor)} towards {byId.get(a.invoiceId)?.number}{dueOf(byId.get(a.invoiceId)!) === a.amountMinor ? " (clears it)" : ""}</T>)}
              {plan.creditMinor > 0n ? <T v="small" color={c.info}>• keep {m(plan.creditMinor)} as an advance for {ledger.name}, because it is more than what is due</T> : null}
            </Card>
          ) : null}
          {planErr ? <T v="small" color={c.negative}>{planErr}</T> : null}
          {err ? <T v="small" color={c.negative}>{err}</T> : null}
          <Button title="Save payment" icon="checkmark-circle-outline" onPress={() => void save()} disabled={!plan || amt <= 0n} busy={saving} />
        </ScrollView>
      </SafeAreaView>
    </Modal>
  );
}

function ReverseSheet({ payment, onClose, onDone }: { payment: Payment; onClose: () => void; onDone: () => void }) {
  const c = usePalette();
  const { db, currency } = useApp();
  const [why, setWhy] = useState("");
  const [err, setErr] = useState<string | null>(null);
  return (
    <Modal visible transparent animationType="fade" onRequestClose={onClose}>
      <View style={{ flex: 1, backgroundColor: "rgba(0,0,0,0.5)", justifyContent: "center", padding: space.lg }}>
        <View style={{ backgroundColor: c.surface, borderRadius: radius.lg + 4, padding: space.xl, gap: 14 }}>
          <T v="h2">Reverse this payment?</T>
          <T v="body" muted>{formatMinor(parseMinor(payment.amount), currency)} will count as not received, and the invoice goes back to owing it. The payment stays in the history, marked as reversed.</T>
          <Field label="Why? (required)" value={why} onChangeText={setWhy} autoFocus />
          {err ? <T v="small" color={c.negative}>{err}</T> : null}
          <View style={{ flexDirection: "row", gap: 10 }}>
            <Button style={{ flex: 1 }} kind="quiet" title="Keep it" onPress={onClose} />
            <Button style={{ flex: 1 }} kind="danger" title="Reverse" disabled={!why.trim()} onPress={() => { if (db) void reversePayment(db, payment.id, why, nowMs()).then(onDone).catch((e) => setErr(e instanceof Error ? e.message : "Couldn't reverse.")); }} />
          </View>
        </View>
      </View>
    </Modal>
  );
}
export { livePayments };
