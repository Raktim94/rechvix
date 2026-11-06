import { StatusBar } from "expo-status-bar";
import React, { useEffect, useState } from "react";
import { ActivityIndicator, BackHandler, Pressable, Text, useColorScheme, View } from "react-native";
import { SafeAreaProvider, useSafeAreaInsets } from "react-native-safe-area-context";
import { Catalogue } from "./src/screens/Catalogue";
import { Connect } from "./src/screens/Connect";
import { Home } from "./src/screens/Home";
import { Login } from "./src/screens/Login";
import { Customers, More } from "./src/screens/More";
import { Sales, SaleDetail } from "./src/screens/Sales";
import { Sell } from "./src/screens/Sell";
import { AppProvider, useApp } from "./src/state/app";
import { usePalette } from "./src/theme";
import { TopInsetHandled } from "./src/ui/kit";
import { SyncBanner } from "./src/ui/SyncBanner";

type Tab = "home" | "sell" | "sales" | "catalogue" | "more";
const TABS: { key: Tab; label: string; glyph: string }[] = [
  { key: "home", label: "Home", glyph: "◧" }, { key: "sell", label: "Sell", glyph: "＋" }, { key: "sales", label: "Sales", glyph: "▤" }, { key: "catalogue", label: "Items", glyph: "▣" }, { key: "more", label: "More", glyph: "⋯" },
];

function Shell() {
  const { phase, sessionExpired } = useApp();
  const c = usePalette();
  const insets = useSafeAreaInsets();
  const [tab, setTab] = useState<Tab>("home");
  const [sale, setSale] = useState<string | null>(null);
  const [customers, setCustomers] = useState(false);

  useEffect(() => {
    const sub = BackHandler.addEventListener("hardwareBackPress", () => {
      if (sale) { setSale(null); return true; }
      if (customers) { setCustomers(false); return true; }
      if (tab !== "home") { setTab("home"); return true; }
      return false;
    });
    return () => sub.remove();
  }, [sale, customers, tab]);

  if (phase === "boot") return <View style={{ flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: c.bg }}><ActivityIndicator color={c.accent} /></View>;
  if (phase === "connect") return <Connect />;
  if (phase === "login" || sessionExpired) return <Login />;

  const body = sale ? <SaleDetail id={sale} onClose={() => setSale(null)} />
    : tab === "home" ? <Home go={setTab} open={setSale} />
    : tab === "sell" ? <Sell onSaved={(id) => { setTab("sales"); setSale(id); }} />
    : tab === "sales" ? <Sales open={setSale} />
    : tab === "catalogue" ? <Catalogue />
    : customers ? <Customers onBack={() => setCustomers(false)} /> : <More openCustomers={() => setCustomers(true)} />;

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <View style={{ paddingTop: insets.top, backgroundColor: c.surface }}><SyncBanner /></View>
      <TopInsetHandled value><View style={{ flex: 1 }}>{body}</View></TopInsetHandled>
      <View style={{ flexDirection: "row", backgroundColor: c.bar, paddingBottom: insets.bottom }} accessibilityRole="tablist">
        {TABS.map((t) => {
          const on = tab === t.key && !sale;
          return (
            <Pressable key={t.key} accessibilityRole="tab" accessibilityState={{ selected: on }} accessibilityLabel={t.label} onPress={() => { setSale(null); setCustomers(false); setTab(t.key); }}
              style={{ flex: 1, minHeight: 60, alignItems: "center", justifyContent: "center", gap: 2, borderTopWidth: 3, borderTopColor: on ? c.accent : "transparent" }}>
              <Text style={{ fontSize: t.key === "sell" ? 24 : 20, color: on ? c.barActive : c.barText }}>{t.glyph}</Text>
              <Text style={{ fontSize: 11, fontWeight: "600", color: on ? c.barActive : c.barText }}>{t.label}</Text>
            </Pressable>
          );
        })}
      </View>
    </View>
  );
}

export default function App() {
  const scheme = useColorScheme();
  return (
    <SafeAreaProvider>
      <AppProvider><StatusBar style={scheme === "dark" ? "light" : "dark"} /><Shell /></AppProvider>
    </SafeAreaProvider>
  );
}
