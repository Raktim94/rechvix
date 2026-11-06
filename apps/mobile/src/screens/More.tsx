import React, { useState } from "react";
import { Alert, ScrollView, View } from "react-native";
import { exportBackup } from "../lib/share";
import { ago } from "../lib/hooks";
import { type Profile } from "../db/repo";
import { GST_RATES } from "../lib/tax";
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

export function More({ openCustomers, openProfile }: { openCustomers: () => void; openProfile: () => void }) {
  const { linked, server, email, lastSyncAt, counts, syncNow, syncing, online, logout, disconnect, startLinking, db, businessName } = useApp();
  const c = usePalette();
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
            <Row icon="download-outline" title="Export a backup" sub="One file with everything on this phone" onPress={() => db && void exportBackup(db)} />
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
  const c = usePalette();
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
