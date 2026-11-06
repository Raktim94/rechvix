import { Ionicons } from "@expo/vector-icons";
import * as Haptics from "expo-haptics";
import React, { createContext, use } from "react";
import { ActivityIndicator, Animated, Platform, Pressable, StyleSheet, TextInput, View, type StyleProp, type TextInputProps, type ViewStyle } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { fonts, radius, space, usePalette } from "../theme";
import { T } from "./text";

/** The signed-in shell pads the status bar itself (its sync banner sits there), so screens inside it must not pad again. */
export const TopInsetHandled = createContext(false);

export function Screen({ children, style }: { children: React.ReactNode; style?: StyleProp<ViewStyle> }) {
  const c = usePalette();
  const handled = use(TopInsetHandled);
  return <SafeAreaView edges={handled ? [] : ["top"]} style={[{ flex: 1, backgroundColor: c.bg }, style]}>{children}</SafeAreaView>;
}

export function H1({ children }: { children: React.ReactNode }) {
  return <T v="title" accessibilityRole="header">{children}</T>;
}
export function Body({ children, muted, small, style }: { children: React.ReactNode; muted?: boolean; small?: boolean; style?: StyleProp<import("react-native").TextStyle> }) {
  return <T v={small ? "small" : "body"} muted={muted} style={style}>{children}</T>;
}
export function Label({ children }: { children: React.ReactNode }) {
  return <T v="label" muted>{children}</T>;
}

export type IconName = React.ComponentProps<typeof Ionicons>["name"];
export function Icon({ name, size = 22, color }: { name: IconName; size?: number; color?: string }) {
  const c = usePalette();
  return <Ionicons name={name} size={size} color={color ?? c.text} accessible={false} />;
}

export function Card({ children, style }: { children: React.ReactNode; style?: StyleProp<ViewStyle> }) {
  const c = usePalette();
  return <View style={[{ backgroundColor: c.surface, borderRadius: radius.lg + 2, borderWidth: 1, borderColor: c.border, padding: space.lg }, style]}>{children}</View>;
}

/** Pressable with a quick, subtle scale so every tap feels acknowledged.
 *  Layout props (flex, width, margins...) go on the outer Pressable, where the parent's layout can see them; the rest is the visual box. */
const LAYOUT = ["flex", "flexGrow", "flexShrink", "flexBasis", "alignSelf", "width", "minWidth", "maxWidth", "margin", "marginTop", "marginBottom", "marginLeft", "marginRight", "marginHorizontal", "marginVertical", "position", "left", "right", "top", "bottom"] as const;
export function Tap({ children, onPress, style, label, disabled, haptic = true }: { children: React.ReactNode; onPress?: () => void; style?: StyleProp<ViewStyle>; label?: string; disabled?: boolean; haptic?: boolean }) {
  const [scale] = React.useState(() => new Animated.Value(1));
  const to = (v: number) => Animated.timing(scale, { toValue: v, duration: 90, useNativeDriver: true }).start();
  const flat = (StyleSheet.flatten(style) ?? {}) as Record<string, unknown>;
  const outer: Record<string, unknown> = {}, inner: Record<string, unknown> = {};
  for (const k of Object.keys(flat)) { if ((LAYOUT as readonly string[]).includes(k)) outer[k] = flat[k]; else inner[k] = flat[k]; }
  return (
    <Pressable accessibilityRole="button" accessibilityLabel={label} accessibilityState={{ disabled: !!disabled }} disabled={disabled} style={outer as ViewStyle}
      onPressIn={() => to(0.97)} onPressOut={() => to(1)}
      onPress={() => { if (haptic && Platform.OS !== "web") void Haptics.selectionAsync(); onPress?.(); }}>
      <Animated.View style={[{ transform: [{ scale }] }, inner as ViewStyle]}>{children}</Animated.View>
    </Pressable>
  );
}

type Kind = "primary" | "quiet" | "danger";
export function Button({ title, onPress, kind = "primary", disabled, busy, style, icon }: { title: string; onPress: () => void; kind?: Kind; disabled?: boolean; busy?: boolean; style?: StyleProp<ViewStyle>; icon?: IconName }) {
  const c = usePalette();
  const off = disabled || busy;
  const fg = kind === "primary" ? (off ? c.muted : c.onAccent) : kind === "danger" ? c.negative : c.text;
  return (
    <Tap label={title} disabled={off} onPress={onPress}
      style={[
        { minHeight: 54, borderRadius: radius.lg, alignItems: "center", justifyContent: "center", paddingHorizontal: space.lg, flexDirection: "row", gap: 8 },
        kind === "primary" && { backgroundColor: off ? c.accentSoft : c.accent },
        kind === "quiet" && { backgroundColor: c.surface, borderWidth: 1, borderColor: c.borderStrong },
        kind === "danger" && { backgroundColor: c.negativeSoft },
        off && kind !== "primary" && { opacity: 0.5 },
        style,
      ]}>
      {busy ? <ActivityIndicator color={fg} /> : icon ? <Icon name={icon} size={20} color={fg} /> : null}
      <T v="h3" color={fg}>{title}</T>
    </Tap>
  );
}

export function Field({ label, error, hint, ...p }: TextInputProps & { label: string; error?: string | null; hint?: string }) {
  const c = usePalette();
  const [focus, setFocus] = React.useState(false);
  return (
    <View style={{ gap: 6 }}>
      <T v="strong" style={{ fontSize: 13.5 }}>{label}</T>
      <TextInput
        accessibilityLabel={label} placeholderTextColor={c.faint}
        {...p}
        onFocus={(e) => { setFocus(true); p.onFocus?.(e); }} onBlur={(e) => { setFocus(false); p.onBlur?.(e); }}
        style={[{ minHeight: 54, borderRadius: radius.md + 2, borderWidth: focus ? 2 : 1, borderColor: error ? c.negative : focus ? c.accent : c.borderStrong, backgroundColor: c.surface, color: c.text, paddingHorizontal: 14, fontSize: 17, fontFamily: fonts.body }, p.style]}
      />
      {error ? <T v="small" color={c.negative}>{error}</T> : hint ? <T v="small" muted>{hint}</T> : null}
    </View>
  );
}

type Tone = "neutral" | "good" | "warn" | "bad" | "info";
export function Badge({ text, tone = "neutral", icon }: { text: string; tone?: Tone; icon?: IconName }) {
  const c = usePalette();
  const map: Record<Tone, [string, string]> = { neutral: [c.surfaceAlt, c.muted], good: [c.positiveSoft, c.positive], warn: [c.warningSoft, c.warning], bad: [c.negativeSoft, c.negative], info: [c.infoSoft, c.info] };
  const [bg, fg] = map[tone];
  return (
    <View style={{ backgroundColor: bg, borderRadius: 999, paddingHorizontal: 10, paddingVertical: 4, alignSelf: "flex-start", flexDirection: "row", alignItems: "center", gap: 5 }}>
      {icon ? <Icon name={icon} size={13} color={fg} /> : null}
      <T v="small" color={fg} style={{ fontFamily: fonts.bodySemi, fontSize: 12 }}>{text}</T>
    </View>
  );
}

export function Empty({ title, hint, icon = "file-tray-outline", action }: { title: string; hint?: string; icon?: IconName; action?: { title: string; onPress: () => void } }) {
  const c = usePalette();
  return (
    <View style={{ padding: space.xxl, alignItems: "center", gap: 10 }}>
      <View style={{ width: 72, height: 72, borderRadius: 36, backgroundColor: c.accentSoft, alignItems: "center", justifyContent: "center", marginBottom: 4 }}><Icon name={icon} size={32} color={c.accent} /></View>
      <T v="h3" style={{ textAlign: "center" }}>{title}</T>
      {hint ? <T v="small" muted style={{ textAlign: "center", maxWidth: 280 }}>{hint}</T> : null}
      {action ? <View style={{ marginTop: 8, alignSelf: "stretch" }}><Button title={action.title} onPress={action.onPress} /></View> : null}
    </View>
  );
}

export function Chip({ title, active, onPress }: { title: string; active?: boolean; onPress: () => void }) {
  const c = usePalette();
  return (
    <Tap label={title} onPress={onPress} style={{ minHeight: 40, paddingHorizontal: 16, borderRadius: 999, justifyContent: "center", backgroundColor: active ? c.accent : c.surface, borderWidth: 1, borderColor: active ? c.accent : c.border }}>
      <T v="strong" color={active ? c.onAccent : c.text} style={{ fontSize: 14 }}>{title}</T>
    </Tap>
  );
}

/** Round icon button used in headers. */
export function IconButton({ name, label, onPress, tone }: { name: IconName; label: string; onPress: () => void; tone?: "accent" }) {
  const c = usePalette();
  return (
    <Tap label={label} onPress={onPress} style={{ width: 46, height: 46, borderRadius: 23, alignItems: "center", justifyContent: "center", backgroundColor: tone === "accent" ? c.accent : c.surface, borderWidth: tone === "accent" ? 0 : 1, borderColor: c.border }}>
      <Icon name={name} size={22} color={tone === "accent" ? c.onAccent : c.text} />
    </Tap>
  );
}

/** Screen header with an optional back affordance. */
export function Header({ title, onBack, right, sub }: { title: string; onBack?: () => void; right?: React.ReactNode; sub?: string }) {
  return (
    <View style={{ paddingHorizontal: space.lg, paddingTop: space.md, paddingBottom: space.sm, gap: 10 }}>
      {onBack ? <IconButton name="chevron-back" label="Back" onPress={onBack} /> : null}
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 12 }}>
        <View style={{ flex: 1 }}><T v="title">{title}</T>{sub ? <T v="small" muted>{sub}</T> : null}</View>
        {right}
      </View>
    </View>
  );
}

export const Divider = () => { const c = usePalette(); return <View style={{ height: StyleSheet.hairlineWidth * 2, backgroundColor: c.border }} />; };
