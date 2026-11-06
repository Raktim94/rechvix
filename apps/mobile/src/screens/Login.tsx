import React, { useState } from "react";
import { KeyboardAvoidingView, Platform, ScrollView, View } from "react-native";
import { useApp } from "../state/app";
import { radius, space, usePalette } from "../theme";
import { Button, Field, Header, Icon, Screen } from "../ui/kit";
import { T } from "../ui/text";

export function Login() {
  const { login, email: saved, server, counts, sessionExpired, cancelLinking, linking, disconnect } = useApp();
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
        <ScrollView contentContainerStyle={{ paddingBottom: 40 }} keyboardShouldPersistTaps="handled">
          <Header title="Sign in" onBack={linking ? cancelLinking : undefined} sub={server ? server.replace(/^https?:\/\//, "") : undefined} />
          <View style={{ padding: space.lg, gap: space.lg }}>
            {sessionExpired && waiting > 0 ? (
              <View style={{ flexDirection: "row", gap: 10, padding: 14, borderRadius: radius.lg, backgroundColor: c.warningSoft }}>
                <Icon name="time-outline" size={22} color={c.warning} />
                <T v="small" style={{ flex: 1 }}>Your session ended. {waiting} sale{waiting === 1 ? " is" : "s are"} safe on this phone and will sync as soon as you sign in.</T>
              </View>
            ) : null}
            <Field label="Email" value={email} onChangeText={setEmail} autoCapitalize="none" autoCorrect={false} keyboardType="email-address" textContentType="username" autoComplete="email" />
            <Field label="Password" value={password} onChangeText={setPassword} secureTextEntry textContentType="password" autoComplete="password" returnKeyType="go" onSubmitEditing={go} />
            {needMfa ? <Field label="Authenticator code" value={mfa} onChangeText={setMfa} keyboardType="number-pad" maxLength={6} /> : null}
            {err ? <T v="small" color={c.negative}>{err}</T> : null}
            <Button title="Sign in and sync" onPress={go} busy={busy} icon="sync-outline" />
            {sessionExpired ? <Button title="Work without the server" kind="quiet" onPress={() => void disconnect()} /> : null}
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </Screen>
  );
}
