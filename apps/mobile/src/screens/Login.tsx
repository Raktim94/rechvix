import React, { useState } from "react";
import { KeyboardAvoidingView, Platform, ScrollView, View } from "react-native";
import { useApp } from "../state/app";
import { space, usePalette } from "../theme";
import { Body, Button, Card, Field, H1, Screen } from "../ui/kit";

export function Login() {
  const { login, email: saved, server, counts, sessionExpired, forgetServer } = useApp();
  const c = usePalette();
  const [email, setEmail] = useState(saved ?? "");
  const [password, setPassword] = useState("");
  const [mfa, setMfa] = useState("");
  const [needMfa, setNeedMfa] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const waiting = counts.pending + counts.syncing + counts.failed;

  async function go() {
    if (!email.trim() || !password) { setErr("Enter your email and password."); return; }
    setBusy(true); setErr(null);
    const r = await login(email, password, mfa);
    setBusy(false);
    if (r === "MFA_REQUIRED") { setNeedMfa(true); setErr("Enter the 6-digit code from your authenticator app."); } else if (r) setErr(r);
  }

  return (
    <Screen>
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1 }}>
        <ScrollView contentContainerStyle={{ padding: space.xl, paddingTop: 48, gap: space.lg }} keyboardShouldPersistTaps="handled">
          <H1>Sign in</H1>
          {sessionExpired && waiting > 0 ? (
            <Card style={{ backgroundColor: c.warningSoft, borderColor: c.warning }}>
              <Body>Your session ended. {waiting} sale{waiting === 1 ? " is" : "s are"} safe on this phone and will sync as soon as you sign in.</Body>
            </Card>
          ) : (
            <Body muted>{server ? `Signing in to ${server.replace(/^https?:\/\//, "")}` : ""}</Body>
          )}
          <Field label="Email" value={email} onChangeText={setEmail} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" textContentType="username" autoComplete="email" />
          <Field label="Password" value={password} onChangeText={setPassword} secureTextEntry textContentType="password" autoComplete="password" returnKeyType="go" onSubmitEditing={go} />
          {needMfa ? <Field label="Authenticator code" value={mfa} onChangeText={setMfa} keyboardType="number-pad" maxLength={6} /> : null}
          {err ? <Body small style={{ color: c.negative }}>{err}</Body> : null}
          <Button title="Sign in" onPress={go} busy={busy} />
          {waiting === 0 ? <View><Button title="Use a different server" kind="quiet" onPress={() => void forgetServer()} /></View> : null}
        </ScrollView>
      </KeyboardAvoidingView>
    </Screen>
  );
}
