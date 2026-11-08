import * as Print from "expo-print";
import React, { useState } from "react";
import { Alert, ScrollView, Switch, View } from "react-native";
import { exportBackup, pickBackup, saveSafetyCopy } from "../lib/share";
import { BackupError, inspectBackup, restoreBackup } from "../lib/backup";
import { ago } from "../lib/hooks";
import { type Profile } from "../db/repo";
import { GST_RATES } from "../lib/tax";
import { TEMPLATES, invoiceHtml, paperFor, sampleInvoice, type InvoiceTemplate } from "../lib/invoice";
import { version } from "../../package.json";
import { useApp } from "../state/app";
import { space, usePalette } from "../theme";
import { Button, Card, Chip, Field, Header, Icon, Label, Screen, Tap, type IconName } from "../ui/kit";
import { T } from "../ui/text";

function Row({ icon, title, sub, onPress, tone }: { icon: IconName; title: string; sub?: string; onPress: () => void; tone?: string }) {
  const c = usePalette();
  return (
    <Tap label={title} onPress={onPress} style={{ minHeight: 64, flexDirection: "row", alignItems: "center", gap: 14, padding: space.lg }}>
      <View style={{ width: 40, height: 40, borderRadius: 12, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center" }}><Icon name={icon} size={20} color={tone ?? c.accent} /></View>
      <View style={{ flex: 1 }}><T v="strong">{title}</T>{sub ? <T v="small" muted>{sub}</T> : null}</View>
      <Icon name="chevron-forward" size={18} color={c.faint} />
    </Tap>
  );
}

export function More({ openCustomers, openProfile, openItems, openDesign, openQuotes }: { openCustomers: () => void; openProfile: () => void; openItems: () => void; openDesign: () => void; openQuotes: () => void }) {
  const { changed, profile, saveProfile, linked, server, email, lastSyncAt, counts, syncNow, syncing, online, logout, disconnect, startLinking, db, businessName } = useApp();
  const c = usePalette();
  async function startRestore() {
    if (!db) return;
    try {
      const picked = await pickBackup();
      if (!picked) return;
      const { file, counts } = inspectBackup(picked.text);
      const lines = [`${counts.sales ?? 0} invoices`, `${counts.payments ?? "no"} payments`, `${counts.parties ?? 0} customers`, `${counts.products ?? 0} items`, `${counts.quotes ?? 0} quotations`].join(", ");
      Alert.alert("Replace everything on this phone?", `${picked.name} was made ${file.exportedAt ? new Date(file.exportedAt).toLocaleString() : "at an unknown time"} and holds ${lines}.\n\nEverything now on this phone is replaced. A copy of what is there now is saved first.`, [
        { text: "Keep what I have", style: "cancel" },
        { text: "Replace", style: "destructive", onPress: async () => {
          try {
            await saveSafetyCopy(db);
            await restoreBackup(db, picked.text);
            changed();
            Alert.alert("Restored", "Your records are back. Check Home and Invoices.");
          } catch (e) { Alert.alert("Nothing was changed", e instanceof BackupError || e instanceof Error ? e.message : "The backup could not be restored."); }
        } },
      ]);
    } catch (e) { Alert.alert("Couldn't open that file", e instanceof BackupError || e instanceof Error ? e.message : "Try another file."); }
  }
  async function guard(fn: () => Promise<string | null>) { const m = await fn(); if (m) Alert.alert("Not yet", m); }
  return (
    <Screen>
      <ScrollView contentContainerStyle={{ paddingBottom: 40 }}>
        <Header title="More" />
        <View style={{ padding: space.lg, gap: space.lg, paddingTop: space.xs }}>
          <Card style={{ padding: 0 }}>
            <Row icon="business-outline" title="Business details" sub={businessName} onPress={openProfile} />
            <View style={{ height: 1, backgroundColor: c.border }} />
            <Row icon="people-outline" title="Customers" onPress={openCustomers} />
            <View style={{ height: 1, backgroundColor: c.border }} />
            <Row icon="cube-outline" title="Items & stock" onPress={openItems} />
            <View style={{ height: 1, backgroundColor: c.border }} />
            <Row icon="document-text-outline" title="Quotations" sub="Offers you have sent; not counted as sales" onPress={openQuotes} />
          </Card>

          <Label>Billing</Label>
          <Card style={{ padding: 0 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 12, padding: space.lg }}>
              <View style={{ flex: 1 }}>
                <T v="strong">Prices include GST</T>
                <T v="small" muted>{profile?.pricingMode === "EXCLUSIVE" ? "New bills start with prices before GST (tax added on top)." : "New bills start with the final price; GST is worked out inside it."}</T>
              </View>
              <Switch accessibilityLabel="Prices include GST by default" value={profile?.pricingMode !== "EXCLUSIVE"} trackColor={{ true: c.accent, false: c.borderStrong }} thumbColor="#fff"
                onValueChange={(v) => { if (profile) void saveProfile({ ...profile, pricingMode: v ? "INCLUSIVE" : "EXCLUSIVE" }); }} />
            </View>
            <View style={{ height: 1, backgroundColor: c.border }} />
            <Row icon="document-text-outline" title="Invoice design" sub={TEMPLATES.find((t) => t.key === (profile?.invoiceTemplate ?? "classic"))?.label} onPress={openDesign} />
          </Card>

          <Label>Server</Label>
          {!linked ? (
            <Card style={{ gap: 12 }}>
              <View style={{ flexDirection: "row", gap: 12, alignItems: "center" }}><Icon name="phone-portrait-outline" size={26} color={c.accent} /><T v="h3">Working on this phone</T></View>
              <T v="small" muted>Everything is saved on this phone. Connect your Rechvix server to back it up, share it with your team and see it on the web. Your items, customers and invoices upload and merge safely.</T>
              <Button title="Connect a server" icon="link-outline" onPress={startLinking} />
            </Card>
          ) : (
            <Card style={{ gap: 8 }}>
              <KV k="Server" v={(server ?? "").replace(/^https?:\/\//, "")} /><KV k="Signed in as" v={email ?? "—"} />
              <KV k="Last synced" v={ago(lastSyncAt)} /><KV k="Waiting to sync" v={String(counts.pending + counts.syncing)} /><KV k="Need attention" v={String(counts.failed)} />
              <Button title="Sync now" kind="quiet" icon="sync-outline" busy={syncing} disabled={!online} onPress={() => void syncNow()} />
              <Button title="Sign out" kind="quiet" onPress={() => void guard(logout)} />
              <Button title="Work without the server" kind="danger" onPress={() => Alert.alert("Work without the server?", "Your items, customers and invoices all stay on this phone. You can reconnect any time.", [{ text: "Cancel", style: "cancel" }, { text: "Disconnect", style: "destructive", onPress: () => void disconnect() }])} />
            </Card>
          )}

          <Label>Your data</Label>
          <Card style={{ padding: 0 }}>
            <Row icon="download-outline" title="Export a backup" sub="One file with everything on this phone, payments included" onPress={() => db && void exportBackup(db)} />
            <View style={{ height: 1, backgroundColor: c.border }} />
            <Row icon="cloud-upload-outline" title="Restore from a backup" sub="Replaces everything on this phone with a backup file" onPress={() => void startRestore()} />
          </Card>
          <T v="small" muted style={{ textAlign: "center" }}>Rechvix {version}</T>
        </View>
      </ScrollView>
    </Screen>
  );
}

function KV({ k, v }: { k: string; v: string }) {
  return <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}><T v="small" muted>{k}</T><T v="strong" style={{ fontSize: 14, flexShrink: 1, textAlign: "right" }} numberOfLines={1}>{v}</T></View>;
}

export function ProfileEditor({ onClose }: { onClose: () => void }) {
  const { profile, saveProfile, org } = useApp();
  const [p, setP] = useState<Profile>(profile!);
  const set = <K extends keyof Profile>(k: K, v: Profile[K]) => setP((x) => ({ ...x, [k]: v }));
  return (
    <Screen>
      <ScrollView contentContainerStyle={{ paddingBottom: 48 }} keyboardShouldPersistTaps="handled">
        <Header title="Business details" onBack={onClose} />
        <View style={{ padding: space.lg, gap: space.lg }}>
          <Field label="Business name" value={p.name} onChangeText={(v) => set("name", v)} autoCapitalize="words" />
          <Field label="Phone" value={p.phone} onChangeText={(v) => set("phone", v)} keyboardType="phone-pad" />
          <Field label="Address" value={p.address} onChangeText={(v) => set("address", v)} multiline style={{ minHeight: 76, paddingTop: 12, textAlignVertical: "top" }} />
          <Field label="GSTIN" value={p.gstin} onChangeText={(v) => { const g = v.toUpperCase(); set("gstin", g); if (/^\d\d/.test(g)) set("gstState", g.slice(0, 2)); }} autoCapitalize="characters" autoCorrect={false} />
          <Field label="Invoice number prefix" value={p.invoicePrefix} onChangeText={(v) => set("invoicePrefix", v)} autoCapitalize="characters" hint={org ? "Used until a sale reaches your server, which then numbers it." : "Invoices are numbered INV-0001, INV-0002…"} />
          {p.country === "IN" ? (
            <View style={{ gap: 8 }}><Label>Usual GST rate for new items</Label>
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>{GST_RATES.map((r) => <Chip key={r} title={`${r}%`} active={p.defaultGst === r} onPress={() => set("defaultGst", r)} />)}</View></View>
          ) : null}
          <Button title="Save" icon="checkmark" onPress={async () => { await saveProfile(p); onClose(); }} />
        </View>
      </ScrollView>
    </Screen>
  );
}

/** Pick how invoices look. Each design is shown as a small drawing; "Preview" opens a real sample invoice in the system print preview. */
export function InvoiceDesign({ onClose }: { onClose: () => void }) {
  const { profile, saveProfile, currency } = useApp();
  const c = usePalette();
  const [busy, setBusy] = useState<string | null>(null);
  const cur = profile?.invoiceTemplate ?? "classic";
  async function preview(t: InvoiceTemplate) {
    if (!profile) return;
    setBusy(t);
    try { await Print.printAsync({ html: invoiceHtml(sampleInvoice(profile, currency), t), ...paperFor(t) }); } catch { /* the user closed the preview */ } finally { setBusy(null); }
  }
  return (
    <Screen>
      <ScrollView contentContainerStyle={{ paddingBottom: 40 }}>
        <Header title="Invoice design" onBack={onClose} sub="Used for every PDF you share. Old invoices keep their figures; only the look changes." />
        <View style={{ padding: space.lg, gap: space.lg }}>
          {TEMPLATES.map((t) => {
            const on = cur === t.key;
            return (
              <Card key={t.key} style={{ gap: 12, borderColor: on ? c.accent : c.border, borderWidth: on ? 2 : 1 }}>
                <Mini kind={t.key} />
                <View style={{ gap: 2 }}>
                  <View style={{ flexDirection: "row", justifyContent: "space-between" }}><T v="h3">{t.label}</T>{on ? <T v="small" color={c.accent} style={{ fontFamily: "IBMPlexSans_600SemiBold" }}>Default</T> : null}</View>
                  <T v="small" muted>{t.blurb} · {t.paper}</T>
                </View>
                <View style={{ flexDirection: "row", gap: 10 }}>
                  <Button style={{ flex: 1 }} kind="quiet" title="Preview" icon="eye-outline" busy={busy === t.key} onPress={() => void preview(t.key)} />
                  <Button style={{ flex: 1 }} title={on ? "In use" : "Use this"} disabled={on} onPress={() => { if (profile) void saveProfile({ ...profile, invoiceTemplate: t.key }); }} />
                </View>
              </Card>
            );
          })}
          <T v="small" muted>You can also pick a different design for a single invoice when you share it.</T>
        </View>
      </ScrollView>
    </Screen>
  );
}

/** A tiny drawing of each layout, so the choice is visible before opening a preview. */
function Mini({ kind }: { kind: InvoiceTemplate }) {
  const c = usePalette();
  const bar = (w: string, h = 6, bg = c.borderStrong) => <View style={{ width: w as `${number}%`, height: h, borderRadius: 2, backgroundColor: bg }} />;
  const sheet = { backgroundColor: "#fff", borderRadius: 6, borderWidth: 1, borderColor: c.border, padding: 12, gap: 6 } as const;
  if (kind === "thermal") return <View style={[sheet, { width: 120, alignSelf: "center", alignItems: "center" }]}>{bar("60%", 8, "#222")}{bar("80%")}{bar("100%", 1, "#999")}{bar("90%")}{bar("90%")}{bar("100%", 1, "#999")}{bar("50%", 8, "#222")}</View>;
  if (kind === "modern") return <View style={[sheet, { paddingTop: 0, overflow: "hidden" }]}><View style={{ height: 22, backgroundColor: c.accent, marginHorizontal: -12 }} />{bar("40%", 8, "#222")}{bar("100%", 5, "#e5e7eb")}{bar("100%", 5, "#f3f4f6")}{bar("100%", 5, "#e5e7eb")}<View style={{ alignSelf: "flex-end", width: "40%" }}>{bar("100%", 9, c.accent)}</View></View>;
  return <View style={[sheet, { borderColor: "#222", borderWidth: 2 }]}><View style={{ flexDirection: "row", justifyContent: "space-between" }}>{bar("40%", 9, "#222")}{bar("20%")}</View>{bar("100%", 1, "#222")}{bar("100%", 5, "#d1d5db")}{bar("100%", 5, "#e5e7eb")}{bar("100%", 5, "#d1d5db")}<View style={{ alignSelf: "flex-end", width: "40%" }}>{bar("100%", 8, "#222")}</View></View>;
}
