import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Link, useNavigate } from "@tanstack/react-router";
import styles from "./auth.module.css";
import { Logo } from "../components/Logo";
import { useAuth } from "../auth/AuthProvider";
import { ApiError } from "../lib/api-client";

const schema = z.object({
  email: z.string().min(1, "Email is required").email("Enter a valid email address"),
  password: z.string().min(1, "Password is required"),
});
type FormValues = z.infer<typeof schema>;

export function LoginPage() {
  const { login } = useAuth();
  const navigate = useNavigate();
  const [serverError, setServerError] = useState<string | null>(null);
  const [mfaRequired, setMfaRequired] = useState(false);
  const [mfaCode, setMfaCode] = useState("");

  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({ resolver: zodResolver(schema) });

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
    <div className={styles.page}>
      <div className={styles.card}>
        <div className={styles.wordmark}>
          <Logo />
          rechvix
        </div>
        <h1 className={styles.title}>Sign in</h1>
        <p className={styles.subtitle}>Enter your work email and password.</p>

        {serverError ? (
          <div className={styles.formError} role="alert">
            {serverError}
          </div>
        ) : null}

        {/* eslint-disable-next-line @typescript-eslint/no-misused-promises */}
        <form onSubmit={handleSubmit(onSubmit)} noValidate>
          <div className={styles.field}>
            <label htmlFor="email">Email</label>
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
            <label htmlFor="password">Password</label>
            <input
              id="password"
              type="password"
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
                <Link to="/forgot-password">Forgot password?</Link>
              </p>
            ) : null}
          </div>

          {mfaRequired ? (
            <div className={styles.field}>
              <label htmlFor="mfa">Verification code</label>
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
            {isSubmitting ? "Signing in…" : mfaRequired ? "Verify and sign in" : "Sign in"}
          </button>
        </form>
      </div>
      <p className={styles.brandFooter}>Built by NodeDR Infotech Private Limited</p>
    </div>
  );
}
