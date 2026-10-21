import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./locales/en.json";

/** UI languages. `name` is the endonym shown in the switcher, so a user who
 * cannot read the current language can still find their own. Translations
 * other than English were machine-drafted and need native-speaker review. */
export const LANGUAGES = [
  { code: "en", name: "English" },
  { code: "es", name: "Español" },
  { code: "fr", name: "Français" },
  { code: "de", name: "Deutsch" },
  { code: "it", name: "Italiano" },
  { code: "pt", name: "Português" },
  { code: "nl", name: "Nederlands" },
  { code: "hi", name: "हिन्दी" },
] as const;

export type LanguageCode = (typeof LANGUAGES)[number]["code"];

const STORAGE_KEY = "rechvix:language";

// One lazily-loaded chunk per non-English language; English ships in the main
// bundle because it is the fallback for every missing key.
const loaders = import.meta.glob<{ default: Record<string, unknown> }>("./locales/*.json");

function isSupported(code: string | null | undefined): code is LanguageCode {
  return LANGUAGES.some((l) => l.code === code);
}

export function initialLanguage(): LanguageCode {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (isSupported(stored)) return stored;
  } catch {
    // storage disabled: fall through to the browser language
  }
  for (const tag of navigator.languages ?? [navigator.language]) {
    const base = tag.toLowerCase().split("-")[0];
    if (isSupported(base)) return base;
  }
  return "en";
}

void i18n.use(initReactI18next).init({
  resources: { en: { translation: en } },
  lng: "en",
  fallbackLng: "en",
  interpolation: { escapeValue: false }, // React escapes
  returnNull: false,
});

/** Loads (if needed), activates and remembers a language. Falls back to
 * English when the bundle cannot be fetched, so the UI never goes blank. */
export async function setLanguage(code: LanguageCode): Promise<void> {
  if (code !== "en" && !i18n.hasResourceBundle(code, "translation")) {
    const load = loaders[`./locales/${code}.json`];
    if (!load) return;
    try {
      i18n.addResourceBundle(code, "translation", (await load()).default, true, true);
    } catch {
      return;
    }
  }
  await i18n.changeLanguage(code);
  document.documentElement.lang = code;
  document.documentElement.dir = "ltr"; // no RTL language is shipped yet
  try {
    localStorage.setItem(STORAGE_KEY, code);
  } catch {
    // not remembered across reloads
  }
}

export function currentLanguage(): LanguageCode {
  return isSupported(i18n.language) ? i18n.language : "en";
}

// --- Formatting locale -------------------------------------------------
// Language and number/date conventions are separate choices: a Spanish
// speaker working for a US company still sees US dates and amounts. The
// region comes from the active company's country.

let formatRegion = "";

export function setFormatRegion(countryCode: string | undefined | null) {
  formatRegion = /^[A-Za-z]{2}$/.test(countryCode ?? "") ? (countryCode as string).toUpperCase() : "";
}

/** BCP 47 locale for Intl: "es-ES", "en-IN", or just the language when the
 * region combination is unknown to the runtime. */
export function formatLocale(): string {
  const lang = currentLanguage();
  if (!formatRegion) return lang;
  const candidate = `${lang}-${formatRegion}`;
  try {
    return Intl.NumberFormat.supportedLocalesOf(candidate).length > 0 ? candidate : lang;
  } catch {
    return lang;
  }
}

export default i18n;
