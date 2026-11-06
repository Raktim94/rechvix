import * as Haptics from "expo-haptics";
import React, { useMemo, useState } from "react";
import { FlatList, Modal, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { productBySku, searchParties, searchProducts, type Party, type Product, type SaleLine } from "../db/repo";
import { useLocal } from "../lib/hooks";
import { bumpQty, formatMoney, isValidQty, lineAmount, num, subtotal } from "../lib/money";
import { useApp } from "../state/app";
import { radius, space, tabular, usePalette } from "../theme";
import { Body, Button, Card, Chip, Divider, Empty, H1, Label, Screen } from "../ui/kit";
import { Scanner } from "./Scanner";

const PAY: { key: string | null; label: string }[] = [{ key: null, label: "Pay later" }, { key: "CASH", label: "Cash" }, { key: "UPI", label: "UPI" }, { key: "CARD", label: "Card" }];

export function Sell({ onSaved }: { onSaved: (localId: string) => void }) {
  const { org, saveSale, online } = useApp();
  const c = usePalette();
  const cur = org?.currency ?? "INR";
  const [customer, setCustomer] = useState<Party | null>(null);
  const [lines, setLines] = useState<SaleLine[]>([]);
  const [q, setQ] = useState("");
  const [pay, setPay] = useState<string | null>("CASH");
  const [pickCustomer, setPickCustomer] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [scanHint, setScanHint] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const walkIn = useLocal((db) => db.first<Party>("SELECT * FROM parties WHERE is_walk_in=1 LIMIT 1"), null);
  const who = customer ?? walkIn;
  const results = useLocal((db) => searchProducts(db, q, 40), [] as Product[], [q]);
  const stockById = useMemo(() => new Map(results.map((p) => [p.variant_id, p.stock])), [results]);

  function add(p: Product) {
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    setLines((ls) => {
      const i = ls.findIndex((l) => l.variantId === p.variant_id);
      if (i >= 0) return ls.map((l, k) => (k === i ? { ...l, qty: bumpQty(l.qty, 1) } : l));
      return [...ls, { variantId: p.variant_id, name: p.name, unitId: p.unit_id, qty: "1", price: p.price ?? "0", discount: "0" }];
    });
  }
  const change = (variantId: string, delta: number) =>
    setLines((ls) => ls.flatMap((l) => (l.variantId !== variantId ? [l] : num(bumpQty(l.qty, delta)) <= 0 ? [] : [{ ...l, qty: bumpQty(l.qty, delta) }])));
  const setPrice = (variantId: string, price: string) => setLines((ls) => ls.map((l) => (l.variantId === variantId ? { ...l, price } : l)));

  const { db } = useApp();
  async function onScan(code: string) {
    if (!db) return;
    const p = await productBySku(db, code);
    if (p) { add(p); setScanHint(`Added ${p.name}`); } else setScanHint(`No item with code ${code}. Search by name instead.`);
  }

  const sub = subtotal(lines);
  const bad = lines.find((l) => !isValidQty(l.qty));
  const priceMissing = lines.some((l) => num(l.price) <= 0);
  const overStock = lines.filter((l) => { const s = stockById.get(l.variantId); return s != null && num(l.qty) > num(s); });
  const canSave = !!who && lines.length > 0 && !bad && !saving;

  async function save() {
    if (!who) return;
    setSaving(true);
    const id = await saveSale({ customerId: who.id, customerName: who.name, lines, paymentMethod: pay });
    setSaving(false);
    setLines([]); setQ(""); setCustomer(null); setPay("CASH");
    void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
    onSaved(id);
  }

  return (
    <Screen>
      <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: 140 }} keyboardShouldPersistTaps="handled">
        <H1>New sale</H1>

        <Pressable accessibilityRole="button" accessibilityLabel={`Customer: ${who?.name ?? "none"}. Change`} onPress={() => setPickCustomer(true)}
          style={{ minHeight: 56, flexDirection: "row", alignItems: "center", justifyContent: "space-between", backgroundColor: c.surface, borderRadius: radius.lg, borderWidth: 1, borderColor: c.border, paddingHorizontal: space.lg }}>
          <View><Label>Customer</Label><Text style={{ fontSize: 17, fontWeight: "600", color: c.text, marginTop: 2 }}>{who?.name ?? "Choose…"}</Text></View>
          <Text style={{ color: c.accent, fontWeight: "700" }}>Change</Text>
        </Pressable>

        <View style={{ flexDirection: "row", gap: 10 }}>
          <TextInput accessibilityLabel="Search items" value={q} onChangeText={setQ} placeholder="Search item or SKU" placeholderTextColor={c.faint} autoCorrect={false}
            style={{ flex: 1, minHeight: 52, borderRadius: radius.md, borderWidth: 1, borderColor: c.borderStrong, backgroundColor: c.surface, color: c.text, paddingHorizontal: 14, fontSize: 17 }} />
          <Button title="Scan" kind="quiet" onPress={() => { setScanHint(null); setScanning(true); }} style={{ minWidth: 84 }} />
        </View>

        {results.length === 0 ? (
          <Empty title={q ? "No matching item" : "No items yet"} hint={q ? "Check the spelling, or search by SKU." : online ? "Items appear after the first sync." : "Connect once to download your items."} />
        ) : (
          <Card style={{ padding: 0 }}>
            {(lines.length ? results.slice(0, q ? 8 : 5) : results.slice(0, 12)).map((p, i, arr) => (
              <View key={p.variant_id}>
                <Pressable accessibilityRole="button" accessibilityLabel={`Add ${p.name}`} onPress={() => add(p)} style={({ pressed }) => ({ minHeight: 60, flexDirection: "row", alignItems: "center", padding: space.lg, gap: 12, opacity: pressed ? 0.6 : 1 })}>
                  <View style={{ flex: 1 }}>
                    <Text style={{ fontSize: 16, fontWeight: "600", color: c.text }} numberOfLines={1}>{p.name}</Text>
                    <Text style={{ fontSize: 13, color: c.muted }}>{p.stock != null ? `${p.stock} in stock` : "stock unknown"}{p.sku ? ` · ${p.sku}` : ""}</Text>
                  </View>
                  <Text style={[{ fontSize: 16, fontWeight: "700", color: c.text }, tabular]}>{p.price != null ? formatMoney(num(p.price), cur) : "—"}</Text>
                  <Text style={{ color: c.accent, fontSize: 22, fontWeight: "700" }}>+</Text>
                </Pressable>
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
                  <View style={{ padding: space.lg, gap: 10 }}>
                    <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
                      <Text style={{ flex: 1, fontSize: 16, fontWeight: "600", color: c.text }} numberOfLines={2}>{l.name}</Text>
                      <Text style={[{ fontSize: 16, fontWeight: "700", color: c.text }, tabular]}>{formatMoney(lineAmount(l), cur)}</Text>
                    </View>
                    <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
                      <Stepper label="Decrease" sym="−" onPress={() => change(l.variantId, -1)} />
                      <Text style={[{ minWidth: 44, textAlign: "center", fontSize: 18, fontWeight: "700", color: c.text }, tabular]}>{l.qty}</Text>
                      <Stepper label="Increase" sym="+" onPress={() => change(l.variantId, 1)} />
                      <Text style={{ color: c.muted }}>×</Text>
                      <TextInput accessibilityLabel={`Price of ${l.name}`} value={l.price} onChangeText={(t) => setPrice(l.variantId, t.replace(/[^0-9.]/g, ""))} keyboardType="decimal-pad"
                        style={[{ minWidth: 84, minHeight: 44, borderRadius: radius.sm, borderWidth: 1, borderColor: num(l.price) <= 0 ? c.negative : c.border, color: c.text, paddingHorizontal: 10, fontSize: 16, textAlign: "right" }, tabular]} />
                    </View>
                  </View>
                  {i < lines.length - 1 ? <Divider /> : null}
                </View>
              ))}
            </Card>
            {overStock.length > 0 ? <Body small style={{ color: c.warning }}>Only {stockById.get(overStock[0]!.variantId)} of {overStock[0]!.name} in stock. The server may refuse this sale.</Body> : null}
            {priceMissing ? <Body small style={{ color: c.negative }}>An item has no price. Set one before saving.</Body> : null}

            <Label>Payment</Label>
            <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
              {PAY.map((p) => <Chip key={p.label} title={p.label} active={pay === p.key} onPress={() => setPay(p.key)} />)}
            </View>
          </View>
        ) : null}
      </ScrollView>

      <View style={{ position: "absolute", left: 0, right: 0, bottom: 0, padding: space.lg, backgroundColor: c.surface, borderTopWidth: 1, borderTopColor: c.border, gap: 8 }}>
        <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "baseline" }}>
          <Text style={{ color: c.muted, fontSize: 14 }}>{lines.length} item{lines.length === 1 ? "" : "s"} · before tax</Text>
          <Text style={[{ fontSize: 22, fontWeight: "700", color: c.text }, tabular]}>{formatMoney(sub, cur)}</Text>
        </View>
        <Button title={online ? "Save sale" : "Save sale (syncs later)"} onPress={() => void save()} disabled={!canSave || priceMissing} busy={saving} />
      </View>

      <CustomerPicker visible={pickCustomer} onClose={() => setPickCustomer(false)} onPick={(p) => { setCustomer(p); setPickCustomer(false); }} />
      <Scanner visible={scanning} onCode={(code) => void onScan(code)} onClose={() => setScanning(false)} hint={scanHint} />
    </Screen>
  );
}

function Stepper({ label, sym, onPress }: { label: string; sym: string; onPress: () => void }) {
  const c = usePalette();
  return (
    <Pressable accessibilityRole="button" accessibilityLabel={label} onPress={onPress} hitSlop={6}
      style={({ pressed }) => ({ width: 44, height: 44, borderRadius: 22, alignItems: "center", justifyContent: "center", backgroundColor: pressed ? c.accentSoft : c.surfaceAlt })}>
      <Text style={{ fontSize: 22, color: c.text, fontWeight: "600" }}>{sym}</Text>
    </Pressable>
  );
}

function CustomerPicker({ visible, onClose, onPick }: { visible: boolean; onClose: () => void; onPick: (p: Party) => void }) {
  const c = usePalette();
  const [q, setQ] = useState("");
  const list = useLocal((db) => searchParties(db, q), [] as Party[], [q, visible]);
  return (
    <Modal visible={visible} animationType="slide" onRequestClose={onClose}>
      <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
        <View style={{ padding: space.lg, gap: 12 }}>
          <H1>Customer</H1>
          <TextInput accessibilityLabel="Search customers" autoFocus value={q} onChangeText={setQ} placeholder="Name or phone" placeholderTextColor={c.faint}
            style={{ minHeight: 52, borderRadius: radius.md, borderWidth: 1, borderColor: c.borderStrong, backgroundColor: c.surface, color: c.text, paddingHorizontal: 14, fontSize: 17 }} />
        </View>
        <FlatList data={list} keyExtractor={(p) => p.id} keyboardShouldPersistTaps="handled" ListEmptyComponent={<Empty title="No customer found" hint="New customers are added on the web for now." />}
          renderItem={({ item }) => (
            <Pressable accessibilityRole="button" onPress={() => onPick(item)} style={({ pressed }) => ({ minHeight: 60, paddingHorizontal: space.lg, justifyContent: "center", backgroundColor: pressed ? c.accentSoft : "transparent" })}>
              <Text style={{ fontSize: 16, fontWeight: "600", color: c.text }}>{item.name}</Text>
              <Text style={{ fontSize: 13, color: c.muted }}>{item.is_walk_in ? "Cash sale, no customer details" : item.phone || "No phone"}</Text>
            </Pressable>
          )} ItemSeparatorComponent={Divider} />
        <View style={{ padding: space.lg }}><Button title="Cancel" kind="quiet" onPress={onClose} /></View>
      </SafeAreaView>
    </Modal>
  );
}
