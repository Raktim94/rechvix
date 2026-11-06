import React from "react";
import { Text, type StyleProp, type TextProps, type TextStyle } from "react-native";
import { fonts, usePalette } from "../theme";

type Variant = "title" | "h2" | "h3" | "body" | "strong" | "small" | "label" | "money" | "moneyLg" | "moneyHero";
const base: Record<Variant, TextStyle> = {
  title: { fontFamily: fonts.display, fontSize: 30, lineHeight: 36, letterSpacing: -0.6 },
  h2: { fontFamily: fonts.display, fontSize: 22, lineHeight: 28, letterSpacing: -0.3 },
  h3: { fontFamily: fonts.bodySemi, fontSize: 17, lineHeight: 23 },
  body: { fontFamily: fonts.body, fontSize: 15, lineHeight: 22 },
  strong: { fontFamily: fonts.bodySemi, fontSize: 15, lineHeight: 22 },
  small: { fontFamily: fonts.body, fontSize: 13, lineHeight: 18 },
  label: { fontFamily: fonts.bodySemi, fontSize: 11.5, lineHeight: 16, letterSpacing: 0.8, textTransform: "uppercase" },
  money: { fontFamily: fonts.mono, fontSize: 15, lineHeight: 22 },
  moneyLg: { fontFamily: fonts.monoBold, fontSize: 22, lineHeight: 28, letterSpacing: -0.3 },
  moneyHero: { fontFamily: fonts.monoBold, fontSize: 38, lineHeight: 44, letterSpacing: -1 },
};

export interface TProps extends TextProps { v?: Variant; muted?: boolean; color?: string; style?: StyleProp<TextStyle> }
/** The one Text component the app uses, so the type system lives in one place. */
export function T({ v = "body", muted, color, style, ...p }: TProps) {
  const c = usePalette();
  return <Text {...p} style={[base[v], { color: color ?? (muted ? c.muted : c.text) }, style]} />;
}
