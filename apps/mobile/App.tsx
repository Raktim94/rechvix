import { Ionicons } from "@expo/vector-icons";
import { IBMPlexMono_500Medium, IBMPlexMono_600SemiBold } from "@expo-google-fonts/ibm-plex-mono";
import { IBMPlexSans_400Regular, IBMPlexSans_500Medium, IBMPlexSans_600SemiBold } from "@expo-google-fonts/ibm-plex-sans";
import { SpaceGrotesk_500Medium, SpaceGrotesk_700Bold } from "@expo-google-fonts/space-grotesk";
import { useFonts } from "expo-font";
import { StatusBar } from "expo-status-bar";
import React, { useEffect, useState } from "react";
import { ActivityIndicator, BackHandler, Image, View, useColorScheme } from "react-native";
import { SafeAreaProvider, useSafeAreaInsets } from "react-native-safe-area-context";
import { Connect } from "./src/screens/Connect";
import { CustomerEditor, Customers } from "./src/screens/Customers";
import { Dues, CustomerLedgerScreen } from "./src/screens/Dues";
import { Home } from "./src/screens/Home";
import { ItemEditor, Items } from "./src/screens/Items";
import { Login } from "./src/screens/Login";
import { InvoiceDesign, More, ProfileEditor } from "./src/screens/More";
import { SaleDetail, Sales } from "./src/screens/Sales";
import { Sell } from "./src/screens/Sell";
import { QuoteDetail, Quotes } from "./src/screens/Quotes";
import { Welcome } from "./src/screens/Welcome";
import { AppProvider, useApp } from "./src/state/app";
import { usePalette } from "./src/theme";
import { Icon, Tap, TopInsetHandled, type IconName } from "./src/ui/kit";
import { SyncBanner } from "./src/ui/SyncBanner";
import { T } from "./src/ui/text";

type Tab = "home" | "sales" | "sell" | "dues" | "more";
const TABS: { key: Tab; label: string; on: IconName; off: IconName }[] = [
  { key: "home", label: "Home", on: "home", off: "home-outline" },
  { key: "sales", label: "Invoices", on: "receipt", off: "receipt-outline" },
  { key: "sell", label: "Sell", on: "add", off: "add" },
  { key: "dues", label: "To collect", on: "wallet", off: "wallet-outline" },
  { key: "more", label: "More", on: "menu", off: "menu-outline" },
];

function Splash() {
  const c = usePalette();
  return (
    <View style={{ flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: c.bg, gap: 18 }}>
      <Image source={require("./assets/icon.png")} style={{ width: 84, height: 84, borderRadius: 22 }} accessibilityIgnoresInvertColors />
      <ActivityIndicator color={c.accent} />
    </View>
  );
}

function Shell() {
  const { phase, linking, sessionExpired, linked } = useApp();
  const c = usePalette();
  const insets = useSafeAreaInsets();
  const [tab, setTab] = useState<Tab>("home");
  const [sale, setSale] = useState<string | null>(null);
  const [customers, setCustomers] = useState(false);
  const [itemsOpen, setItemsOpen] = useState(false);
  const [designOpen, setDesignOpen] = useState(false);
  const [quotesOpen, setQuotesOpen] = useState(false);
  const [quote, setQuote] = useState<string | null>(null);
  const [ledger, setLedger] = useState<string | null>(null);
  const [profileOpen, setProfileOpen] = useState(false);
  const [itemEditor, setItemEditor] = useState<string | null | undefined>(undefined);
  const [custEditor, setCustEditor] = useState<string | null | undefined>(undefined);

  useEffect(() => {
    const sub = BackHandler.addEventListener("hardwareBackPress", () => {
      if (itemEditor !== undefined) { setItemEditor(undefined); return true; }
      if (custEditor !== undefined) { setCustEditor(undefined); return true; }
      if (profileOpen) { setProfileOpen(false); return true; }
      if (quote) { setQuote(null); return true; }
      if (quotesOpen) { setQuotesOpen(false); return true; }
      if (designOpen) { setDesignOpen(false); return true; }
      if (itemsOpen) { setItemsOpen(false); return true; }
      if (sale) { setSale(null); return true; }
      if (ledger) { setLedger(null); return true; }
      if (customers) { setCustomers(false); return true; }
      if (tab !== "home") { setTab("home"); return true; }
      return false;
    });
    return () => sub.remove();
  }, [sale, customers, tab, profileOpen, itemEditor, custEditor, designOpen, itemsOpen, ledger, quotesOpen, quote]);

  if (phase === "boot") return <Splash />;
  if (phase === "welcome") return <Welcome />;
  if (linking === "connect") return <Connect />;
  if (linking === "login" || (linked && sessionExpired)) return <Login />;

  const go = (t: Tab | "items") => { setQuote(null); setQuotesOpen(false); setSale(null); setCustomers(false); setProfileOpen(false); setDesignOpen(false); setLedger(null); if (t === "items") { setItemsOpen(true); return; } setItemsOpen(false); setTab(t); };
  const openLedger = (id: string) => { setSale(null); setLedger(id); };

  const body = quote ? <QuoteDetail id={quote} onClose={() => setQuote(null)} goSell={() => { setQuote(null); setQuotesOpen(false); setTab("sell"); }} />
    : quotesOpen ? <Quotes onBack={() => setQuotesOpen(false)} open={setQuote} />
    : profileOpen ? <ProfileEditor onClose={() => setProfileOpen(false)} />
    : designOpen ? <InvoiceDesign onClose={() => setDesignOpen(false)} />
    : itemsOpen ? <Items openEditor={setItemEditor} onBack={() => setItemsOpen(false)} />
    : sale ? <SaleDetail id={sale} onClose={() => setSale(null)} goSell={() => { setSale(null); setTab("sell"); }} openCustomer={openLedger} />
    : ledger ? <CustomerLedgerScreen partyId={ledger} onBack={() => setLedger(null)} openSale={(id) => { setLedger(null); setSale(id); }} />
    : customers ? <Customers onBack={() => setCustomers(false)} openEditor={setCustEditor} />
    : tab === "home" ? <Home go={(t) => go(t)} open={setSale} addItem={() => setItemEditor(null)} addCustomer={() => setCustEditor(null)} />
    : tab === "sell" ? <Sell onDone={(id) => { setTab("sales"); setSale(id); }} addItem={() => setItemEditor(null)} />
    : tab === "sales" ? <Sales open={setSale} />
    : tab === "dues" ? <Dues open={setLedger} />
    : <More openCustomers={() => setCustomers(true)} openProfile={() => setProfileOpen(true)} openItems={() => setItemsOpen(true)} openDesign={() => setDesignOpen(true)} openQuotes={() => setQuotesOpen(true)} />;

  return (
    <View style={{ flex: 1, backgroundColor: c.bg }}>
      <View style={{ paddingTop: insets.top, backgroundColor: c.surface }}><SyncBanner /></View>
      <TopInsetHandled value><View style={{ flex: 1 }}>{body}</View></TopInsetHandled>

      {itemEditor !== undefined ? <ItemEditor id={itemEditor} onClose={() => setItemEditor(undefined)} /> : null}
      {custEditor !== undefined ? <CustomerEditor id={custEditor} onClose={() => setCustEditor(undefined)} /> : null}

      <View style={{ flexDirection: "row", backgroundColor: c.bar, paddingBottom: insets.bottom, alignItems: "flex-end" }} accessibilityRole="tablist">
        {TABS.map((t) => {
          const on = tab === t.key && !sale && !customers && !profileOpen && !itemsOpen && !designOpen && !ledger && !quotesOpen && !quote;
          if (t.key === "sell") {
            return (
              <View key={t.key} style={{ flex: 1, alignItems: "center" }}>
                <Tap label="New sale" onPress={() => go("sell")} style={{ marginTop: -22, marginBottom: 6, width: 62, height: 62, borderRadius: 31, backgroundColor: c.accent, alignItems: "center", justifyContent: "center", borderWidth: 4, borderColor: c.bar }}>
                  <Icon name="add" size={34} color={c.onAccent} />
                </Tap>
              </View>
            );
          }
          return (
            <Tap key={t.key} label={t.label} onPress={() => go(t.key)} style={{ flex: 1, minHeight: 62, alignItems: "center", justifyContent: "center", gap: 3 }}>
              <Icon name={on ? t.on : t.off} size={23} color={on ? c.barActive : c.barText} />
              <T v="small" color={on ? c.barActive : c.barText} style={{ fontSize: 11, fontFamily: "IBMPlexSans_600SemiBold" }}>{t.label}</T>
            </Tap>
          );
        })}
      </View>
    </View>
  );
}

export default function App() {
  const scheme = useColorScheme();
  const [loaded] = useFonts({
    ...Ionicons.font,
    IBMPlexSans_400Regular, IBMPlexSans_500Medium, IBMPlexSans_600SemiBold, IBMPlexMono_500Medium, IBMPlexMono_600SemiBold, SpaceGrotesk_500Medium, SpaceGrotesk_700Bold,
  });
  return (
    <SafeAreaProvider>
      <AppProvider>
        <StatusBar style={scheme === "dark" ? "light" : "dark"} />
        {loaded ? <Shell /> : <Splash />}
      </AppProvider>
    </SafeAreaProvider>
  );
}
