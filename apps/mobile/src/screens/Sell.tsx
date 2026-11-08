import * as Haptics from "expo-haptics";
import React, { useEffect, useState } from "react";
import { Animated, FlatList, Modal, Pressable, ScrollView, Switch, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import {
  createItem, createParty, frequentItems, productByCode, recentCustomers, searchParties, searchProducts, type Party, type Product,
} from "../db/repo";
import { dec, minorToString, toMinor } from "../lib/decimal";
import { useLocal } from "../lib/hooks";
import { useKeyboardLift } from "../lib/keyboard";
import { formatMinor } from "../lib/money";
import { unitPriceFor } from "../lib/sale";
import { shareSale } from "../lib/share";
import { GST_RATES, stateFromGstin } from "../lib/tax";
import { cartActions, cartHasErrors, useCart, type CartLine } from "../state/cart";
import { CheckoutError, priceCart, receivedFor } from "../state/checkout";
import { uid, useApp } from "../state/app";
import { fonts, radius, space, usePalette } from "../theme";
import { Badge, Button, Card, Chip, Divider, Empty, Field, Header, Icon, IconButton, Label, Screen, Tap } from "../ui/kit";
import { T } from "../ui/text";
import { QuoteDone } from "./Quotes";
import { Scanner } from "./Scanner";

const PAY: { key: string | null; label: string }[] = [{ key: "CASH", label: "Cash" }, { key: "UPI", label: "UPI" }, { key: "CARD", label: "Card" }, { key: null, label: "Pay later" }];

export function Sell({ onDone, addItem }: { onDone: (localId: string) => void; addItem: () => void }) {
  const { profile, saveCart, saveQuoteCart, linked, currency, db, changed } = useApp();
  const c = usePalette();
  const cart = useCart();
  const [q, setQ] = useState("");
  const [pickCustomer, setPickCustomer] = useState(false);
  const [custom, setCustom] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [scanHint, setScanHint] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const [done, setDone] = useState<{ id: string; number: string; totalMinor: bigint; receivedMinor: bigint } | null>(null);
  const [quoted, setQuoted] = useState<{ id: string; number: string; totalMinor: bigint } | null>(null);
  const { anchor, lift } = useKeyboardLift();

  const walkIn = useLocal((d) => d.first<Party>("SELECT * FROM parties WHERE is_walk_in=1 ORDER BY origin DESC LIMIT 1"), null);
  const results = useLocal((d) => searchProducts(d, q, 30), [] as Product[], [q]);
  const frequent = useLocal((d) => frequentItems(d, 8), [] as Product[]);
  const anyItems = useLocal((d) => d.first<{ n: number }>("SELECT COUNT(*) AS n FROM products WHERE archived=0 AND is_custom=0").then((r) => r?.n ?? 0), 0);

  // A fresh bill starts with the walk-in customer, so a cash sale never needs a name.
  useEffect(() => {
    if (!cart.customerId && walkIn && !cart.editing) cartActions.patch({ customerId: walkIn.id, customerName: walkIn.name });
  }, [cart.customerId, cart.editing, walkIn]);

  const customer = useLocal((d) => (cart.customerId ? d.first<Party>("SELECT * FROM parties WHERE id=?", [cart.customerId]) : Promise.resolve(null)), null, [cart.customerId]);
  const calc = priceCart(cart, profile?.gstState || null, stateFromGstin(customer?.gstin) ?? null);
  const received = receivedFor(cart, calc.totalMinor);
  const balance = calc.totalMinor - received;
  const errors = cartHasErrors(cart) || calc.lines.some((l) => l.invalid);
  const m = (x: bigint) => formatMinor(x, currency);
  const incl = cart.mode === "INCLUSIVE";

  function add(p: Product) {
    void Haptics.impactAsync(Haptics.ImpactFeedbackStyle.Light);
    cartActions.addProduct(p, cart.mode, unitPriceFor(p, cart.mode));
  }
  async function onScan(code: string) {
    if (!db) return;
    const p = await productByCode(db, code);
    if (p) { add(p); setScanHint(`Added ${p.name}`); } else setScanHint(`No item with code ${code}. Close the scanner to add it.`);
  }
  async function setMode(next: boolean) {
    // Prices are re-expressed for the new mode (not reinterpreted), so the same item shows the same real price either way.
    const mode = next ? "INCLUSIVE" : "EXCLUSIVE";
    cartActions.setMode(mode);
    if (!db) return;
    for (const l of cart.lines) {
      const p = await db.first<Product>("SELECT * FROM products WHERE variant_id=?", [l.variantId]);
      if (p && dec(l.price) === dec(unitPriceFor(p, cart.mode))) cartActions.setPrice(l.key, unitPriceFor(p, mode));
    }
  }

  async function save() {
    if (saving) return;
    setSaving(true); setProblem(null);
    try {
      const r = await saveCart();
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      setDone({ id: r.localId, number: r.number, totalMinor: r.totalMinor, receivedMinor: r.receivedMinor });
      setQ("");
    } catch (e) {
      setProblem(e instanceof CheckoutError || e instanceof Error ? e.message : "Couldn't save. Nothing was lost; try again.");
    } finally { setSaving(false); }
  }

  async function saveAsQuote() {
    if (saving) return;
    setSaving(true); setProblem(null);
    try {
      const r = await saveQuoteCart(30);
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      setQuoted({ id: r.localId, number: r.number, totalMinor: r.totalMinor });
      setQ("");
    } catch (e) {
      setProblem(e instanceof CheckoutError || e instanceof Error ? e.message : "Couldn't save the quotation. Nothing was lost; try again.");
    } finally { setSaving(false); }
  }

  if (quoted) return <QuoteDone info={quoted} currency={currency} onNew={() => setQuoted(null)} />;
  if (done) return <Done info={done} currency={currency} onView={() => { const id = done.id; setDone(null); onDone(id); }} onNew={() => setDone(null)} />;

  const canSave = !!cart.customerId && cart.lines.length > 0 && !errors && !saving;
  // With nothing typed: the items sold most often as one-tap chips; until there is any history, the first few items as a list so nothing is hidden.
  const asList = !!q.trim() || frequent.length === 0;
  const showing = q.trim() ? results.slice(0, 8) : frequent.length ? frequent : results.slice(0, 8);
  const owedBackText = cart.corrects && dec(cart.corrects.owedBack) > 0n ? m(toMinor(dec(cart.corrects.owedBack))) : null;

  return (
    <Screen>
      <ScrollView contentContainerStyle={{ paddingBottom: 290 }} keyboardShouldPersistTaps="handled">
        <Header title={cart.editing ? "Edit invoice" : "New sale"} sub={cart.editing ? cart.editing.number : cart.corrects ? `Re-issuing ${cart.corrects.number}` : undefined}
          right={cart.lines.length > 0 || cart.editing ? <Tap label="Clear bill" onPress={() => { cartActions.clear(profile?.pricingMode ?? "INCLUSIVE"); setProblem(null); }} style={{ minHeight: 44, paddingHorizontal: 12, justifyContent: "center" }}><T v="strong" color={c.negative}>{cart.editing ? "Cancel edit" : "Clear"}</T></Tap> : undefined} />
        <View style={{ padding: space.lg, gap: space.lg, paddingTop: space.xs }}>
          {cart.fromQuote ? (
            <Card style={{ backgroundColor: c.accentSoft, borderColor: c.accent }}>
              <T v="strong" color={c.accent}>From quotation {cart.fromQuote.number}</T>
              <T v="small" color={c.accent}>Check the items and prices, then save. The quotation is marked as converted.</T>
            </Card>
          ) : null}
          {cart.corrects ? (
            <Card style={{ backgroundColor: c.warningSoft, borderColor: c.warning }}>
              <T v="strong" color={c.warning}>Correcting {cart.corrects.number}</T>
              <T v="small" color={c.warning}>The old invoice is cancelled. Saving this one replaces it{owedBackText ? `, and ${owedBackText} already received from the customer is applied to it` : ""}.</T>
            </Card>
          ) : null}

          <Tap label={`Customer: ${cart.customerName || "none"}. Change`} onPress={() => setPickCustomer(true)}
            style={{ minHeight: 64, flexDirection: "row", alignItems: "center", gap: 12, backgroundColor: c.surface, borderRadius: radius.lg + 2, borderWidth: 1, borderColor: c.border, paddingHorizontal: space.lg }}>
            <View style={{ width: 40, height: 40, borderRadius: 20, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center" }}><Icon name="person" size={20} color={c.accent} /></View>
            <View style={{ flex: 1 }}><T v="label" muted>Customer</T><T v="h3" numberOfLines={1}>{cart.customerName || "Choose…"}</T></View>
            <T v="small" color={c.accent} style={{ fontFamily: fonts.bodySemi }}>Change</T>
          </Tap>

          <View style={{ flexDirection: "row", alignItems: "center", gap: 12, backgroundColor: c.surface, borderRadius: radius.lg, borderWidth: 1, borderColor: c.border, paddingHorizontal: space.lg, minHeight: 56, paddingVertical: 8 }}>
            <View style={{ flex: 1 }}>
              <T v="strong">Price includes GST</T>
              <T v="small" muted>{incl ? "Type the final price; GST is worked out inside it." : "Type the price before GST; GST is added on top."}</T>
            </View>
            <Switch accessibilityLabel="Price includes GST" value={incl} onValueChange={(v) => void setMode(v)} trackColor={{ true: c.accent, false: c.borderStrong }} thumbColor="#fff" />
          </View>

          <View style={{ flexDirection: "row", gap: 10 }}>
            <View style={{ flex: 1, flexDirection: "row", alignItems: "center", backgroundColor: c.surface, borderRadius: radius.md + 2, borderWidth: 1, borderColor: c.borderStrong, paddingLeft: 14 }}>
              <Icon name="search" size={20} color={c.faint} />
              <TextInput accessibilityLabel="Search items" value={q} onChangeText={setQ} placeholder="Name, SKU or barcode" placeholderTextColor={c.faint} autoCorrect={false} returnKeyType="search"
                onSubmitEditing={() => { const only = results.length === 1 ? results[0] : results.find((r) => r.barcode === q.trim() || r.sku.toLowerCase() === q.trim().toLowerCase()); if (only) { add(only); setQ(""); } }}
                style={{ flex: 1, minHeight: 52, paddingHorizontal: 10, fontSize: 17, color: c.text, fontFamily: fonts.body }} />
              {q ? <Tap label="Clear search" onPress={() => setQ("")} style={{ width: 44, height: 44, alignItems: "center", justifyContent: "center" }}><Icon name="close-circle" size={20} color={c.faint} /></Tap> : null}
            </View>
            <IconButton name="barcode-outline" label="Scan barcode" tone="accent" onPress={() => { setScanHint(null); setScanning(true); }} />
          </View>

          <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
            <Chip title="+ New item" onPress={addItem} />
            <Chip title="+ One-off item" onPress={() => setCustom(true)} />
          </View>

          {anyItems === 0 && !q ? (
            <Card><Empty icon="pricetag-outline" title="Add your first item" hint="Or use a one-off item to bill right now without setting anything up." action={{ title: "Add an item", onPress: addItem }} /></Card>
          ) : q.trim() && results.length === 0 ? (
            <Card><Empty icon="search" title="No matching item" hint="Check the spelling, or add it as a new item. This bill stays as it is." action={{ title: `Add “${q.trim()}” as a new item`, onPress: addItem }} /></Card>
          ) : showing.length > 0 ? (
            <View style={{ gap: 8 }}>
              <Label>{q.trim() ? "Matches" : frequent.length ? "Quick add" : "Your items"}</Label>
              {asList ? (
                <Card style={{ padding: 0, overflow: "hidden" }}>
                  {showing.map((p, i) => (
                    <View key={p.variant_id}>
                      <Tap label={`Add ${p.name}`} onPress={() => add(p)} style={{ minHeight: 60, flexDirection: "row", alignItems: "center", padding: space.md + 2, gap: 12 }}>
                        <View style={{ flex: 1 }}>
                          <T v="strong" numberOfLines={1}>{p.name}</T>
                          <T v="small" muted>{p.stock != null ? `${p.stock} ${p.unit_label} in stock` : p.unit_label}{p.sku ? ` · ${p.sku}` : ""}</T>
                        </View>
                        <T v="money">{p.price != null ? m(toMinor(dec(unitPriceFor(p, cart.mode)))) : "—"}</T>
                        <View style={{ width: 34, height: 34, borderRadius: 17, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center" }}><Icon name="add" size={20} color={c.accent} /></View>
                      </Tap>
                      {i < showing.length - 1 ? <Divider /> : null}
                    </View>
                  ))}
                </Card>
              ) : (
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                  {showing.map((p) => <Chip key={p.variant_id} title={p.name} onPress={() => add(p)} />)}
                </View>
              )}
            </View>
          ) : null}

          {cart.lines.length > 0 ? (
            <View style={{ gap: 10 }}>
              <Label>On this bill</Label>
              <Card style={{ padding: 0 }}>
                {cart.lines.map((l, i) => (
                  <View key={l.key}>
                    <LineEditor line={l} mode={cart.mode} lineTotal={m(calc.lines[i]?.grossMinor ?? 0n)} linked={linked} />
                    {i < cart.lines.length - 1 ? <Divider /> : null}
                  </View>
                ))}
              </Card>

              {!cart.editing ? (
                <>
                  <Label>Payment</Label>
                  <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
                    {PAY.map((p) => <Chip key={p.label} title={p.label} active={cart.payMethod === p.key} onPress={() => cartActions.patch(p.key && cart.payMethod ? { payMethod: p.key } : { payMethod: p.key, receivedText: null })} />)}
                  </View>
                  {cart.payMethod ? (
                    <Field label={`Amount received (${currency})`} value={cart.receivedText ?? minorToString(calc.totalMinor)} keyboardType="decimal-pad"
                      onChangeText={(t) => cartActions.patch({ receivedText: t.replace(/[^0-9.]/g, "") })}
                      hint={balance > 0n ? `${m(balance)} will stay due from ${cart.customerName || "the customer"}.` : "Paid in full."} />
                  ) : null}
                  {cart.payMethod ? (
                    <View style={{ flexDirection: "row", gap: 8 }}>
                      <Chip title="Full amount" active={received === calc.totalMinor} onPress={() => cartActions.patch({ receivedText: null })} />
                      <Chip title="Half" active={received * 2n === calc.totalMinor} onPress={() => cartActions.patch({ receivedText: minorToString(calc.totalMinor / 2n) })} />
                      <Chip title="Nothing yet" active={received === 0n} onPress={() => cartActions.patch({ receivedText: "0" })} />
                    </View>
                  ) : null}
                </>
              ) : <T v="small" muted>Payments already recorded stay as they are. Add or reverse them from the invoice.</T>}
              {balance > 0n && !cart.editing ? (
                <View style={{ gap: 8 }}>
                  <Label>Due in</Label>
                  <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>
                    {[{ d: null, t: "No date" }, { d: 7, t: "7 days" }, { d: 15, t: "15 days" }, { d: 30, t: "30 days" }].map((o) => <Chip key={o.t} title={o.t} active={cart.dueDays === o.d} onPress={() => cartActions.patch({ dueDays: o.d })} />)}
                  </View>
                  {customer?.is_walk_in ? <T v="small" color={c.warning}>This is the walk-in customer. To track what they owe, choose a named customer.</T> : null}
                </View>
              ) : null}
              <Field label="Note on invoice (optional)" value={cart.note} onChangeText={(t) => cartActions.patch({ note: t })} multiline />
            </View>
          ) : null}
          {problem ? <Card style={{ backgroundColor: c.negativeSoft, borderColor: c.negative }}><T v="strong" color={c.negative}>{problem}</T></Card> : null}
        </View>
      </ScrollView>

      <View ref={anchor} pointerEvents="none" style={{ position: "absolute", left: 0, right: 0, bottom: 0, height: 0 }} />
      <View style={{ position: "absolute", left: 0, right: 0, bottom: lift, padding: space.lg, backgroundColor: c.surface, borderTopWidth: 1, borderTopColor: c.border, gap: 10 }}>
        <View style={{ gap: 3 }}>
          <Row k={`${cart.lines.length} item${cart.lines.length === 1 ? "" : "s"} · taxable value`} v={m(calc.taxableMinor)} />
          {calc.components.map((x) => <Row key={x.label} k={x.label} v={m(x.minor)} />)}
          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "baseline" }}>
            <View><T v="h3">Total</T>{incl ? <T v="small" muted>GST included</T> : null}</View>
            <T v="moneyLg" accessibilityLabel={`Total ${m(calc.totalMinor)}`}>{m(calc.totalMinor)}</T>
          </View>
        </View>
        <Button title={cart.editing ? "Save changes" : balance > 0n && received > 0n ? "Save · part paid" : balance > 0n ? "Save · to collect" : "Save invoice"} onPress={() => void save()} disabled={!canSave} busy={saving} icon="checkmark-circle-outline" />
        {!cart.editing && !cart.corrects ? <Button title="Save as quotation" kind="quiet" icon="document-text-outline" onPress={() => void saveAsQuote()} disabled={!cart.customerId || cart.lines.length === 0 || errors || saving} /> : null}
      </View>

      <CustomerPicker visible={pickCustomer} onClose={() => setPickCustomer(false)} onPick={(p) => { cartActions.patch({ customerId: p.id, customerName: p.name }); setPickCustomer(false); }} />
      <OneOff visible={custom} onClose={() => setCustom(false)} mode={cart.mode} onMade={(p) => { setCustom(false); add(p); changed(); }} />
      <Scanner visible={scanning} onCode={(code) => void onScan(code)} onClose={() => setScanning(false)} hint={scanHint} />
    </Screen>
  );
}

const Row = ({ k, v }: { k: string; v: string }) => <View style={{ flexDirection: "row", justifyContent: "space-between" }}><T v="small" muted>{k}</T><T v="money" style={{ fontSize: 14 }}>{v}</T></View>;

/** One bill line. The quantity is a real text field (type 2.5 for two and a half kilos); +/- are conveniences, not the only way in. */
function LineEditor({ line: l, mode, lineTotal, linked }: { line: CartLine; mode: "INCLUSIVE" | "EXCLUSIVE"; lineTotal: string; linked: boolean }) {
  const c = usePalette();
  const [disc, setDisc] = useState(dec(l.discount) > 0n);
  const over = l.stock != null && !l.qtyError && dec(l.qty) > dec(l.stock);
  const noPrice = dec(l.price) === 0n;
  return (
    <View style={{ padding: space.lg, gap: 10 }}>
      <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
        <View style={{ flex: 1 }}>
          <T v="strong" numberOfLines={2}>{l.name}</T>
          <T v="small" muted>{l.gstRate && dec(l.gstRate) > 0n ? `GST ${l.gstRate}%` : "No GST"} · per {l.unitLabel}</T>
        </View>
        <T v="money" style={{ fontFamily: fonts.monoBold }}>{lineTotal}</T>
      </View>
      <View style={{ flexDirection: "row", alignItems: "flex-start", gap: 8 }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}>
          <Stepper label={`Decrease ${l.name}`} icon="remove" onPress={() => cartActions.step(l.key, -1)} />
          <TextInput accessibilityLabel={`Quantity of ${l.name} in ${l.unitLabel}`} value={l.qtyText} onChangeText={(t) => cartActions.setQtyText(l.key, t.replace(",", "."))}
            keyboardType={l.qtyDecimals === 0 ? "number-pad" : "decimal-pad"} selectTextOnFocus
            style={{ width: 74, minHeight: 44, borderRadius: radius.sm + 2, borderWidth: l.qtyError ? 2 : 1, borderColor: l.qtyError ? c.negative : c.border, color: c.text, textAlign: "center", fontSize: 17, fontFamily: fonts.monoBold }} />
          <Stepper label={`Increase ${l.name}`} icon="add" onPress={() => cartActions.step(l.key, 1)} />
        </View>
        <T v="small" muted style={{ paddingTop: 13 }}>×</T>
        <TextInput accessibilityLabel={`${mode === "INCLUSIVE" ? "Price including GST" : "Price before GST"} of ${l.name}`} value={l.price}
          onChangeText={(t) => cartActions.setPrice(l.key, t.replace(/[^0-9.]/g, ""))} keyboardType="decimal-pad" selectTextOnFocus
          style={{ flex: 1, minWidth: 84, minHeight: 44, borderRadius: radius.sm + 2, borderWidth: noPrice ? 2 : 1, borderColor: noPrice ? c.negative : c.border, color: c.text, paddingHorizontal: 10, fontSize: 16, textAlign: "right", fontFamily: fonts.mono }} />
        <Tap label={`Remove ${l.name}`} onPress={() => cartActions.remove(l.key)} style={{ width: 44, height: 44, alignItems: "center", justifyContent: "center" }}><Icon name="trash-outline" size={20} color={c.negative} /></Tap>
      </View>
      <T v="small" muted>{mode === "INCLUSIVE" ? "Price includes GST" : "Price before GST"} · per {l.unitLabel}</T>
      {l.qtyError ? <T v="small" color={c.negative}>{l.qtyError}</T> : null}
      {noPrice ? <T v="small" color={c.negative}>Set a price before saving.</T> : null}
      {over ? <T v="small" color={c.warning}>{linked ? `The last synced stock is ${l.stock} ${l.unitLabel}. The server may refuse this sale if it is still short.` : `This phone counts ${l.stock} ${l.unitLabel} in stock. You can still sell it; stock will show negative.`}</T> : null}
      {disc ? (
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
          <T v="small" muted style={{ flex: 1 }}>Discount (amount off this line)</T>
          <TextInput accessibilityLabel={`Discount on ${l.name}`} value={l.discount === "0" ? "" : l.discount} placeholder="0" placeholderTextColor={c.faint}
            onChangeText={(t) => cartActions.setDiscount(l.key, t.replace(/[^0-9.]/g, "") || "0")} keyboardType="decimal-pad"
            style={{ width: 110, minHeight: 40, borderRadius: radius.sm + 2, borderWidth: 1, borderColor: c.border, color: c.text, paddingHorizontal: 10, textAlign: "right", fontFamily: fonts.mono }} />
        </View>
      ) : <Tap label={`Add discount to ${l.name}`} onPress={() => setDisc(true)} style={{ minHeight: 36, justifyContent: "center", alignSelf: "flex-start" }}><T v="small" color={c.accent} style={{ fontFamily: fonts.bodySemi }}>+ Discount</T></Tap>}
    </View>
  );
}

function Stepper({ label, icon, onPress }: { label: string; icon: "add" | "remove"; onPress: () => void }) {
  const c = usePalette();
  return (
    <Tap label={label} onPress={onPress} style={{ width: 44, height: 44, borderRadius: 22, alignItems: "center", justifyContent: "center", backgroundColor: c.surfaceAlt }}>
      <Icon name={icon} size={22} />
    </Tap>
  );
}

/** A thing sold once, without cluttering the catalogue: still a real item underneath so it posts to the server like any other. */
function OneOff({ visible, onClose, mode, onMade }: { visible: boolean; onClose: () => void; mode: "INCLUSIVE" | "EXCLUSIVE"; onMade: (p: Product) => void }) {
  const c = usePalette();
  const { db, profile } = useApp();
  const [name, setName] = useState(""); const [price, setPrice] = useState(""); const [rate, setRate] = useState(profile?.defaultGst ?? "18");
  const ok = name.trim().length > 0 && dec(price) > 0n;
  async function make() {
    if (!db || !ok) return;
    const id = uid();
    await createItem(db, id, { name: name.trim(), sku: "", barcode: "", hsn: "", price, gstRate: rate, stock: "", unitLabel: "pcs", priceBasis: mode === "INCLUSIVE" ? "INCL" : "EXCL", isCustom: true }, Date.now());
    const p = await db.first<Product>("SELECT * FROM products WHERE variant_id=?", [id]);
    setName(""); setPrice("");
    if (p) onMade(p);
  }
  return (
    <Modal visible={visible} animationType="slide" onRequestClose={onClose}>
      <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
        <Header title="One-off item" onBack={onClose} sub="For something you sell once. It won't clutter your item list." />
        <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg }} keyboardShouldPersistTaps="handled">
          <Field label="What is it?" value={name} onChangeText={setName} autoFocus />
          <Field label={mode === "INCLUSIVE" ? "Price including GST" : "Price before GST"} value={price} onChangeText={(t) => setPrice(t.replace(/[^0-9.]/g, ""))} keyboardType="decimal-pad" />
          <View style={{ gap: 8 }}><Label>GST rate</Label><View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>{GST_RATES.map((r) => <Chip key={r} title={`${r}%`} active={rate === r} onPress={() => setRate(r)} />)}</View></View>
          <Button title="Add to bill" icon="add" onPress={() => void make()} disabled={!ok} />
        </ScrollView>
      </SafeAreaView>
    </Modal>
  );
}

/** The moment after saving. A short confirmation with the next obvious steps. */
function Done({ info, currency, onView, onNew }: { info: { id: string; number: string; totalMinor: bigint; receivedMinor: bigint }; currency: string; onView: () => void; onNew: () => void }) {
  const c = usePalette();
  const { db, profile } = useApp();
  const [pop] = useState(() => new Animated.Value(0));
  useEffect(() => { Animated.spring(pop, { toValue: 1, friction: 5, tension: 90, useNativeDriver: true }).start(); }, [pop]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const due = info.totalMinor - info.receivedMinor;
  async function share() {
    if (!db || !profile) return;
    setBusy(true); setErr(null);
    try { await shareSale(db, info.id, profile, currency); } catch { setErr("Couldn't create the PDF. The invoice is saved; open it and try again."); } finally { setBusy(false); }
  }
  return (
    <Screen>
      <View style={{ flex: 1, padding: space.xl, justifyContent: "center", gap: space.xl }}>
        <View style={{ alignItems: "center", gap: 14 }}>
          <Animated.View style={{ transform: [{ scale: pop }], width: 96, height: 96, borderRadius: 48, backgroundColor: c.positiveSoft, alignItems: "center", justifyContent: "center" }}>
            <Icon name="checkmark" size={54} color={c.positive} />
          </Animated.View>
          <T v="h2">Invoice saved</T>
          <T v="moneyHero" style={{ fontSize: 34 }}>{formatMinor(info.totalMinor, currency)}</T>
          <T v="body" muted>{info.number}</T>
          {info.receivedMinor > 0n ? <T v="body" muted>{`Received ${formatMinor(info.receivedMinor, currency)}`}</T> : null}
          {due > 0n ? <Badge tone="warn" icon="time-outline" text={`${formatMinor(due, currency)} to collect`} /> : <Badge tone="good" icon="checkmark-circle" text="Paid in full" />}
        </View>
        {err ? <T v="small" color={c.negative} style={{ textAlign: "center" }}>{err}</T> : null}
        <View style={{ gap: 10 }}>
          <Button title="Share invoice (PDF)" icon="share-outline" onPress={() => void share()} busy={busy} />
          <Button title="View invoice" kind="quiet" onPress={onView} />
          <Button title="New sale" kind="quiet" icon="add" onPress={onNew} />
        </View>
      </View>
    </Screen>
  );
}

export function CustomerPicker({ visible, onClose, onPick, noWalkIn }: { visible: boolean; onClose: () => void; onPick: (p: Party) => void; noWalkIn?: boolean }) {
  const c = usePalette();
  const [q, setQ] = useState("");
  const list = useLocal(async (d) => (await searchParties(d, q)).filter((p) => !(noWalkIn && p.is_walk_in)), [] as Party[], [q, visible, noWalkIn]);
  const recent = useLocal((d) => recentCustomers(d, 5), [] as Party[], [visible]);
  const { db, changed } = useApp();
  async function quickAdd() {
    if (!db) return;
    const id = uid();
    const text = q.trim();
    const isPhone = /^[0-9+\s-]{7,}$/.test(text);
    await createParty(db, id, { name: isPhone ? "New customer" : text, phone: isPhone ? text : "", gstin: "", address: "" }, Date.now());
    const p = await db.first<Party>("SELECT * FROM parties WHERE id=?", [id]);
    changed(); setQ("");
    if (p) onPick(p);
  }
  return (
    <Modal visible={visible} animationType="slide" onRequestClose={onClose}>
      <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
        <Header title="Customer" onBack={onClose} />
        <View style={{ paddingHorizontal: space.lg, paddingBottom: space.md, gap: 10 }}>
          <TextInput accessibilityLabel="Search customers" autoFocus value={q} onChangeText={setQ} placeholder="Name or phone" placeholderTextColor={c.faint}
            style={{ minHeight: 52, borderRadius: radius.md + 2, borderWidth: 1, borderColor: c.borderStrong, backgroundColor: c.surface, color: c.text, paddingHorizontal: 14, fontSize: 17, fontFamily: fonts.body }} />
          {!q && recent.length > 0 ? <View style={{ flexDirection: "row", gap: 8, flexWrap: "wrap" }}>{recent.map((p) => <Chip key={p.id} title={p.name} onPress={() => onPick(p)} />)}</View> : null}
        </View>
        {q.trim().length > 1 && !list.some((p) => p.name.toLowerCase() === q.trim().toLowerCase()) ? (
          <View style={{ paddingHorizontal: space.lg, paddingBottom: space.sm }}>
            <Button kind="quiet" icon="person-add-outline" title={`Add “${q.trim()}” as a new customer`} onPress={() => void quickAdd()} />
          </View>
        ) : null}
        <FlatList data={list} keyExtractor={(p) => p.id} keyboardShouldPersistTaps="handled" ListEmptyComponent={<Empty icon="people-outline" title="No customer found" hint="Type a name above to add them without leaving this bill." />}
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
