import * as Haptics from "expo-haptics";
import React, { useEffect, useMemo, useRef, useState } from "react";
import { Animated, FlatList, Modal, Pressable, ScrollView, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { productByCode, searchParties, searchProducts, type Party, type Product, type SaleLine } from "../db/repo";
import { shareInvoice } from "../lib/share";
import { useLocal } from "../lib/hooks";
import { bumpQty, formatMoney, isValidQty, lineAmount, num } from "../lib/money";
import { calcTotals } from "../lib/tax";
import { useApp } from "../state/app";
import { fonts, radius, space, usePalette } from "../theme";
import { Button, Card, Chip, Divider, Empty, Header, Icon, IconButton, Label, Screen, Tap } from "../ui/kit";
import { T } from "../ui/text";
import { Scanner } from "./Scanner";

const PAY: { key: string | null; label: string }[] = [{ key: "CASH", label: "Cash" }, { key: "UPI", label: "UPI" }, { key: "CARD", label: "Card" }, { key: null, label: "Pay later" }];

export function Sell({ onDone, addItem }: { onDone: (localId: string) => void; addItem: () => void }) {
  const { profile, saveSale, linked, currency, db } = useApp();
  const c = usePalette();
  const [customer, setCustomer] = useState<Party | null>(null);
  const [lines, setLines] = useState<SaleLine[]>([]);
  const [q, setQ] = useState("");
  const [pay, setPay] = useState<string | null>("CASH");
  const [pickCustomer, setPickCustomer] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [scanHint, setScanHint] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [done, setDone] = useState<{ id: string; number: string; total: number } | null>(null);

  const walkIn = useLocal((d) => d.first<Party>("SELECT * FROM parties WHERE is_walk_in=1 ORDER BY origin DESC LIMIT 1"), null);
  const who = customer ?? walkIn;
  const results = useLocal((d) => searchProducts(d, q, 40), [] as Product[], [q]);
  const anyItems = useLocal((d) => d.first<{ n: number }>("SELECT COUNT(*) AS n FROM products WHERE archived=0").then((r) => r?.n ?? 0), 0);
  const stockById = useMemo(() => new Map(results.map((p) => [p.variant_id, p.stock])), [results]);

  function add(p: Product) {
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    setLines((ls) => {
      const i = ls.findIndex((l) => l.variantId === p.variant_id);
      if (i >= 0) return ls.map((l, k) => (k === i ? { ...l, qty: bumpQty(l.qty, 1) } : l));
      return [...ls, { variantId: p.variant_id, name: p.name, unitId: p.unit_id, qty: "1", price: p.price ?? "0", discount: "0", gstRate: p.gst_rate ?? (p.origin === "local" ? null : null) }];
    });
  }
  const change = (id: string, delta: number) => setLines((ls) => ls.flatMap((l) => (l.variantId !== id ? [l] : num(bumpQty(l.qty, delta)) <= 0 ? [] : [{ ...l, qty: bumpQty(l.qty, delta) }])));
  const setPrice = (id: string, price: string) => setLines((ls) => ls.map((l) => (l.variantId === id ? { ...l, price } : l)));

  async function onScan(code: string) {
    if (!db) return;
    const p = await productByCode(db, code);
    if (p) { add(p); setScanHint(`Added ${p.name}`); } else setScanHint(`No item with code ${code}.`);
  }

  const totals = calcTotals(lines);
  const bad = lines.find((l) => !isValidQty(l.qty));
  const priceMissing = lines.some((l) => num(l.price) <= 0);
  const over = lines.filter((l) => { const s = stockById.get(l.variantId); return s != null && num(l.qty) > num(s); });
  const canSave = !!who && lines.length > 0 && !bad && !priceMissing && !saving;

  async function save() {
    if (!who) return;
    setSaving(true);
    const r = await saveSale({ customerId: who.id, customerName: who.name, lines, paymentMethod: pay });
    setSaving(false);
    void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    setDone({ id: r.localId, number: r.number, total: totals.total });
    setLines([]); setQ(""); setCustomer(null); setPay("CASH");
  }

  if (done) return <Done info={done} currency={currency} onView={() => { const id = done.id; setDone(null); onDone(id); }} onNew={() => setDone(null)} />;

  return (
    <Screen>
      <ScrollView contentContainerStyle={{ paddingBottom: 190 }} keyboardShouldPersistTaps="handled">
        <Header title="New sale" />
        <View style={{ padding: space.lg, gap: space.lg, paddingTop: space.xs }}>
          <Tap label={`Customer: ${who?.name ?? "none"}. Change`} onPress={() => setPickCustomer(true)}
            style={{ minHeight: 64, flexDirection: "row", alignItems: "center", gap: 12, backgroundColor: c.surface, borderRadius: radius.lg + 2, borderWidth: 1, borderColor: c.border, paddingHorizontal: space.lg }}>
            <View style={{ width: 40, height: 40, borderRadius: 20, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center" }}><Icon name="person" size={20} color={c.accent} /></View>
            <View style={{ flex: 1 }}><T v="label" muted>Customer</T><T v="h3" numberOfLines={1}>{who?.name ?? "Choose…"}</T></View>
            <T v="small" color={c.accent} style={{ fontFamily: fonts.bodySemi }}>Change</T>
          </Tap>

          <View style={{ flexDirection: "row", gap: 10 }}>
            <View style={{ flex: 1, flexDirection: "row", alignItems: "center", backgroundColor: c.surface, borderRadius: radius.md + 2, borderWidth: 1, borderColor: c.borderStrong, paddingLeft: 14 }}>
              <Icon name="search" size={20} color={c.faint} />
              <TextInput accessibilityLabel="Search items" value={q} onChangeText={setQ} placeholder="Search item or scan" placeholderTextColor={c.faint} autoCorrect={false}
                style={{ flex: 1, minHeight: 52, paddingHorizontal: 10, fontSize: 17, color: c.text, fontFamily: fonts.body }} />
            </View>
            <IconButton name="barcode-outline" label="Scan barcode" tone="accent" onPress={() => { setScanHint(null); setScanning(true); }} />
          </View>

          {anyItems === 0 ? (
            <Card><Empty icon="pricetag-outline" title="Add your first item" hint="Items hold the name, price, GST and stock you'll bill with." action={{ title: "Add an item", onPress: addItem }} /></Card>
          ) : results.length === 0 ? (
            <Empty icon="search" title="No matching item" hint="Check the spelling, or search by SKU or barcode." />
          ) : (
            <Card style={{ padding: 0, overflow: "hidden" }}>
              {(lines.length ? results.slice(0, q ? 8 : 4) : results.slice(0, 12)).map((p, i, arr) => (
                <View key={p.variant_id}>
                  <Tap label={`Add ${p.name}`} onPress={() => add(p)} style={{ minHeight: 64, flexDirection: "row", alignItems: "center", padding: space.lg, gap: 12 }}>
                    <View style={{ flex: 1 }}>
                      <T v="strong" numberOfLines={1}>{p.name}</T>
                      <T v="small" muted>{p.stock != null ? `${p.stock} ${p.unit_label} in stock` : "stock not tracked"}{p.sku ? ` · ${p.sku}` : ""}</T>
                    </View>
                    <T v="money">{p.price != null ? formatMoney(num(p.price), currency) : "—"}</T>
                    <View style={{ width: 34, height: 34, borderRadius: 17, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center" }}><Icon name="add" size={20} color={c.accent} /></View>
                  </Tap>
                  {i < arr.length - 1 ? <Divider /> : null}
                </View>
              ))}
            </Card>
          )}

          {lines.length > 0 ? (
            <View style={{ gap: 10 }}>
              <Label>On this bill</Label>
              <Card style={{ padding: 0 }}>
                {lines.map((l, i) => (
                  <View key={l.variantId}>
                    <View style={{ padding: space.lg, gap: 12 }}>
                      <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
                        <T v="strong" numberOfLines={2} style={{ flex: 1 }}>{l.name}</T>
                        <T v="money" style={{ fontFamily: fonts.monoBold }}>{formatMoney(lineAmount(l), currency)}</T>
                      </View>
                      <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                        <Stepper label="Decrease" icon="remove" onPress={() => change(l.variantId, -1)} />
                        <T v="money" style={{ minWidth: 40, textAlign: "center", fontSize: 18 }}>{l.qty}</T>
                        <Stepper label="Increase" icon="add" onPress={() => change(l.variantId, 1)} />
                        <T v="small" muted>×</T>
                        <TextInput accessibilityLabel={`Price of ${l.name}`} value={l.price} onChangeText={(t) => setPrice(l.variantId, t.replace(/[^0-9.]/g, ""))} keyboardType="decimal-pad"
                          style={{ flex: 1, minWidth: 84, minHeight: 44, borderRadius: radius.sm + 2, borderWidth: 1, borderColor: num(l.price) <= 0 ? c.negative : c.border, color: c.text, paddingHorizontal: 10, fontSize: 16, textAlign: "right", fontFamily: fonts.mono }} />
                      </View>
                    </View>
                    {i < lines.length - 1 ? <Divider /> : null}
                  </View>
                ))}
              </Card>
              {over.length > 0 ? <T v="small" color={c.warning}>Only {stockById.get(over[0]!.variantId)} of {over[0]!.name} in stock.{linked ? " The server may refuse this sale." : ""}</T> : null}
              {priceMissing ? <T v="small" color={c.negative}>An item has no price. Set one before saving.</T> : null}

              <Label>Payment</Label>
              <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>{PAY.map((p) => <Chip key={p.label} title={p.label} active={pay === p.key} onPress={() => setPay(p.key)} />)}</View>
            </View>
          ) : null}
        </View>
      </ScrollView>

      <View style={{ position: "absolute", left: 0, right: 0, bottom: 0, padding: space.lg, backgroundColor: c.surface, borderTopWidth: 1, borderTopColor: c.border, gap: 10 }}>
        <View style={{ gap: 3 }}>
          <Row k={`${lines.length} item${lines.length === 1 ? "" : "s"} · subtotal`} v={formatMoney(totals.subtotal, currency)} />
          {totals.tax > 0 ? <Row k={`GST${linked ? " (estimate)" : ""}`} v={formatMoney(totals.tax, currency)} /> : profile?.country === "IN" && lines.length > 0 && !lines.some((l) => num(l.gstRate) > 0) && linked ? <T v="small" muted>Tax is added by the server when this syncs.</T> : null}
          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "baseline" }}><T v="h3">Total</T><T v="moneyLg">{formatMoney(totals.total, currency)}</T></View>
        </View>
        <Button title={linked ? "Save sale" : "Save invoice"} onPress={() => void save()} disabled={!canSave} busy={saving} icon="checkmark-circle-outline" />
      </View>

      <CustomerPicker visible={pickCustomer} onClose={() => setPickCustomer(false)} onPick={(p) => { setCustomer(p); setPickCustomer(false); }} />
      <Scanner visible={scanning} onCode={(code) => void onScan(code)} onClose={() => setScanning(false)} hint={scanHint} />
    </Screen>
  );
}

const Row = ({ k, v }: { k: string; v: string }) => <View style={{ flexDirection: "row", justifyContent: "space-between" }}><T v="small" muted>{k}</T><T v="money" style={{ fontSize: 14 }}>{v}</T></View>;

function Stepper({ label, icon, onPress }: { label: string; icon: "add" | "remove"; onPress: () => void }) {
  const c = usePalette();
  return (
    <Tap label={label} onPress={onPress} style={{ width: 44, height: 44, borderRadius: 22, alignItems: "center", justifyContent: "center", backgroundColor: c.surfaceAlt }}>
      <Icon name={icon} size={22} />
    </Tap>
  );
}

/** The moment after saving. A short confirmation with the next obvious steps. */
function Done({ info, currency, onView, onNew }: { info: { id: string; number: string; total: number }; currency: string; onView: () => void; onNew: () => void }) {
  const c = usePalette();
  const { db, profile } = useApp();
  const pop = useRef(new Animated.Value(0)).current;
  useEffect(() => { Animated.spring(pop, { toValue: 1, friction: 5, tension: 90, useNativeDriver: true }).start(); }, [pop]);
  const [busy, setBusy] = useState(false);
  async function share() {
    if (!db || !profile) return;
    setBusy(true);
    try {
      const s = await db.first<{ payload: string; created_at: number; local_number: string | null }>("SELECT payload, created_at, local_number FROM sales WHERE local_id=?", [info.id]);
      if (s) {
        const p = JSON.parse(s.payload) as { customerName: string; lines: SaleLine[]; paymentMethod: string | null };
        await shareInvoice({ number: s.local_number ?? info.number, date: s.created_at, business: profile, customerName: p.customerName, lines: p.lines, paymentMethod: p.paymentMethod, currency });
      }
    } finally { setBusy(false); }
  }
  return (
    <Screen>
      <View style={{ flex: 1, padding: space.xl, justifyContent: "center", gap: space.xl }}>
        <View style={{ alignItems: "center", gap: 14 }}>
          <Animated.View style={{ transform: [{ scale: pop }], width: 96, height: 96, borderRadius: 48, backgroundColor: c.positiveSoft, alignItems: "center", justifyContent: "center" }}>
            <Icon name="checkmark" size={54} color={c.positive} />
          </Animated.View>
          <T v="h2">Invoice saved</T>
          <T v="moneyHero" style={{ fontSize: 34 }}>{formatMoney(info.total, currency)}</T>
          <T v="body" muted>{info.number}</T>
        </View>
        <View style={{ gap: 10 }}>
          <Button title="Share invoice (PDF)" icon="share-outline" onPress={() => void share()} busy={busy} />
          <Button title="View invoice" kind="quiet" onPress={onView} />
          <Button title="New sale" kind="quiet" icon="add" onPress={onNew} />
        </View>
      </View>
    </Screen>
  );
}

function CustomerPicker({ visible, onClose, onPick }: { visible: boolean; onClose: () => void; onPick: (p: Party) => void }) {
  const c = usePalette();
  const [q, setQ] = useState("");
  const list = useLocal((d) => searchParties(d, q), [] as Party[], [q, visible]);
  return (
    <Modal visible={visible} animationType="slide" onRequestClose={onClose}>
      <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
        <Header title="Customer" onBack={onClose} />
        <View style={{ paddingHorizontal: space.lg, paddingBottom: space.md }}>
          <TextInput accessibilityLabel="Search customers" autoFocus value={q} onChangeText={setQ} placeholder="Name or phone" placeholderTextColor={c.faint}
            style={{ minHeight: 52, borderRadius: radius.md + 2, borderWidth: 1, borderColor: c.borderStrong, backgroundColor: c.surface, color: c.text, paddingHorizontal: 14, fontSize: 17, fontFamily: fonts.body }} />
        </View>
        <FlatList data={list} keyExtractor={(p) => p.id} keyboardShouldPersistTaps="handled" ListEmptyComponent={<Empty icon="people-outline" title="No customer found" hint="Add customers from the Home screen." />}
          renderItem={({ item }) => (
            <Pressable accessibilityRole="button" onPress={() => onPick(item)} style={({ pressed }) => ({ minHeight: 64, paddingHorizontal: space.lg, justifyContent: "center", backgroundColor: pressed ? c.accentSoft : "transparent" })}>
              <T v="strong">{item.name}</T>
              <T v="small" muted>{item.is_walk_in ? "Cash sale, no customer details" : item.phone || "No phone"}</T>
            </Pressable>
          )} ItemSeparatorComponent={Divider} />
      </SafeAreaView>
    </Modal>
  );
}
