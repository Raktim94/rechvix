import { useState } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Link, useNavigate } from "@tanstack/react-router";
import styles from "./auth.module.css";
import { Logo } from "../components/Logo";
import { api, ApiError } from "../lib/api-client";

const schema = z
  .object({
    password: z.string().min(12, "Use at least 12 characters"),
    confirmPassword: z.string().min(1, "Confirm your password"),
  })
  .refine((v) => v.password === v.confirmPassword, {
    message: "Passwords do not match",
    path: ["confirmPassword"],
  });
type FormValues = z.infer<typeof schema>;

/** Completes the link POST /auth/password-reset/request sent — the token
 * comes in as `?token=`, this screen never sees or needs the email. */
export function ResetPasswordPage({ token }: { token: string | undefined }) {
  const navigate = useNavigate();
  const [serverError, setServerError] = useState<string | null>(null);
  const {
    register,
    handleSubmit,
    formState: { errors, isSubmitting },
  } = useForm<FormValues>({ resolver: zodResolver(schema) });

  const onSubmit = async (values: FormValues) => {
    setServerError(null);
    try {
      await api.post("/auth/password-reset/complete", {
        token,
        new_password: values.password,
        confirm_password: values.confirmPassword,
      });
      await navigate({ to: "/login" });
    } catch (err) {
      setServerError(err instanceof ApiError ? err.message : "Could not reset your password. Please try again.");
    }
  };

  return (
    <div className={styles.page}>
      <div className={styles.card}>
        <div className={styles.wordmark}>
          <Logo />
          rechvix
        </div>
        <h1 className={styles.title}>Set a new password</h1>
        <p className={styles.subtitle}>Choose a new password for your account.</p>

        {!token ? (
          <div className={styles.formError} role="alert">
            This link is missing its reset token. Request a new one from the{" "}
            <Link to="/forgot-password">password reset</Link> page.
          </div>
        ) : (
          <>
            {serverError ? (
              <div className={styles.formError} role="alert">
                {serverError}
              </div>
            ) : null}
            {/* eslint-disable-next-line @typescript-eslint/no-misused-promises */}
            <form onSubmit={handleSubmit(onSubmit)} noValidate>
              <div className={styles.field}>
                <label htmlFor="password">New password</label>
                <input id="password" type="password" autoComplete="new-password" {...register("password")} />
                {errors.password ? <p className={styles.error}>{errors.password.message}</p> : null}
              </div>
              <div className={styles.field}>
                <label htmlFor="confirmPassword">Confirm password</label>
                <input id="confirmPassword" type="password" autoComplete="new-password" {...register("confirmPassword")} />
                {errors.confirmPassword ? <p className={styles.error}>{errors.confirmPassword.message}</p> : null}
              </div>
              <button type="submit" className={styles.submit} disabled={isSubmitting}>
                {isSubmitting ? "Saving…" : "Reset password"}
              </button>
            </form>
          </>
        )}
      </div>
      <p className={styles.brandFooter}>Built by NodeDR Infotech Private Limited</p>
    </div>
  );
}
