import { useState } from "react";
import { useTranslation } from "react-i18next";
import { LANGUAGES, currentLanguage, setLanguage, type LanguageCode } from "../i18n";

/** Plain labelled <select> for pages shown before sign-in. */
export function LanguageSelect() {
  const { t } = useTranslation();
  const [value, setValue] = useState<LanguageCode>(currentLanguage());
  return (
    <label style={{ display: "inline-flex", gap: "var(--space-2)", alignItems: "center", fontSize: "var(--text-sm, 0.875rem)", color: "var(--color-text)" }}>
      <span>{t("shell.language")}</span>
      <select
        style={{ minHeight: 44, minWidth: 44, padding: "0 var(--space-2)", color: "var(--color-text)" }}
        value={value}
        onChange={(e) => {
          const code = e.target.value as LanguageCode;
          setValue(code);
          void setLanguage(code);
        }}
      >
        {LANGUAGES.map((l) => (
          <option key={l.code} value={l.code} lang={l.code}>
            {l.name}
          </option>
        ))}
      </select>
    </label>
  );
}
