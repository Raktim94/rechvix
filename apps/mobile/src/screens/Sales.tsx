import React, { useState } from "react";
import { Alert, FlatList, Pressable, ScrollView, Text, View } from "react-native";
import { discardSale, getSale, listSales, recentServerDocs, retrySale, type Sale, type ServerDoc } from "../db/repo";
import { useLocal } from "../lib/hooks";
import { formatMoney, lineAmount, num, subtotal } from "../lib/money";
import { useApp } from "../state/app";
import { space, tabular, usePalette } from "../theme";
import { Badge, Body, Button, Card, Chip, Divider, Empty, H1, Label, Screen } from "../ui/kit";

export const stateBadge = (s: Sale): { text: string; tone: "good" | "warn" | "bad" | "info" } =>
  s.state === "synced" ? { text: s.doc_number ?? "Synced", tone: "good" } : s.state === "failed" ? { text: "Needs attention", tone: "bad" } : s.state === "syncing" ? { text: "Syncing…", tone: "info" } : { text: "Waiting to sync", tone: "warn" };

export function SaleRow({ sale, last, onPress }: { sale: Sale; last?: boolean; onPress: () => void }) {
  const { org } = useApp();
  const c = usePalette();
  const b = stateBadge(sale);
  const amt = sale.total ? num(sale.total) : subtotal(sale.payload.lines);
  return (
    <View>
      <Pressable accessibilityRole="button" onPress={onPress} style={({ pressed }) => ({ minHeight: 68, padding: space.lg, gap: 4, opacity: pressed ? 0.6 : 1 })}>
        <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
          <Text style={{ flex: 1, fontSize: 16, fontWeight: "600", color: c.text }} numberOfLines={1}>{sale.payload.customerName}</Text>
          <Text style={[{ fontSize: 16, fontWeight: "700", color: c.text }, tabular]}>{formatMoney(amt, org?.currency)}{sale.total ? "" : "*"}</Text>
        </View>
        <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
          <Badge text={b.text} tone={b.tone} />
          <Text style={{ fontSize: 12, color: c.muted }}>{new Date(sale.created_at).toLocaleString(undefined, { day: "numeric", month: "short", hour: "numeric", minute: "2-digit" })}</Text>
        </View>
      </Pressable>
      {last ? null : <Divider />}
    </View>
  );
}

type Filter = "all" | "waiting" | "attention";

export function Sales({ open }: { open: (id: string) => void }) {
  const { org, counts } = useApp();
  const c = usePalette();
  const [f, setF] = useState<Filter>("all");
  const local = useLocal((db) => listSales(db), []);
  const server = useLocal((db) => recentServerDocs(db), [] as ServerDoc[]);
  const mine = new Set(local.map((s) => s.progress.docId).filter(Boolean));
  const shown = local.filter((s) => (f === "all" ? true : f === "waiting" ? s.state === "pending" || s.state === "syncing" : s.state === "failed"));
  const others = f === "all" ? server.filter((d) => !mine.has(d.id)) : [];
  const attention = counts.failed;
  return (
    <Screen>
      <View style={{ padding: space.lg, gap: 12 }}>
        <H1>Sales</H1>
        <View style={{ flexDirection: "row", gap: 8 }}>
          <Chip title="All" active={f === "all"} onPress={() => setF("all")} />
          <Chip title={`Waiting${counts.pending + counts.syncing ? ` (${counts.pending + counts.syncing})` : ""}`} active={f === "waiting"} onPress={() => setF("waiting")} />
          <Chip title={`Needs attention${attention ? ` (${attention})` : ""}`} active={f === "attention"} onPress={() => setF("attention")} />
        </View>
      </View>
      <FlatList
        data={[...shown.map((s) => ({ kind: "local" as const, s })), ...others.map((d) => ({ kind: "server" as const, d }))]}
        keyExtractor={(x) => (x.kind === "local" ? x.s.local_id : x.d.id)}
        contentContainerStyle={{ paddingHorizontal: space.lg, paddingBottom: 32 }}
        ListEmptyComponent={<Empty title={f === "all" ? "No sales yet" : "Nothing here"} hint={f === "all" ? "Sales you make appear here, even before they reach the server." : undefined} />}
        ListFooterComponent={shown.some((s) => !s.total) ? <Body small muted style={{ paddingTop: 12 }}>* Before tax. The server adds tax when the sale syncs.</Body> : undefined}
        renderItem={({ item }) =>
          item.kind === "local" ? (
            <Card style={{ padding: 0, marginBottom: 10 }}><SaleRow sale={item.s} last onPress={() => open(item.s.local_id)} /></Card>
          ) : (
            <Card style={{ marginBottom: 10, gap: 4 }}>
              <View style={{ flexDirection: "row", justifyContent: "space-between" }}>
                <Text style={{ fontSize: 16, fontWeight: "600", color: c.text }}>{item.d.number}</Text>
                <Text style={[{ fontSize: 16, fontWeight: "700", color: c.text }, tabular]}>{item.d.total ? formatMoney(num(item.d.total), org?.currency) : "—"}</Text>
              </View>
              <Text style={{ fontSize: 12, color: c.muted }}>From the server · {item.d.issue_date.slice(0, 10)} · {item.d.status.toLowerCase()}</Text>
            </Card>
          )
        }
      />
    </Screen>
  );
}

export function SaleDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const { db, org, syncNow, syncing, online } = useApp();
  const c = usePalette();
  const sale = useLocal((d) => getSale(d, id), null as Sale | null, [id]);
  if (!sale) return <Screen><Empty title="Sale not found" /><View style={{ padding: space.lg }}><Button title="Back" kind="quiet" onPress={onClose} /></View></Screen>;
  const b = stateBadge(sale);
  const cur = org?.currency ?? "INR";
  const est = subtotal(sale.payload.lines);
  return (
    <Screen>
      <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: 40 }}>
        <Pressable accessibilityRole="button" onPress={onClose} hitSlop={10}><Text style={{ color: c.accent, fontWeight: "700", fontSize: 16 }}>‹ Sales</Text></Pressable>
        <View style={{ gap: 8 }}><H1>{sale.payload.customerName}</H1><Badge text={b.text} tone={b.tone} /></View>

        {sale.state === "failed" ? (
          <Card style={{ backgroundColor: c.negativeSoft, borderColor: c.negative, gap: 6 }}>
            <Text style={{ fontWeight: "700", color: c.negative }}>The server could not accept this sale</Text>
            <Body>{sale.error ?? "Unknown error."}</Body>
            <Body small muted>Nothing was lost: the sale is still on this phone. Fix the cause (for example stock), then retry.</Body>
          </Card>
        ) : null}
        {sale.state === "pending" && sale.error ? <Body small style={{ color: c.warning }}>Last attempt: {sale.error}</Body> : null}

        <Card style={{ padding: 0 }}>
          {sale.payload.lines.map((l, i) => (
            <View key={l.variantId}>
              <View style={{ padding: space.lg, flexDirection: "row", justifyContent: "space-between", gap: 12 }}>
                <View style={{ flex: 1 }}><Text style={{ fontSize: 16, fontWeight: "600", color: c.text }}>{l.name}</Text><Text style={[{ fontSize: 13, color: c.muted }, tabular]}>{l.qty} × {formatMoney(num(l.price), cur)}</Text></View>
                <Text style={[{ fontSize: 16, fontWeight: "700", color: c.text }, tabular]}>{formatMoney(lineAmount(l), cur)}</Text>
              </View>
              {i < sale.payload.lines.length - 1 ? <Divider /> : null}
            </View>
          ))}
        </Card>

        <Card style={{ gap: 8 }}>
          <Row k="Subtotal (before tax)" v={formatMoney(est, cur)} />
          {sale.total ? <Row k="Total with tax" v={formatMoney(num(sale.total), cur)} strong /> : <Body small muted>Tax and the final total are added by the server when this syncs.</Body>}
          <Row k="Payment" v={sale.payload.paymentMethod ? sale.payload.paymentMethod.toLowerCase() : "pay later"} />
          {sale.doc_number ? <Row k="Invoice" v={sale.doc_number} /> : null}
          <Row k="Made" v={new Date(sale.created_at).toLocaleString()} />
        </Card>

        {sale.state === "failed" ? (
          <View style={{ gap: 10 }}>
            <Button title="Retry" busy={syncing} onPress={async () => { await retrySale(db!, sale.local_id); void syncNow(); }} />
            <Button title="Discard this sale" kind="danger" onPress={() => Alert.alert("Discard this sale?", "It will be removed from this phone and will not be sent to the server.", [{ text: "Keep", style: "cancel" }, { text: "Discard", style: "destructive", onPress: async () => { await discardSale(db!, sale.local_id); onClose(); } }])} />
          </View>
        ) : sale.state === "pending" ? (
          <Button title={online ? "Sync now" : "Waiting for a connection"} kind="quiet" disabled={!online} busy={syncing} onPress={() => void syncNow()} />
        ) : null}
        <Label>Reference</Label><Body small muted>{`mobile:${sale.local_id}`}</Body>
      </ScrollView>
    </Screen>
  );
}

function Row({ k, v, strong }: { k: string; v: string; strong?: boolean }) {
  const c = usePalette();
  return <View style={{ flexDirection: "row", justifyContent: "space-between", gap: 12 }}><Text style={{ color: c.muted, fontSize: 14 }}>{k}</Text><Text style={[{ color: c.text, fontSize: strong ? 18 : 14, fontWeight: strong ? "700" : "500", flexShrink: 1, textAlign: "right" }, tabular]}>{v}</Text></View>;
}
