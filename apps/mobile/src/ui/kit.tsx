import * as Haptics from "expo-haptics";
import React, { createContext, use } from "react";
import { ActivityIndicator, Platform, Pressable, StyleSheet, Text, TextInput, View, type StyleProp, type TextInputProps, type ViewStyle } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { radius, space, usePalette } from "../theme";

/** The signed-in shell pads the status bar itself (its sync banner sits there), so screens inside it must not pad again. */
export const TopInsetHandled = createContext(false);

export function Screen({ children, style }: { children: React.ReactNode; style?: StyleProp<ViewStyle> }) {
  const c = usePalette();
  const handled = use(TopInsetHandled);
  return <SafeAreaView edges={handled ? [] : ["top"]} style={[{ flex: 1, backgroundColor: c.bg }, style]}>{children}</SafeAreaView>;
}

export function H1({ children }: { children: React.ReactNode }) {
  const c = usePalette();
  return <Text accessibilityRole="header" style={{ fontSize: 28, fontWeight: "700", color: c.text, letterSpacing: -0.4 }}>{children}</Text>;
}
export function Body({ children, muted, small, style }: { children: React.ReactNode; muted?: boolean; small?: boolean; style?: StyleProp<import("react-native").TextStyle> }) {
  const c = usePalette();
  return <Text style={[{ fontSize: small ? 13 : 15, lineHeight: small ? 18 : 21, color: muted ? c.muted : c.text }, style]}>{children}</Text>;
}
export function Label({ children }: { children: React.ReactNode }) {
  const c = usePalette();
  return <Text style={{ fontSize: 12, fontWeight: "700", letterSpacing: 0.6, textTransform: "uppercase", color: c.muted }}>{children}</Text>;
}

export function Card({ children, style }: { children: React.ReactNode; style?: StyleProp<ViewStyle> }) {
  const c = usePalette();
  return <View style={[{ backgroundColor: c.surface, borderRadius: radius.lg, borderWidth: StyleSheet.hairlineWidth * 2, borderColor: c.border, padding: space.lg }, style]}>{children}</View>;
}

type Kind = "primary" | "quiet" | "danger";
export function Button({ title, onPress, kind = "primary", disabled, busy, style, accessibilityLabel }: { title: string; onPress: () => void; kind?: Kind; disabled?: boolean; busy?: boolean; style?: StyleProp<ViewStyle>; accessibilityLabel?: string }) {
  const c = usePalette();
  const off = disabled || busy;
  return (
    <Pressable
      accessibilityRole="button" accessibilityLabel={accessibilityLabel ?? title} accessibilityState={{ disabled: !!off, busy: !!busy }}
      disabled={off}
      onPress={() => { if (Platform.OS !== "web") void Haptics.selectionAsync(); onPress(); }}
      style={({ pressed }) => [
        { minHeight: 52, borderRadius: radius.md, alignItems: "center", justifyContent: "center", paddingHorizontal: space.lg, flexDirection: "row", gap: 8 },
        kind === "primary" && { backgroundColor: off ? c.accentSoft : pressed ? c.accentPressed : c.accent },
        kind === "quiet" && { backgroundColor: c.surface, borderWidth: 1, borderColor: c.borderStrong, opacity: pressed ? 0.7 : 1 },
        kind === "danger" && { backgroundColor: c.negativeSoft, opacity: pressed ? 0.7 : 1 },
        off && kind !== "primary" && { opacity: 0.5 },
        style,
      ]}
    >
      {busy ? <ActivityIndicator color={kind === "primary" ? c.onAccent : c.text} /> : null}
      <Text style={{ fontSize: 16, fontWeight: "600", color: kind === "primary" ? (off ? c.muted : c.onAccent) : kind === "danger" ? c.negative : c.text }}>{title}</Text>
    </Pressable>
  );
}

export function Field({ label, error, ...p }: TextInputProps & { label: string; error?: string | null }) {
  const c = usePalette();
  return (
    <View style={{ gap: 6 }}>
      <Text style={{ fontSize: 13, fontWeight: "600", color: c.text }}>{label}</Text>
      <TextInput
        accessibilityLabel={label} placeholderTextColor={c.faint}
        {...p}
        style={[{ minHeight: 52, borderRadius: radius.md, borderWidth: 1, borderColor: error ? c.negative : c.borderStrong, backgroundColor: c.surface, color: c.text, paddingHorizontal: 14, fontSize: 17 }, p.style]}
      />
      {error ? <Text style={{ color: c.negative, fontSize: 13 }}>{error}</Text> : null}
    </View>
  );
}

type Tone = "neutral" | "good" | "warn" | "bad" | "info";
export function Badge({ text, tone = "neutral" }: { text: string; tone?: Tone }) {
  const c = usePalette();
  const map: Record<Tone, [string, string]> = { neutral: [c.surfaceAlt, c.muted], good: [c.positiveSoft, c.positive], warn: [c.warningSoft, c.warning], bad: [c.negativeSoft, c.negative], info: [c.infoSoft, c.info] };
  const [bg, fg] = map[tone];
  return <View style={{ backgroundColor: bg, borderRadius: 999, paddingHorizontal: 10, paddingVertical: 3, alignSelf: "flex-start" }}><Text style={{ color: fg, fontSize: 12, fontWeight: "700" }}>{text}</Text></View>;
}

export function Empty({ title, hint }: { title: string; hint?: string }) {
  const c = usePalette();
  return (
    <View style={{ padding: space.xxl, alignItems: "center", gap: 6 }}>
      <Text style={{ fontSize: 17, fontWeight: "600", color: c.text, textAlign: "center" }}>{title}</Text>
      {hint ? <Text style={{ fontSize: 14, color: c.muted, textAlign: "center" }}>{hint}</Text> : null}
    </View>
  );
}

export function Chip({ title, active, onPress }: { title: string; active?: boolean; onPress: () => void }) {
  const c = usePalette();
  return (
    <Pressable accessibilityRole="button" accessibilityState={{ selected: !!active }} onPress={onPress}
      style={{ minHeight: 40, paddingHorizontal: 14, borderRadius: 999, justifyContent: "center", backgroundColor: active ? c.accentSoft : c.surface, borderWidth: 1, borderColor: active ? c.accent : c.border }}>
      <Text style={{ fontSize: 14, fontWeight: "600", color: active ? c.accent : c.text }}>{title}</Text>
    </Pressable>
  );
}

export const Divider = () => { const c = usePalette(); return <View style={{ height: StyleSheet.hairlineWidth * 2, backgroundColor: c.border }} />; };
