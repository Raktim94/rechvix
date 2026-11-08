import React, { useEffect, useState } from "react";
import { unitPriceFor } from "../lib/sale";
import { Alert, FlatList, Modal, ScrollView, TextInput, View, KeyboardAvoidingView, Platform } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { archiveItem, createItem, getItem, searchProducts, setLowAt, updateItem, type ItemInput, type Product } from "../db/repo";
import { useLocal } from "../lib/hooks";
import { formatMoney, num } from "../lib/money";
import { stockState, DEFAULT_LOW_AT } from "../lib/stock";
import { GST_RATES } from "../lib/tax";
import { uid, useApp } from "../state/app";
import { fonts, radius, space, usePalette } from "../theme";
import { Badge, Button, Chip, Divider, Empty, Field, Header, Icon, IconButton, Label, Screen, Tap } from "../ui/kit";
import { T } from "../ui/text";
import { Scanner } from "./Scanner";

export function Items({ openEditor, onBack }: { openEditor: (id: string | null) => void; onBack?: () => void }) {
  const { currency, linked, profile } = useApp();
  const c = usePalette();
  const [q, setQ] = useState("");
  const all = useLocal((db) => searchProducts(db, q, 400), [] as Product[], [q]);
  const [f, setF] = useState<"all" | "low" | "out">("all");
  const nLow = all.filter((p) => stockState(p.stock, p.low_at) === "low").length;
  const nOut = all.filter((p) => stockState(p.stock, p.low_at) === "out").length;
  const list = all.filter((p) => f === "all" || (f === "out" ? stockState(p.stock, p.low_at) === "out" : stockState(p.stock, p.low_at) === "low" || stockState(p.stock, p.low_at) === "out"));
  return (
    <Screen>
      <Header title="Items" onBack={onBack} right={<IconButton name="add" label="Add item" tone="accent" onPress={() => openEditor(null)} />} />
      <View style={{ paddingHorizontal: space.lg, paddingBottom: space.md }}>
        <View style={{ flexDirection: "row", alignItems: "center", backgroundColor: c.surface, borderRadius: radius.md + 2, borderWidth: 1, borderColor: c.borderStrong, paddingLeft: 14 }}>
          <Icon name="search" size={20} color={c.faint} />
          <TextInput accessibilityLabel="Search items" value={q} onChangeText={setQ} placeholder="Search name, SKU, barcode" placeholderTextColor={c.faint} autoCorrect={false}
            style={{ flex: 1, minHeight: 50, paddingHorizontal: 10, fontSize: 16, color: c.text, fontFamily: fonts.body }} />
        </View>
      </View>
      <View style={{ flexDirection: "row", gap: 8, paddingHorizontal: space.lg, paddingBottom: space.sm }}>
        <Chip title="All" active={f === "all"} onPress={() => setF("all")} />
        <Chip title={`Low stock (${nLow + nOut})`} active={f === "low"} onPress={() => setF("low")} />
        <Chip title={`Out of stock (${nOut})`} active={f === "out"} onPress={() => setF("out")} />
      </View>
      <FlatList data={list} keyExtractor={(p) => p.variant_id} contentContainerStyle={{ paddingBottom: 40 }} ItemSeparatorComponent={Divider}
        ListEmptyComponent={<Empty icon="pricetag-outline" title={q ? "No matching item" : "No items yet"} hint={q ? "Check the spelling." : "Add what you sell once: name, price, GST and stock."} action={q ? undefined : { title: "Add your first item", onPress: () => openEditor(null) }} />}
        renderItem={({ item }) => {
          return (
            <Tap label={item.name} onPress={() => openEditor(item.variant_id)} style={{ minHeight: 70, backgroundColor: c.surface, paddingHorizontal: space.lg, paddingVertical: 14, flexDirection: "row", alignItems: "center", gap: 12 }}>
              <View style={{ width: 44, height: 44, borderRadius: 12, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center" }}><T v="h3" color={c.accent}>{item.name.slice(0, 1).toUpperCase()}</T></View>
              <View style={{ flex: 1, gap: 2 }}>
                <T v="strong" numberOfLines={1}>{item.name}</T>
                <View style={{ flexDirection: "row", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
                  <T v="small" muted numberOfLines={1} style={{ flexShrink: 1 }}>{item.sku}</T>
                  {item.gst_rate ? <Badge text={`GST ${item.gst_rate}%`} /> : null}
                  {linked && item.origin === "local" && !item.pushed ? <Badge text="Not uploaded" tone="info" /> : null}
                </View>
              </View>
              <View style={{ alignItems: "flex-end", gap: 2 }}>
                <T v="money" style={{ fontFamily: fonts.monoBold }}>{item.price != null ? formatMoney(num(unitPriceFor(item, profile?.pricingMode ?? "INCLUSIVE")), currency) : "—"}</T>
                {(() => { const st = stockState(item.stock, item.low_at); return <T v="small" color={st === "out" ? c.negative : st === "low" ? c.warning : c.muted}>{st === "out" ? `Out of stock` : st === "untracked" ? "not tracked" : `${item.stock} ${item.unit_label}${st === "low" ? " · low" : ""}`}</T>; })()}
              </View>
            </Tap>
          );
        }} />
    </Screen>
  );
}

const slugSku = (name: string) => name.toUpperCase().replace(/[^A-Z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 24);
const EMPTY: ItemInput = { name: "", sku: "", barcode: "", hsn: "", price: "", gstRate: "", stock: "", unitLabel: "pcs" };

export function ItemEditor({ id, onClose }: { id: string | null; onClose: () => void }) {
  const { db, profile, changed, linked } = useApp();
  const c = usePalette();
  const [f, setF] = useState<ItemInput>({ ...EMPTY, gstRate: profile?.country === "IN" ? profile.defaultGst : "", priceBasis: profile?.pricingMode === "EXCLUSIVE" ? "EXCL" : "INCL" });
  const [existing, setExisting] = useState<Product | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [scan, setScan] = useState(false);
  const [lowAt, setLowAtText] = useState("");
  const set = <K extends keyof ItemInput>(k: K, v: ItemInput[K]) => setF((x) => ({ ...x, [k]: v }));

  useEffect(() => {
    if (!id || !db) return;
    void getItem(db, id).then((p) => {
      if (!p) return;
      setExisting(p);
      setLowAtText(p.low_at ?? "");
      setF({ name: p.name, sku: p.sku, barcode: p.barcode, hsn: p.hsn, price: p.price ?? "", gstRate: p.gst_rate ?? "", stock: p.stock ?? "", unitLabel: p.unit_label, priceBasis: p.price_basis, qtyDecimals: p.qty_decimals });
    });
  }, [id, db]);

  const readOnly = !!existing && !(existing.origin === "local" && !existing.pushed);

  async function save() {
    if (!db) return;
    if (!f.name.trim()) { setErr("Give the item a name."); return; }
    if (f.price.trim() && !(num(f.price) >= 0)) { setErr("Price must be a number."); return; }
    if (f.stock.trim() && !Number.isFinite(Number(f.stock))) { setErr("Stock must be a number."); return; }
    const dupe = await db.first("SELECT 1 FROM products WHERE archived=0 AND sku=? COLLATE NOCASE AND variant_id != ?", [f.sku.trim(), id ?? ""]);
    if (f.sku.trim() && dupe) { setErr("Another item already uses that SKU."); return; }
    if (existing) await updateItem(db, existing.variant_id, f); else await createItem(db, uid(), f, Date.now());
    await setLowAt(db, existing?.sku ?? (f.sku.trim() || slugSku(f.name)), lowAt);
    changed();
    onClose();
  }
  async function saveLowOnly() {
    if (!db || !existing) return;
    await setLowAt(db, existing.sku, lowAt);
    changed(); onClose();
  }
  function remove() {
    if (!db || !existing) return;
    Alert.alert("Remove this item?", "Past invoices keep it. You can add it again later.", [{ text: "Cancel", style: "cancel" }, { text: "Remove", style: "destructive", onPress: async () => { await archiveItem(db, existing.variant_id); changed(); onClose(); } }]);
  }

  return (
    <Modal visible animationType="slide" onRequestClose={onClose}>
      <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
        <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1 }}>
          <ScrollView contentContainerStyle={{ paddingBottom: 48 }} keyboardShouldPersistTaps="handled">
            <Header title={existing ? "Item" : "New item"} onBack={onClose} />
            <View style={{ padding: space.lg, gap: space.lg }}>
              {readOnly ? (
                <View style={{ flexDirection: "row", gap: 10, padding: 14, borderRadius: radius.lg, backgroundColor: c.infoSoft }}>
                  <Icon name="lock-closed-outline" size={20} color={c.info} /><T v="small" style={{ flex: 1 }}>This item lives on your server. Change its name, price or GST on the web so every device agrees.</T>
                </View>
              ) : null}
              <Field label="Name" value={f.name} onChangeText={(v) => { set("name", v); setErr(null); }} editable={!readOnly} placeholder="e.g. Steel bolt M8" autoCapitalize="words" error={err} />
              <View style={{ flexDirection: "row", gap: 12 }}>
                <View style={{ flex: 1 }}><Field label={`Price${profile ? ` (${profile.currency})` : ""}`} value={f.price} onChangeText={(v) => set("price", v.replace(/[^0-9.]/g, ""))} keyboardType="decimal-pad" editable={!readOnly} hint={f.priceBasis === "INCL" ? "Including GST" : "Before GST"} /></View>
                <View style={{ flex: 1 }}><Field label="Stock" value={f.stock} onChangeText={(v) => set("stock", v.replace(/[^0-9.]/g, ""))} keyboardType="decimal-pad" editable={!readOnly} hint="Blank = not tracked" /></View>
              </View>
              {!readOnly && profile?.country === "IN" ? (
                <View style={{ flexDirection: "row", gap: 8 }}>
                  <Chip title="Price includes GST" active={f.priceBasis === "INCL"} onPress={() => set("priceBasis", "INCL")} />
                  <Chip title="Price before GST" active={f.priceBasis === "EXCL"} onPress={() => set("priceBasis", "EXCL")} />
                </View>
              ) : null}
              {profile?.country === "IN" ? (
                <View style={{ gap: 8 }}><Label>GST rate</Label>
                  <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>{GST_RATES.map((r) => <Chip key={r} title={`${r}%`} active={f.gstRate === r} onPress={() => !readOnly && set("gstRate", r)} />)}</View></View>
              ) : null}
              <View style={{ flexDirection: "row", gap: 12 }}>
                <View style={{ flex: 1 }}><Field label="Unit" value={f.unitLabel} onChangeText={(v) => set("unitLabel", v)} editable={!readOnly} autoCapitalize="none" hint="pcs, kg, box…" /></View>
                <View style={{ flex: 1 }}><Field label="HSN / SAC" value={f.hsn} onChangeText={(v) => set("hsn", v)} keyboardType="number-pad" editable={!readOnly} hint={profile?.country === "IN" && Number(f.gstRate || "0") > 0 && !f.hsn.trim() ? "Needed before this item can be sent to a server" : "Optional"} /></View>
              </View>
              {!readOnly ? (
                <View style={{ gap: 8 }}><Label>Sold in</Label>
                  <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
                    <Chip title="By unit type" active={f.qtyDecimals == null} onPress={() => set("qtyDecimals", null)} />
                    <Chip title="Whole numbers" active={f.qtyDecimals === 0} onPress={() => set("qtyDecimals", 0)} />
                    <Chip title="Fractions (0.5, 2.25…)" active={f.qtyDecimals === 3} onPress={() => set("qtyDecimals", 3)} />
                  </View>
                  <T v="small" muted>“By unit type” allows fractions for kg, litre, metre… and whole numbers for pcs, box…</T>
                </View>
              ) : null}
              <Field label="SKU" value={f.sku} onChangeText={(v) => set("sku", v)} editable={!readOnly} autoCapitalize="characters" hint="Leave blank to generate one" />
              <View style={{ flexDirection: "row", gap: 10, alignItems: "flex-end" }}>
                <View style={{ flex: 1 }}><Field label="Barcode" value={f.barcode} onChangeText={(v) => set("barcode", v)} editable={!readOnly} keyboardType="number-pad" /></View>
                {!readOnly ? <IconButton name="barcode-outline" label="Scan barcode" onPress={() => setScan(true)} /> : null}
              </View>
              {(f.stock.trim() !== "" || existing?.stock != null) ? (
                <Field label="Warn me when stock is at or below" value={lowAt} onChangeText={(v) => setLowAtText(v.replace(/[^0-9.]/g, ""))} keyboardType="decimal-pad" hint={`Blank = ${DEFAULT_LOW_AT}. Shows on Home, the item list and while billing.`} />
              ) : null}
              {!readOnly ? <Button title={existing ? "Save changes" : "Add item"} onPress={() => void save()} icon="checkmark" /> : null}
              {existing && readOnly ? <Button title="Save low-stock level" onPress={() => void saveLowOnly()} icon="checkmark" kind="quiet" /> : null}
              {existing && !readOnly ? <Button title="Remove item" kind="danger" onPress={remove} /> : null}
              {!existing && !linked ? <T v="small" muted>Stock you enter here is your opening stock. It goes down as you sell.</T> : null}
            </View>
          </ScrollView>
        </KeyboardAvoidingView>
        <Scanner visible={scan} onCode={(code) => { set("barcode", code); setScan(false); }} onClose={() => setScan(false)} hint="Point at the item's barcode" />
      </SafeAreaView>
    </Modal>
  );
}
