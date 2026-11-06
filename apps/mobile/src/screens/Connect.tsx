import React, { useState } from "react";
import { Image, KeyboardAvoidingView, Platform, ScrollView, View } from "react-native";
import { isCleartext, normalizeServer } from "../lib/server";
import { useApp } from "../state/app";
import { space, usePalette } from "../theme";
import { Body, Button, Field, H1, Screen } from "../ui/kit";

export function Connect() {
  const { connect, server } = useApp();
  const c = usePalette();
  const [addr, setAddr] = useState(server ?? "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function go() {
    if (!addr.trim()) { setErr("Type your server address first."); return; }
    setBusy(true); setErr(null);
    const p = await connect(addr);
    setBusy(false);
    if (p) setErr(p);
  }
  const preview = addr.trim() ? normalizeServer(addr) : null;

  return (
    <Screen>
      <KeyboardAvoidingView behavior={Platform.OS === "ios" ? "padding" : undefined} style={{ flex: 1 }}>
        <ScrollView contentContainerStyle={{ padding: space.xl, paddingTop: 56, gap: space.lg }} keyboardShouldPersistTaps="handled">
          <Image source={require("../../assets/icon.png")} style={{ width: 56, height: 56, borderRadius: 14 }} accessibilityIgnoresInvertColors />
          <H1>Connect to your Rechvix</H1>
          <Body muted>Enter the address you use to open Rechvix in a browser. You only do this once.</Body>
          <Field label="Server address" value={addr} onChangeText={setAddr} placeholder="books.yourshop.com" autoCapitalize="none" autoCorrect={false} keyboardType="url" returnKeyType="go" onSubmitEditing={go} error={err} />
          {preview && isCleartext(preview) ? <Body small muted style={{ color: c.warning }}>This address is not encrypted. That is normal on a shop network, but don't use it over public Wi-Fi.</Body> : null}
          <Button title="Connect" onPress={go} busy={busy} />
          <View><Body small muted>On a shop network without a certificate, an address like 192.168.1.40:8080 works too.</Body></View>
        </ScrollView>
      </KeyboardAvoidingView>
    </Screen>
  );
}
