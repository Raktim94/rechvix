/**
 * echarts theme colors, mirroring the custom properties in
 * styles/tokens.css. echarts options are plain JS (not CSS), so they
 * can't reference a CSS variable directly — this is the one place those
 * token values get duplicated as hex literals, kept in sync with
 * tokens.css by hand. Previously every chart component re-declared its
 * own `dark ? "#.." : "#.."` ternaries for the same handful of colors;
 * this is the single source those now pull from.
 *
 * `categorical` extends the two semantic colors (accent, warning) already
 * in use into a 6-color qualitative sequence for when a report grows a
 * third-plus series. Ordered so neighbors alternate hue *and* lightness —
 * distinguishable under deuteranopia/protanopia (the common forms of
 * red-green color blindness) and still separable in a grayscale export.
 */

export interface ChartPalette {
  accent: string;
  warning: string;
  negative: string;
  text: string;
  grid: string;
  categorical: readonly [string, string, string, string, string, string];
}

const lightPalette: ChartPalette = {
  accent: "#0f6e5c",
  warning: "#906409",
  negative: "#b3452f",
  text: "#5b6b62",
  grid: "#dbdfd8",
  categorical: ["#0f6e5c", "#906409", "#2f6fb3", "#b3452f", "#6a4fb0", "#5b6b62"],
};

const darkPalette: ChartPalette = {
  accent: "#29c191",
  warning: "#e0b355",
  negative: "#e2896a",
  text: "#9db0a4",
  grid: "#2b3632",
  categorical: ["#29c191", "#e0b355", "#6badf0", "#e2896a", "#b39ae0", "#9db0a4"],
};

export function chartPalette(dark: boolean): ChartPalette {
  return dark ? darkPalette : lightPalette;
}
