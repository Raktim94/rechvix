import React, { useEffect, useRef, useState } from "react";
import { Animated, Image, KeyboardAvoidingView, Platform, ScrollView, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { DEFAULT_PROFILE, type Profile } from "../db/repo";
import { GST_RATES } from "../lib/tax";
import { useApp } from "../state/app";
import { radius, space, usePalette } from "../theme";
import { Button, Chip, Field, Icon, Label, type IconName } from "../ui/kit";
import { T } from "../ui/text";

const CURRENCIES = [["INR", "₹ Rupee"], ["USD", "$ Dollar"], ["EUR", "€ Euro"], ["GBP", "£ Pound"], ["AED", "AED Dirham"]] as const;

function Fade({ children, delay = 0 }: { children: React.ReactNode; delay?: number }) {
  const v = useRef(new Animated.Value(0)).current;
  useEffect(() => { Animated.timing(v, { toValue: 1, duration: 420, delay, useNativeDriver: true }).start(); }, [v, delay]);
  return <Animated.View style={{ opacity: v, transform: [{ translateY: v.interpolate({ inputRange: [0, 1], outputRange: [14, 0] }) }] }}>{children}</Animated.View>;
}

function Feature({ icon, title, text }: { icon: IconName; title: string; text: string }) {
  return (
    <View style={{ flexDirection: "row", gap: 14, alignItems: "flex-start" }}>
      <View style={{ width: 46, height: 46, borderRadius: 14, backgroundColor: "rgba(255,255,255,0.12)", alignItems: "center", justifyContent: "center" }}><Icon name={icon} size={24} color="#fff" /></View>
      <View style={{ flex: 1 }}><T v="h3" color="#fff">{title}</T><T v="small" color="rgba(255,255,255,0.72)">{text}</T></View>
    </View>
  );
}

export function Welcome() {
  const { completeSetup } = useApp();
  const c = usePalette();
  const [step, setStep] = useState<0 | 1>(0);
  const [p, setP] = useState<Profile>({ ...DEFAULT_PROFILE });
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const set = <K extends keyof Profile>(k: K, v: Profile[K]) => setP((x) => ({ ...x, [k]: v }));

  async function finish() {
    if (!p.name.trim()) { setErr("Give your business a name. It goes on your invoices."); return; }
    setBusy(true);
    await completeSetup({ ...p, name: p.name.trim() });
  }

  if (step === 0) {
    return (
      <View style={{ flex: 1, backgroundColor: "#0e1f1b" }}>
        <SafeAreaView style={{ flex: 1, padding: space.xl, justifyContent: "space-between" }}>
          <Fade><View style={{ marginTop: 28, gap: 22 }}>
            <Image source={require("../../assets/icon.png")} style={{ width: 64, height: 64, borderRadius: 18 }} accessibilityIgnoresInvertColors />
            <T v="title" color="#fff" style={{ fontSize: 40, lineHeight: 44 }}>Billing that never waits for signal.</T>
            <T v="body" color="rgba(255,255,255,0.78)">Make invoices, track stock and know who owes you. Everything works on this phone, with or without internet.</T>
          </View></Fade>
          <Fade delay={180}><View style={{ gap: 20 }}>
            <Feature icon="flash-outline" title="Ready in a minute" text="No account and no server to set up first." />
            <Feature icon="receipt-outline" title="Proper GST invoices" text="Share a clean PDF on WhatsApp in two taps." />
            <Feature icon="cloud-upload-outline" title="Connect a server any time" text="Add your Rechvix server later and everything syncs." />
          </View></Fade>
          <Fade delay={320}><Button title="Get started" onPress={() => setStep(1)} icon="arrow-forward" /></Fade>
        </SafeAreaView>
      </View>
    );
  }

  return (
    <SafeAreaView style={{ flex: 1, backgroundColor: c.bg }}>
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1 }}>
        <ScrollView contentContainerStyle={{ padding: space.xl, gap: space.lg, paddingBottom: 48 }} keyboardShouldPersistTaps="handled">
          <Button title="Back" kind="quiet" icon="chevron-back" onPress={() => setStep(0)} style={{ alignSelf: "flex-start", minHeight: 44 }} />
          <View style={{ gap: 6 }}><T v="title">Your business</T><T v="body" muted>This appears on your invoices. You can change it any time.</T></View>
          <Field label="Business name" value={p.name} onChangeText={(v) => { set("name", v); setErr(null); }} placeholder="e.g. Sharma Hardware" autoCapitalize="words" error={err} />
          <Field label="Phone (optional)" value={p.phone} onChangeText={(v) => set("phone", v)} keyboardType="phone-pad" />
          <Field label="Address (optional)" value={p.address} onChangeText={(v) => set("address", v)} multiline style={{ minHeight: 76, paddingTop: 12, textAlignVertical: "top" }} />
          <Field label="GSTIN (optional)" value={p.gstin} onChangeText={(v) => { const g = v.toUpperCase(); set("gstin", g); if (g.length >= 2 && /^\d\d/.test(g)) set("gstState", g.slice(0, 2)); }} autoCapitalize="characters" autoCorrect={false} hint="Leave blank if you are not GST-registered." />
          <View style={{ gap: 8 }}><Label>Currency</Label>
            <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>{CURRENCIES.map(([k, n]) => <Chip key={k} title={n} active={p.currency === k} onPress={() => { set("currency", k); set("country", k === "INR" ? "IN" : p.country); }} />)}</View></View>
          {p.currency === "INR" ? (
            <View style={{ gap: 8 }}><Label>Usual GST rate on your items</Label>
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>{GST_RATES.map((r) => <Chip key={r} title={`${r}%`} active={p.defaultGst === r} onPress={() => set("defaultGst", r)} />)}</View>
              <T v="small" muted>New items start with this rate. You can set a different one per item.</T></View>
          ) : null}
          <View style={{ height: 6 }} />
          <Button title="Start billing" onPress={() => void finish()} busy={busy} icon="checkmark" />
          <View style={{ flexDirection: "row", gap: 10, alignItems: "center", padding: 14, borderRadius: radius.lg, backgroundColor: c.accentSoft }}>
            <Icon name="cloud-outline" size={22} color={c.accent} /><T v="small" style={{ flex: 1 }}>Have a Rechvix server? Connect it later from More. Your data uploads and merges safely.</T>
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
