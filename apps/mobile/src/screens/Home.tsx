import React from "react";
import { ScrollView, Text, View } from "react-native";
import { listSales } from "../db/repo";
import { useLocal } from "../lib/hooks";
import { formatMoney, subtotal } from "../lib/money";
import { useApp } from "../state/app";
import { space, tabular, usePalette } from "../theme";
import { Body, Button, Card, H1, Label, Screen } from "../ui/kit";
import { SaleRow } from "./Sales";

export function Home({ go, open }: { go: (t: "sell" | "sales" | "catalogue" | "more") => void; open: (id: string) => void }) {
  const { org } = useApp();
  const c = usePalette();
  const sales = useLocal((db) => listSales(db, 50), []);
  const start = new Date(); start.setHours(0, 0, 0, 0);
  const today = sales.filter((s) => s.created_at >= start.getTime());
  const cur = org?.currency ?? "INR";
  const amount = today.reduce((n, s) => n + (s.total ? Number(s.total) : subtotal(s.payload.lines)), 0);
  const estimated = today.some((s) => !s.total);
  return (
    <Screen>
      <ScrollView contentContainerStyle={{ padding: space.lg, gap: space.lg, paddingBottom: 32 }}>
        <View><H1>{org?.legalName ?? "Rechvix"}</H1><Body muted>Today on this phone</Body></View>
        <Card style={{ gap: 6 }}>
          <Label>Sales today</Label>
          <Text style={[{ fontSize: 34, fontWeight: "700", color: c.text, letterSpacing: -0.5 }, tabular]}>{formatMoney(amount, cur)}</Text>
          <Body small muted>{today.length} sale{today.length === 1 ? "" : "s"}{estimated ? " · some totals are before tax until they sync" : ""}</Body>
        </Card>
        <Button title="New sale" onPress={() => go("sell")} />
        <View style={{ gap: 10 }}>
          <Label>Recent sales</Label>
          {sales.length === 0 ? <Body muted>No sales yet. Tap New sale to make your first.</Body> : (
            <Card style={{ padding: 0 }}>{sales.slice(0, 5).map((s, i, a) => <SaleRow key={s.local_id} sale={s} last={i === a.length - 1} onPress={() => open(s.local_id)} />)}</Card>
          )}
          {sales.length > 5 ? <Button title="See all sales" kind="quiet" onPress={() => go("sales")} /> : null}
        </View>
      </ScrollView>
    </Screen>
  );
}
