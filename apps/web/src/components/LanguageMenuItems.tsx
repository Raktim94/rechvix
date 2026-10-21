import { useTranslation } from "react-i18next";
import { LANGUAGES, currentLanguage, setLanguage } from "../i18n";

/** Radio-style menu entries for choosing the UI language; place inside an
 * element with role="menu". */
export function LanguageMenuItems({ onChosen }: { onChosen: () => void }) {
  const { t } = useTranslation();
  const active = currentLanguage();
  return (
    <div role="group" aria-label={t("shell.language")}>
      {LANGUAGES.map((l) => (
        <button
          key={l.code}
          type="button"
          role="menuitemradio"
          aria-checked={l.code === active}
          lang={l.code}
          onClick={() => {
            void setLanguage(l.code);
            onChosen();
          }}
        >
          {l.code === active ? "✓ " : ""}
          {l.name}
        </button>
      ))}
    </div>
  );
}
