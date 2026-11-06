import { useColorScheme } from "react-native";

/** Design tokens, taken from the web app (apps/web/src/styles/tokens.css) so the phone app reads as the same product.
 *  Cool paper, one confident emerald accent, tabular figures wherever money appears. */
export interface Palette {
  bg: string; surface: string; surfaceAlt: string; border: string; borderStrong: string;
  text: string; muted: string; faint: string;
  accent: string; accentPressed: string; accentSoft: string; onAccent: string;
  positive: string; positiveSoft: string; negative: string; negativeSoft: string; warning: string; warningSoft: string; info: string; infoSoft: string;
  bar: string; barText: string; barActive: string;
}

export const light: Palette = {
  bg: "#f6f7f5", surface: "#ffffff", surfaceAlt: "#eef0ec", border: "#dbdfd8", borderStrong: "#c3cac0",
  text: "#16211c", muted: "#5b6b62", faint: "#68726c",
  accent: "#0f6e5c", accentPressed: "#0c5a4b", accentSoft: "#e3f0ec", onAccent: "#ffffff",
  positive: "#146b49", positiveSoft: "#e5f4ec", negative: "#9c3a26", negativeSoft: "#fbeae5", warning: "#7a5407", warningSoft: "#fbf1de", info: "#1f4f96", infoSoft: "#e6eefa",
  bar: "#0e1f1b", barText: "#aebcb5", barActive: "#ffffff",
};

export const dark: Palette = {
  bg: "#0d151a", surface: "#142029", surfaceAlt: "#1d2c35", border: "#31424b", borderStrong: "#44575f",
  text: "#f0f5f6", muted: "#afbec7", faint: "#93a3ac",
  accent: "#52d4bd", accentPressed: "#3fbfa8", accentSoft: "#123b37", onAccent: "#092622",
  positive: "#5ae0a0", positiveSoft: "#12352a", negative: "#ff918a", negativeSoft: "#40201e", warning: "#ffca70", warningSoft: "#3b2d10", info: "#8db8ff", infoSoft: "#16294a",
  bar: "#0a1114", barText: "#93a3ac", barActive: "#ffffff",
};

export function usePalette(): Palette {
  return useColorScheme() === "dark" ? dark : light;
}

export const space = { xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32 } as const;
export const radius = { sm: 8, md: 10, lg: 14 } as const;
/** Tabular numerals so columns of rupee figures line up. */
export const tabular = { fontVariant: ["tabular-nums" as const] };
