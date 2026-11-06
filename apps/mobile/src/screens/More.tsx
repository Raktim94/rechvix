import React, { useState } from "react";
import { Alert, FlatList, Linking, Pressable, ScrollView, Text, TextInput, View } from "react-native";
import { searchParties, type Party } from "../db/repo";
import { ago, useLocal } from "../lib/hooks";
import { useApp } from "../state/app";
import { radius, space, usePalette } from "../theme";
import { Body, Button, Card, Divider, Empty, H1, Label, Screen } from "../ui/kit";
import { version } from "../../package.json";

export function Customers({ onBack }: { onBack: () => void }) {
  const c = usePalette();
  const [q, setQ] = useState("");
  const list = useLocal((db) => searchParties(db, q, 200), [] as Party[], [q]);
  return (
    <Screen>
      <View style={{ padding: space.lg, gap: 12 }}>
        <Pressable accessibilityRole="button" onPress={onBack} hitSlop={10}><Text style={{ color: c.accent, fontWeight: "700", fontSize: 16 }}>‹ More</Text></Pressable>
        <H1>Customers</H1>
        <TextInput accessibilityLabel="Search customers" value={q} onChangeText={setQ} placeholder="Name or phone" placeholderTextColor={c.faint}
          style={{ minHeight: 52, borderRadius: radius.md, borderWidth: 1, borderColor: c.borderStrong, backgroundColor: c.surface, color: c.text, paddingHorizontal: 14, fontSize: 17 }} />
      </View>
      <FlatList data={list} keyExtractor={(p) => p.id} contentContainerStyle={{ paddingHorizontal: space.lg, paddingBottom: 32 }} ItemSeparatorComponent={Divider}
        ListEmptyComponent={<Empty title="No customers found" hint="Customers are added on the web and sync to this phone." />}
        renderItem={({ item }) => (
          <View style={{ minHeight: 64, backgroundColor: c.surface, padding: space.lg, flexDirection: "row", alignItems: "center", gap: 12 }}>
            <View style={{ flex: 1 }}><Text style={{ fontSize: 16, fontWeight: "600", color: c.text }}>{item.name}</Text><Text style={{ fontSize: 13, color: c.muted }}>{item.is_walk_in ? "Cash sale, no customer details" : item.phone || "No phone"}</Text></View>
            {item.phone ? <Button title="Call" kind="quiet" style={{ minHeight: 44 }} onPress={() => void Linking.openURL(`tel:${item.phone}`)} /> : null}
          </View>
        )} />
    </Screen>
  );
}

export function More({ openCustomers }: { openCustomers: () => void }) {
  const { org, server, email, lastSyncAt, counts, syncNow, syncing, online, logout, forgetServer } = useApp();
  const c = usePalette();
  async function guard(fn: () => Promise<string | null>) { const m = await fn(); if (m) Alert.alert("Not yet", m); }
  return (
    <Screen>
      <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: 40 }}>
        <H1>More</H1>
        <Card style={{ padding: 0 }}>
          <Pressable accessibilityRole="button" onPress={openCustomers} style={({ pressed }) => ({ minHeight: 60, padding: space.lg, justifyContent: "center", opacity: pressed ? 0.6 : 1 })}>
            <Text style={{ fontSize: 16, fontWeight: "600", color: c.text }}>Customers</Text>
          </Pressable>
        </Card>

        <Label>Sync</Label>
        <Card style={{ gap: 8 }}>
          <KV k="Last synced" v={ago(lastSyncAt)} />
          <KV k="Waiting to sync" v={String(counts.pending + counts.syncing)} />
          <KV k="Need attention" v={String(counts.failed)} />
          <KV k="Connection" v={online ? "Online" : "Offline"} />
          <Button title="Sync now" kind="quiet" busy={syncing} disabled={!online} onPress={() => void syncNow()} />
          <Body small muted>Sales are saved on this phone first, then sent to your server automatically. Items and customers refresh at the same time.</Body>
        </Card>

        <Label>Account</Label>
        <Card style={{ gap: 8 }}>
          <KV k="Business" v={org?.legalName ?? "—"} />
          <KV k="Signed in as" v={email ?? "—"} />
          <KV k="Server" v={(server ?? "—").replace(/^https?:\/\//, "")} />
          <Button title="Sign out" kind="quiet" onPress={() => void guard(logout)} />
          <Button title="Change server" kind="danger" onPress={() => Alert.alert("Change server?", "This clears the items and customers saved on this phone. You'll download them again from the new server.", [{ text: "Cancel", style: "cancel" }, { text: "Change", style: "destructive", onPress: () => void guard(forgetServer) }])} />
        </Card>
        <Body small muted style={{ textAlign: "center" }}>Rechvix {version}</Body>
      </ScrollView>
    </Screen>
  );
}

function KV({ k, v }: { k: string; v: string }) {
  const c = usePalette();
  return <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}><Text style={{ color: c.muted, fontSize: 14 }}>{k}</Text><Text style={{ color: c.text, fontSize: 14, fontWeight: "600", flexShrink: 1, textAlign: "right" }} numberOfLines={1}>{v}</Text></View>;
}
