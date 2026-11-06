import React, { useState } from "react";
import { ScrollView, View } from "react-native";
import { allPayments, listSales, serverBalances, type Payment, type Product } from "../db/repo";
import { parseMinor } from "../lib/decimal";
import { ago, useLocal, useNow } from "../lib/hooks";
import { formatMinor } from "../lib/money";
import { dashboardStats, type Range } from "../lib/stats";
import { groupPaymentsBySale } from "../db/repo";
import { useApp } from "../state/app";
import { fonts, radius, space, usePalette } from "../theme";
import { Card, Chip, Icon, Label, Screen, Tap, type IconName } from "../ui/kit";
import { T } from "../ui/text";
import { SaleRow, useSaleRows } from "./Sales";

type Go = "sell" | "sales" | "items" | "dues" | "more";
const RANGES: { k: Range; t: string; long: string }[] = [{ k: "today", t: "Today", long: "today" }, { k: "7d", t: "7 days", long: "in the last 7 days" }, { k: "30d", t: "30 days", long: "in the last 30 days" }];

function Stat({ icon, label, value, sub, tone, onPress }: { icon: IconName; label: string; value: string; sub?: string; tone: string; onPress?: () => void }) {
  const c = usePalette();
  return (
    <Tap label={`${label}: ${value}`} onPress={onPress} style={{ flex: 1 }}>
      <Card style={{ gap: 8, minHeight: 118 }}>
        <View style={{ width: 34, height: 34, borderRadius: 10, backgroundColor: c.surfaceAlt, alignItems: "center", justifyContent: "center" }}><Icon name={icon} size={19} color={tone} /></View>
        <T v="label" muted>{label}</T>
        <T v="moneyLg" style={{ fontSize: 19 }} adjustsFontSizeToFit numberOfLines={1}>{value}</T>
        {sub ? <T v="small" muted>{sub}</T> : null}
      </Card>
    </Tap>
  );
}

/** Sales over the range as bars. Each bar is labelled for screen readers; the heights are relative to the best day. */
function Trend({ points, currency }: { points: { dayStart: number; salesMinor: bigint }[]; currency: string }) {
  const c = usePalette();
  const max = points.reduce((n, p) => (p.salesMinor > n ? p.salesMinor : n), 0n);
  const todayStart = new Date().setHours(0, 0, 0, 0);
  if (points.length < 2) return null;
  return (
    <View accessible accessibilityLabel={`Sales per day. Best day ${formatMinor(max, currency)}.`} style={{ flexDirection: "row", alignItems: "flex-end", gap: points.length > 14 ? 2 : 6, height: 84 }}>
      {points.map((p) => {
        const h = max === 0n ? 3 : Math.max(3, Math.round(Number((p.salesMinor * 1000n) / max) / 1000 * 80));
        return <View key={p.dayStart} style={{ flex: 1, height: h, borderRadius: 4, backgroundColor: p.dayStart === todayStart ? c.accent : c.accentSoft, borderWidth: p.dayStart === todayStart ? 0 : 1, borderColor: c.border }} />;
      })}
    </View>
  );
}

export function Home({ go, open, addItem, addCustomer }: { go: (t: Go) => void; open: (id: string) => void; addItem: () => void; addCustomer: () => void }) {
  const { businessName, currency, linked, counts, lastSyncAt, syncNow, online } = useApp();
  const c = usePalette();
  const [range, setRange] = useState<Range>("today");
  const now = useNow();
  const rows = useSaleRows();
  const sales = useLocal((db) => listSales(db, 5000), []);
  const pays = useLocal((db) => allPayments(db), [] as Payment[]);
  const server = useLocal((db) => serverBalances(db), []);
  const low = useLocal((db) => db.all<Product>("SELECT * FROM products WHERE archived=0 AND is_custom=0 AND stock IS NOT NULL AND CAST(stock AS REAL) <= 5 ORDER BY CAST(stock AS REAL) LIMIT 5"), [] as Product[]);
  const lowCount = useLocal((db) => db.first<{ n: number }>("SELECT COUNT(*) AS n FROM products WHERE archived=0 AND is_custom=0 AND stock IS NOT NULL AND CAST(stock AS REAL) <= 5").then((r) => r?.n ?? 0), 0);

  const loose = pays.filter((p) => !p.sale_local_id && !p.server_doc_id);
  const st = dashboardStats(sales, groupPaymentsBySale(pays), loose, range, now);
  const m = (x: bigint) => formatMinor(x, currency);
  const r = RANGES.find((x) => x.k === range)!;
  const hour = new Date().getHours();
  const greet = hour < 12 ? "Good morning" : hour < 17 ? "Good afternoon" : "Good evening";
  const serverDue = server.reduce((n, b) => n + parseMinor(b.total), 0n);
  const waiting = counts.pending + counts.syncing;

  return (
    <Screen>
      <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: 36 }}>
        <View style={{ gap: 2 }}><T v="small" muted>{greet}</T><T v="title" numberOfLines={1}>{businessName}</T></View>

        <View style={{ flexDirection: "row", gap: 8 }}>{RANGES.map((x) => <Chip key={x.k} title={x.t} active={range === x.k} onPress={() => setRange(x.k)} />)}</View>

        <View style={{ backgroundColor: c.accent, borderRadius: radius.lg + 6, padding: space.xl, gap: 6 }}>
          <T v="label" color="rgba(255,255,255,0.78)">Billed {r.long}</T>
          <T v="moneyHero" color="#fff" adjustsFontSizeToFit numberOfLines={1} accessibilityLabel={`Billed ${r.long} ${m(st.grossSalesMinor)}`}>{m(st.grossSalesMinor)}</T>
          <T v="small" color="rgba(255,255,255,0.85)">{st.invoiceCount} invoice{st.invoiceCount === 1 ? "" : "s"}{st.invoiceCount ? ` · average ${m(st.averageBillMinor)}` : ""}</T>
          <View style={{ marginTop: 14 }}>
            <Tap label="New sale" onPress={() => go("sell")} style={{ backgroundColor: "#fff", borderRadius: radius.lg, minHeight: 52, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 8 }}>
              <Icon name="add-circle" size={22} color="#0f6e5c" /><T v="h3" color="#0f6e5c">New sale</T>
            </Tap>
          </View>
        </View>

        <View style={{ flexDirection: "row", gap: 12 }}>
          <Stat icon="arrow-down-circle-outline" label="Collected" value={m(st.collectedMinor)} sub={`Money received ${r.long}`} tone={c.positive} onPress={() => go("dues")} />
          <Stat icon="wallet-outline" label="To collect" value={m(st.dueMinor)} sub={st.overdueMinor > 0n ? `${m(st.overdueMinor)} overdue` : st.dueMinor > 0n ? "Nothing overdue" : "All paid up"} tone={st.overdueMinor > 0n ? c.negative : st.dueMinor > 0n ? c.warning : c.positive} onPress={() => go("dues")} />
        </View>
        {linked && serverDue > st.dueMinor ? <T v="small" muted>Across the whole business the server shows {m(serverDue)} outstanding, including invoices made on other devices.</T> : null}

        {st.trend.length > 1 ? (
          <Card style={{ gap: 10 }}>
            <View style={{ flexDirection: "row", justifyContent: "space-between" }}><T v="strong">Sales per day</T><T v="small" muted>{r.t}</T></View>
            <Trend points={st.trend} currency={currency} />
          </Card>
        ) : null}

        {st.top.length > 0 ? (
          <View style={{ gap: 8 }}>
            <Label>Top products {r.long}</Label>
            <Card style={{ padding: 0 }}>
              {st.top.map((t, i) => (
                <View key={t.name} style={{ flexDirection: "row", alignItems: "center", gap: 12, padding: 14, borderTopWidth: i ? 1 : 0, borderTopColor: c.border }}>
                  <View style={{ width: 26, height: 26, borderRadius: 13, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center" }}><T v="small" color={c.accent} style={{ fontFamily: fonts.bodySemi }}>{i + 1}</T></View>
                  <View style={{ flex: 1 }}><T v="strong" numberOfLines={1}>{t.name}</T><T v="small" muted>{t.qty} sold</T></View>
                  <T v="money">{m(t.amountMinor)}</T>
                </View>
              ))}
            </Card>
          </View>
        ) : null}

        <Stat icon="cube-outline" label="Low stock" value={String(lowCount)} sub={lowCount ? "5 or fewer left" : "All stocked"} tone={lowCount ? c.negative : c.positive} onPress={() => go("items")} />
        {low.length > 0 ? (
          <Card style={{ padding: 0 }}>
            {low.map((p, i) => (
              <View key={p.variant_id} style={{ flexDirection: "row", justifyContent: "space-between", padding: 14, borderTopWidth: i ? 1 : 0, borderTopColor: c.border }}>
                <T v="strong" numberOfLines={1} style={{ flex: 1 }}>{p.name}</T><T v="money" color={Number(p.stock) <= 0 ? c.negative : c.warning}>{p.stock} {p.unit_label}</T>
              </View>
            ))}
          </Card>
        ) : null}

        <View style={{ flexDirection: "row", gap: 12 }}>
          {([["pricetag-outline", "Add item", addItem], ["person-add-outline", "Add customer", addCustomer]] as [IconName, string, () => void][]).map(([ic, t, fn]) => (
            <Tap key={t} label={t} onPress={fn} style={{ flex: 1, flexDirection: "row", alignItems: "center", gap: 10, backgroundColor: c.surface, borderWidth: 1, borderColor: c.border, borderRadius: radius.lg, padding: 14 }}>
              <Icon name={ic} size={22} color={c.accent} /><T v="strong">{t}</T>
            </Tap>
          ))}
        </View>

        <View style={{ gap: 10 }}>
          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
            <Label>Recent invoices</Label>
            {rows.length > 4 ? <Tap label="See all" onPress={() => go("sales")} haptic={false}><T v="small" color={c.accent} style={{ fontFamily: fonts.bodySemi }}>See all</T></Tap> : null}
          </View>
          {rows.length === 0 ? (
            <Card style={{ alignItems: "center", gap: 6 }}><Icon name="receipt-outline" size={28} color={c.muted} /><T v="body" muted style={{ textAlign: "center" }}>No invoices yet. Tap New sale to make your first one.</T></Card>
          ) : (
            <Card style={{ padding: 0 }}>{rows.slice(0, 4).map((x, i, a) => <SaleRow key={x.sale.local_id} row={x} last={i === a.length - 1} onPress={() => open(x.sale.local_id)} />)}</Card>
          )}
        </View>

        <Card style={{ flexDirection: "row", gap: 12, alignItems: "center", backgroundColor: counts.failed ? c.negativeSoft : c.surface }}>
          <Icon name={!linked ? "phone-portrait-outline" : counts.failed ? "alert-circle" : waiting ? "time-outline" : "checkmark-circle"} size={24} color={!linked ? c.muted : counts.failed ? c.negative : waiting ? c.warning : c.positive} />
          <View style={{ flex: 1 }}>
            <T v="strong">{!linked ? "Saved on this phone" : counts.failed ? `${counts.failed} need attention` : waiting ? `${waiting} waiting to sync` : "Everything is synced"}</T>
            <T v="small" muted>{!linked ? "Connect a server from More to back up and share with your team." : online ? `Last synced ${ago(lastSyncAt)}` : "Offline. You can keep selling."}</T>
          </View>
          {linked && online ? <Tap label="Sync now" onPress={() => void syncNow()} style={{ minHeight: 44, paddingHorizontal: 12, justifyContent: "center" }}><T v="small" color={c.accent} style={{ fontFamily: fonts.bodySemi }}>Sync</T></Tap> : null}
        </Card>
        {st.unsynced > 0 && linked ? <T v="small" muted>{st.unsynced} invoice{st.unsynced === 1 ? "" : "s"} on this phone aren&apos;t on the server yet, so the web dashboard won&apos;t include them until they sync.</T> : null}
      </ScrollView>
    </Screen>
  );
}
