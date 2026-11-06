import React from "react";
import { ScrollView, View } from "react-native";
import { listSales, type Product } from "../db/repo";
import { useLocal } from "../lib/hooks";
import { formatMoney, num } from "../lib/money";
import { calcTotals } from "../lib/tax";
import { outstandingAmount } from "../lib/receivables";
import { useApp } from "../state/app";
import { radius, space, usePalette } from "../theme";
import { Card, Icon, Label, Screen, Tap, type IconName } from "../ui/kit";
import { T } from "../ui/text";
import { SaleRow } from "./Sales";

type Go = "sell" | "sales" | "items" | "more";

function Stat({ icon, label, value, sub, tone, onPress }: { icon: IconName; label: string; value: string; sub?: string; tone: string; onPress?: () => void }) {
  const c = usePalette();
  return (
    <Tap label={`${label}: ${value}`} onPress={onPress} style={{ flex: 1 }}>
      <Card style={{ gap: 8, minHeight: 112 }}>
        <View style={{ width: 34, height: 34, borderRadius: 10, backgroundColor: c.surfaceAlt, alignItems: "center", justifyContent: "center" }}><Icon name={icon} size={19} color={tone} /></View>
        <T v="label" muted>{label}</T>
        <T v="moneyLg" style={{ fontSize: 19 }}>{value}</T>
        {sub ? <T v="small" muted>{sub}</T> : null}
      </Card>
    </Tap>
  );
}

export function Home({ go, open, addItem, addCustomer }: { go: (t: Go) => void; open: (id: string) => void; addItem: () => void; addCustomer: () => void }) {
  const { businessName, currency, linked } = useApp();
  const c = usePalette();
  const sales = useLocal((db) => listSales(db, 300), []);
  const low = useLocal((db) => db.all<Product>("SELECT * FROM products WHERE archived=0 AND stock IS NOT NULL AND CAST(stock AS REAL) <= 5 ORDER BY CAST(stock AS REAL) LIMIT 5"), [] as Product[]);
  const lowCount = useLocal((db) => db.first<{ n: number }>("SELECT COUNT(*) AS n FROM products WHERE archived=0 AND stock IS NOT NULL AND CAST(stock AS REAL) <= 5").then((r) => r?.n ?? 0), 0);

  const total = (s: (typeof sales)[number]) => (s.total ? num(s.total) : calcTotals(s.payload.lines).total);
  const start = new Date(); start.setHours(0, 0, 0, 0);
  const today = sales.filter((s) => s.created_at >= start.getTime());
  const todayTotal = today.reduce((n, s) => n + total(s), 0);
  const unpaid = sales.filter((s) => outstandingAmount(s) > 0.004);
  const unpaidTotal = unpaid.reduce((n, s) => n + outstandingAmount(s), 0);
  const hour = new Date().getHours();
  const greet = hour < 12 ? "Good morning" : hour < 17 ? "Good afternoon" : "Good evening";

  return (
    <Screen>
      <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: 36 }}>
        <View style={{ gap: 2 }}><T v="small" muted>{greet}</T><T v="title" numberOfLines={1}>{businessName}</T></View>

        <View style={{ backgroundColor: c.accent, borderRadius: radius.lg + 6, padding: space.xl, gap: 6 }}>
          <T v="label" color="rgba(255,255,255,0.78)">Sales today</T>
          <T v="moneyHero" color="#fff">{formatMoney(todayTotal, currency)}</T>
          <T v="small" color="rgba(255,255,255,0.8)">{today.length} sale{today.length === 1 ? "" : "s"}{today.some((s) => !s.total) ? " · estimated until synced" : ""}</T>
          <View style={{ marginTop: 14 }}>
            <Tap label="New sale" onPress={() => go("sell")} style={{ backgroundColor: "#fff", borderRadius: radius.lg, minHeight: 52, flexDirection: "row", alignItems: "center", justifyContent: "center", gap: 8 }}>
              <Icon name="add-circle" size={22} color={c.accent} /><T v="h3" color={c.accent === "#52d4bd" ? "#092622" : c.accent}>New sale</T>
            </Tap>
          </View>
        </View>

        <View style={{ flexDirection: "row", gap: 12 }}>
          <Stat icon="wallet-outline" label="To collect" value={formatMoney(unpaidTotal, currency)} sub={`${unpaid.length} unpaid`} tone={unpaid.length ? c.warning : c.positive} onPress={() => go("sales")} />
          <Stat icon="cube-outline" label="Low stock" value={String(lowCount)} sub={lowCount ? "5 or fewer left" : "All stocked"} tone={lowCount ? c.negative : c.positive} onPress={() => go("items")} />
        </View>

        <View style={{ flexDirection: "row", gap: 12 }}>
          {([["pricetag-outline", "Add item", addItem], ["person-add-outline", "Add customer", addCustomer]] as [IconName, string, () => void][]).map(([ic, t, fn]) => (
            <Tap key={t} label={t} onPress={fn} style={{ flex: 1, flexDirection: "row", alignItems: "center", gap: 10, backgroundColor: c.surface, borderWidth: 1, borderColor: c.border, borderRadius: radius.lg, padding: 14 }}>
              <Icon name={ic} size={22} color={c.accent} /><T v="strong">{t}</T>
            </Tap>
          ))}
        </View>

        {low.length > 0 ? (
          <View style={{ gap: 10 }}>
            <Label>Running low</Label>
            <Card style={{ padding: 0 }}>
              {low.map((p, i) => (
                <View key={p.variant_id} style={{ flexDirection: "row", justifyContent: "space-between", padding: 14, borderTopWidth: i ? 1 : 0, borderTopColor: c.border }}>
                  <T v="strong" numberOfLines={1} style={{ flex: 1 }}>{p.name}</T><T v="money" color={num(p.stock) <= 0 ? c.negative : c.warning}>{p.stock} {p.unit_label}</T>
                </View>
              ))}
            </Card>
          </View>
        ) : null}

        <View style={{ gap: 10 }}>
          <View style={{ flexDirection: "row", justifyContent: "space-between", alignItems: "center" }}>
            <Label>Recent sales</Label>
            {sales.length > 4 ? <Tap label="See all" onPress={() => go("sales")} haptic={false}><T v="small" color={c.accent} style={{ fontFamily: "IBMPlexSans_600SemiBold" }}>See all</T></Tap> : null}
          </View>
          {sales.length === 0 ? (
            <Card style={{ alignItems: "center", gap: 6 }}><Icon name="receipt-outline" size={28} color={c.muted} /><T v="body" muted style={{ textAlign: "center" }}>No sales yet. Tap New sale to make your first invoice.</T></Card>
          ) : (
            <Card style={{ padding: 0 }}>{sales.slice(0, 4).map((s, i, a) => <SaleRow key={s.local_id} sale={s} last={i === a.length - 1} onPress={() => open(s.local_id)} />)}</Card>
          )}
        </View>
        {!linked ? (
          <Card style={{ flexDirection: "row", gap: 12, alignItems: "center", backgroundColor: c.accentSoft, borderColor: c.accentSoft }}>
            <Icon name="shield-checkmark-outline" size={26} color={c.accent} />
            <T v="small" style={{ flex: 1 }}>Your data lives only on this phone. Connect a server from More to back it up and share it with your team.</T>
          </Card>
        ) : null}
      </ScrollView>
    </Screen>
  );
}
