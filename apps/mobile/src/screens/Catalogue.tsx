import React, { useState } from "react";
import { FlatList, Text, TextInput, View } from "react-native";
import { searchProducts, type Product } from "../db/repo";
import { useLocal } from "../lib/hooks";
import { formatMoney, num } from "../lib/money";
import { useApp } from "../state/app";
import { radius, space, tabular, usePalette } from "../theme";
import { Card, Divider, Empty, H1, Screen } from "../ui/kit";

export function Catalogue() {
  const { org } = useApp();
  const c = usePalette();
  const [q, setQ] = useState("");
  const list = useLocal((db) => searchProducts(db, q, 300), [] as Product[], [q]);
  return (
    <Screen>
      <View style={{ padding: space.lg, gap: 12 }}>
        <H1>Catalogue</H1>
        <TextInput accessibilityLabel="Search catalogue" value={q} onChangeText={setQ} placeholder="Search item, SKU or HSN" placeholderTextColor={c.faint} autoCorrect={false}
          style={{ minHeight: 52, borderRadius: radius.md, borderWidth: 1, borderColor: c.borderStrong, backgroundColor: c.surface, color: c.text, paddingHorizontal: 14, fontSize: 17 }} />
      </View>
      <FlatList data={list} keyExtractor={(p) => p.variant_id} contentContainerStyle={{ paddingHorizontal: space.lg, paddingBottom: 32 }}
        ListEmptyComponent={<Empty title={q ? "No matching item" : "No items yet"} hint="Items are added on the web and sync to this phone." />}
        renderItem={({ item }) => (
          <View style={{ backgroundColor: c.surface }}>
            <View style={{ minHeight: 64, padding: space.lg, flexDirection: "row", alignItems: "center", gap: 12 }}>
              <View style={{ flex: 1 }}>
                <Text style={{ fontSize: 16, fontWeight: "600", color: c.text }} numberOfLines={1}>{item.name}</Text>
                <Text style={{ fontSize: 13, color: c.muted }}>{item.sku}{item.hsn ? ` · HSN ${item.hsn}` : ""}</Text>
              </View>
              <View style={{ alignItems: "flex-end" }}>
                <Text style={[{ fontSize: 16, fontWeight: "700", color: c.text }, tabular]}>{item.price != null ? formatMoney(num(item.price), org?.currency) : "—"}</Text>
                <Text style={[{ fontSize: 12, color: item.stock != null && num(item.stock) <= 0 ? c.negative : c.muted }, tabular]}>{item.stock != null ? `${item.stock} in stock` : "stock unknown"}</Text>
              </View>
            </View>
            <Divider />
          </View>
        )} />
    </Screen>
  );
}
