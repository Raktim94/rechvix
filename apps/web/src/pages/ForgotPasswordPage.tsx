import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Link } from "@tanstack/react-router";
import styles from "./auth.module.css";
import { Logo } from "../components/Logo";
import { api, ApiError } from "../lib/api-client";

const schema = z.object({
  email: z.string().min(1, "Email is required").email("Enter a valid email address"),
});
type FormValues = z.infer<typeof schema>;

/**
 * Talks to internal/modules/identity's POST /auth/password-reset/request —
 * always answers the same way regardless of whether the email matched an
 * account (brief §27 enumeration protection), so this screen has nothing
 * to branch on either.
 */
export function ForgotPasswordPage() {
  const [done, setDone] = useState(false);
  const [serverError, setServerError] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({ resolver: zodResolver(schema) });

  const onSubmit = async (values: FormValues) => {
    setServerError(null);
    try {
      await api.post("/auth/password-reset/request", { email: values.email });
      setDone(true);
    } catch (err) {
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
        <h1 className={styles.title}>Reset your password</h1>
        <p className={styles.subtitle}>Enter your work email and we'll send you a reset link.</p>

        {serverError ? (
          <div className={styles.formError} role="alert">
            {serverError}
          </div>
        ) : null}

        {done ? (
          <p className={styles.subtitle} role="status" style={{ marginBottom: 0 }}>
            If an account exists for that email, a reset link is on its way.
          </p>
        ) : (
          // eslint-disable-next-line @typescript-eslint/no-misused-promises
          <form onSubmit={handleSubmit(onSubmit)} noValidate>
            <div className={styles.field}>
              <label htmlFor="email">Email</label>
              <input id="email" type="email" autoComplete="username" {...register("email")} />
              {errors.email ? <p className={styles.error}>{errors.email.message}</p> : null}
            </div>
            <button type="submit" className={styles.submit} disabled={isSubmitting}>
              {isSubmitting ? "Sending…" : "Send reset link"}
            </button>
          </form>
        )}

        <p className={styles.subtitle} style={{ marginTop: "var(--space-4)", marginBottom: 0 }}>
          <Link to="/login">Back to sign in</Link>
        </p>
      </div>
      <p className={styles.brandFooter}>Built by NodeDR Infotech Private Limited</p>
    </div>
  );
}
