import React, { useEffect, useState } from "react";
import { Alert, FlatList, KeyboardAvoidingView, Linking, Modal, Platform, ScrollView, TextInput, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { createParty, getParty, searchParties, updateParty, type Party, type PartyInput } from "../db/repo";
import { useLocal } from "../lib/hooks";
import { uid, useApp } from "../state/app";
import { fonts, radius, space, usePalette } from "../theme";
import { Badge, Button, Divider, Empty, Field, Header, Icon, IconButton, Screen, Tap } from "../ui/kit";
import { T } from "../ui/text";

export function Customers({ onBack, openEditor }: { onBack: () => void; openEditor: (id: string | null) => void }) {
  const { linked } = useApp();
  const c = usePalette();
  const [q, setQ] = useState("");
  const list = useLocal((db) => searchParties(db, q, 300), [] as Party[], [q]);
  return (
    <Screen>
      <Header title="Customers" onBack={onBack} right={<IconButton name="person-add-outline" label="Add customer" tone="accent" onPress={() => openEditor(null)} />} />
      <View style={{ paddingHorizontal: space.lg, paddingBottom: space.md }}>
        <View style={{ flexDirection: "row", alignItems: "center", backgroundColor: c.surface, borderRadius: radius.md + 2, borderWidth: 1, borderColor: c.borderStrong, paddingLeft: 14 }}>
          <Icon name="search" size={20} color={c.faint} />
          <TextInput accessibilityLabel="Search customers" value={q} onChangeText={setQ} placeholder="Name or phone" placeholderTextColor={c.faint}
            style={{ flex: 1, minHeight: 50, paddingHorizontal: 10, fontSize: 16, color: c.text, fontFamily: fonts.body }} />
        </View>
      </View>
      <FlatList data={list} keyExtractor={(p) => p.id} contentContainerStyle={{ paddingBottom: 40 }} ItemSeparatorComponent={Divider}
        ListEmptyComponent={<Empty icon="people-outline" title="No customers yet" hint="Save regulars to bill them in two taps." action={{ title: "Add a customer", onPress: () => openEditor(null) }} />}
        renderItem={({ item }) => (
          <Tap label={item.name} onPress={() => !item.is_walk_in && openEditor(item.id)} style={{ minHeight: 68, backgroundColor: c.surface, padding: space.lg, flexDirection: "row", alignItems: "center", gap: 12 }}>
            <View style={{ width: 44, height: 44, borderRadius: 22, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center" }}>
              {item.is_walk_in ? <Icon name="storefront-outline" size={20} color={c.accent} /> : <T v="h3" color={c.accent}>{item.name.slice(0, 1).toUpperCase()}</T>}
            </View>
            <View style={{ flex: 1, gap: 2 }}>
              <T v="strong" numberOfLines={1}>{item.name}</T>
              <View style={{ flexDirection: "row", gap: 8, alignItems: "center" }}>
                <T v="small" muted>{item.is_walk_in ? "Cash sale, no details" : item.phone || "No phone"}</T>
                {linked && item.origin === "local" && !item.pushed && !item.is_walk_in ? <Badge text="Not uploaded" tone="info" /> : null}
              </View>
            </View>
            {item.phone ? <IconButton name="call-outline" label={`Call ${item.name}`} onPress={() => void Linking.openURL(`tel:${item.phone}`)} /> : null}
          </Tap>
        )} />
    </Screen>
  );
}

export function CustomerEditor({ id, onClose }: { id: string | null; onClose: () => void }) {
  const { db, changed } = useApp();
  const c = usePalette();
  const [f, setF] = useState<PartyInput>({ name: "", phone: "", gstin: "", address: "" });
  const [existing, setExisting] = useState<Party | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const set = <K extends keyof PartyInput>(k: K, v: PartyInput[K]) => setF((x) => ({ ...x, [k]: v }));
  useEffect(() => {
    if (!id || !db) return;
    void getParty(db, id).then((p) => { if (p) { setExisting(p); setF({ name: p.name, phone: p.phone, gstin: p.gstin, address: p.address }); } });
  }, [id, db]);
  const readOnly = !!existing && !(existing.origin === "local" && !existing.pushed);

  async function save() {
    if (!db) return;
    if (!f.name.trim()) { setErr("Enter the customer's name."); return; }
    if (existing) await updateParty(db, existing.id, f); else await createParty(db, uid(), f, Date.now());
    changed();
    onClose();
  }
  void Alert;
  return (
    <Modal visible animationType="slide" onRequestClose={onClose}>
      <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
        <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1 }}>
          <ScrollView contentContainerStyle={{ paddingBottom: 48 }} keyboardShouldPersistTaps="handled">
            <Header title={existing ? "Customer" : "New customer"} onBack={onClose} />
            <View style={{ padding: space.lg, gap: space.lg }}>
              {readOnly ? (
                <View style={{ flexDirection: "row", gap: 10, padding: 14, borderRadius: radius.lg, backgroundColor: c.infoSoft }}>
                  <Icon name="lock-closed-outline" size={20} color={c.info} /><T v="small" style={{ flex: 1 }}>This customer lives on your server. Edit them on the web.</T>
                </View>
              ) : null}
              <Field label="Name" value={f.name} onChangeText={(v) => { set("name", v); setErr(null); }} editable={!readOnly} autoCapitalize="words" error={err} />
              <Field label="Phone" value={f.phone} onChangeText={(v) => set("phone", v)} keyboardType="phone-pad" editable={!readOnly} hint="With country code to message on WhatsApp, e.g. 919876543210" />
              <Field label="GSTIN (optional)" value={f.gstin} onChangeText={(v) => set("gstin", v.toUpperCase())} autoCapitalize="characters" autoCorrect={false} editable={!readOnly} />
              <Field label="Address (optional)" value={f.address} onChangeText={(v) => set("address", v)} multiline editable={!readOnly} style={{ minHeight: 76, paddingTop: 12, textAlignVertical: "top" }} />
              {!readOnly ? <Button title={existing ? "Save changes" : "Add customer"} onPress={() => void save()} icon="checkmark" /> : null}
            </View>
          </ScrollView>
        </KeyboardAvoidingView>
      </SafeAreaView>
    </Modal>
  );
}
