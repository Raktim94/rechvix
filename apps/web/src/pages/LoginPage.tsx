import { useEffect, useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { useTranslation } from "react-i18next";
import { Link, useNavigate } from "@tanstack/react-router";
import styles from "./auth.module.css";
import { Logo } from "../components/Logo";
import { PasswordInput } from "../components/PasswordInput";
import { LanguageSelect } from "../components/LanguageSelect";
import { useAuth } from "../auth/AuthProvider";
import { api, ApiError } from "../lib/api-client";

type FormValues = { email: string; password: string };

interface BootstrapStatus {
  available: boolean;
}

export function LoginPage() {
  const { t } = useTranslation();
  // Built per render so validation messages follow the selected language.
  const schema = z.object({
    email: z.string().min(1, t("auth.emailRequired")).email(t("auth.emailInvalid")),
    password: z.string().min(1, t("auth.passwordRequired")),
  });
  const { login } = useAuth();
  const navigate = useNavigate();
  const [serverError, setServerError] = useState<string | null>(null);
  const [mfaRequired, setMfaRequired] = useState(false);
  const [mfaCode, setMfaCode] = useState("");
  // Three states: null while the one-time first-run check is in flight,
  // then locked to whatever it found (or false if the check itself
  // failed — fail open to a normal sign-in screen rather than blocking a
  // returning user over a transient network hiccup).
  const [setupAvailable, setSetupAvailable] = useState<boolean | null>(null);
  const justCreated = new URLSearchParams(window.location.search).get("created") === "true";

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({ resolver: zodResolver(schema) });

  useEffect(() => {
    let cancelled = false;
    api
      .get<BootstrapStatus>("/auth/bootstrap")
      .then((res) => {
        if (!cancelled) setSetupAvailable(res.available);
      })
      .catch(() => {
        if (!cancelled) setSetupAvailable(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const onSubmit = async (values: FormValues) => {
    setServerError(null);
    try {
      await login(values.email, values.password, mfaRequired ? mfaCode : undefined);
      await navigate({ to: "/" });
    } catch (err) {
      if (err instanceof ApiError && err.code === "MFA_REQUIRED") {
        setMfaRequired(true);
        return;
      }
      setServerError(err instanceof ApiError ? err.message : "Something went wrong. Please try again.");
    }
  };

  return (
    <main className={styles.page}>
      <div className={styles.card}>
        <div className={styles.wordmark}>
          <Logo />
          rechvix
        </div>
        <h1 className={styles.title}>{t("auth.signIn")}</h1>
        <p className={styles.subtitle}>{t("auth.signInHint")}</p>

        {justCreated ? (
          <div className={styles.formSuccess} role="status">
            {t("auth.businessCreated")}
          </div>
        ) : null}

        {serverError ? (
          <div className={styles.formError} role="alert">
            {serverError}
          </div>
        ) : null}

        {/* eslint-disable-next-line @typescript-eslint/no-misused-promises */}
        <form onSubmit={handleSubmit(onSubmit)} noValidate>
          <div className={styles.field}>
            <label htmlFor="email">{t("auth.email")}</label>
            <input
              id="email"
              type="email"
              autoComplete="username"
              disabled={mfaRequired}
              aria-invalid={!!errors.email}
              aria-describedby={errors.email ? "email-error" : undefined}
              {...register("email")}
            />
            {errors.email ? (
              <p className={styles.error} id="email-error">
                {errors.email.message}
              </p>
            ) : null}
          </div>
          <div className={styles.field}>
            <label htmlFor="password">{t("auth.password")}</label>
            <PasswordInput
              id="password"
              autoComplete="current-password"
              disabled={mfaRequired}
              aria-invalid={!!errors.password}
              aria-describedby={errors.password ? "password-error" : undefined}
              {...register("password")}
            />
            {errors.password ? (
              <p className={styles.error} id="password-error">
                {errors.password.message}
              </p>
            ) : null}
            {!mfaRequired ? (
              <p className={styles.subtitle} style={{ marginTop: "var(--space-1)", marginBottom: 0 }}>
                <Link to="/forgot-password">{t("auth.forgotPassword")}</Link>
              </p>
            ) : null}
          </div>

          {mfaRequired ? (
            <div className={styles.field}>
              <label htmlFor="mfa">{t("auth.verificationCode")}</label>
              <input
                id="mfa"
                type="text"
                inputMode="numeric"
                autoComplete="one-time-code"
                autoFocus
                value={mfaCode}
                onChange={(e) => setMfaCode(e.target.value)}
              />
            </div>
          ) : null}

          <button type="submit" className={styles.submit} disabled={isSubmitting || (mfaRequired && mfaCode === "")}>
            {isSubmitting ? t("auth.signingIn") : mfaRequired ? t("auth.verifyAndSignIn") : t("auth.signIn")}
          </button>
        </form>

        {setupAvailable ? (
          <p className={styles.setupLink}>
            {t("auth.newTo")} <Link to="/setup">{t("auth.setUpBusiness")}</Link>
          </p>
        ) : null}
      </div>
      <p className={styles.brandFooter}>
        <LanguageSelect />
        <br />
        Built by{" "}
        <a href="https://www.nodedr.com/" target="_blank" rel="noopener noreferrer">
          NodeDR Infotech Private Limited
        </a>
      </p>
    </main>
  );
}
