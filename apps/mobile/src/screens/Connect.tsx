import React, { useState } from "react";
import { KeyboardAvoidingView, Platform, ScrollView, View } from "react-native";
import { isCleartext, normalizeServer } from "../lib/server";
import { useApp } from "../state/app";
import { space, usePalette } from "../theme";
import { Button, Field, Header, Icon, Screen } from "../ui/kit";
import { T } from "../ui/text";

export function Connect() {
  const { connect, server, cancelLinking } = useApp();
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
        <ScrollView contentContainerStyle={{ paddingBottom: 40 }} keyboardShouldPersistTaps="handled">
          <Header title="Connect a server" onBack={cancelLinking} />
          <View style={{ padding: space.lg, gap: space.lg }}>
            <T v="body" muted>Enter the address you use to open Rechvix in a browser. Everything you&apos;ve made on this phone uploads and merges with your business data.</T>
            <Field label="Server address" value={addr} onChangeText={setAddr} placeholder="books.yourshop.com" autoCapitalize="none" autoCorrect={false} keyboardType="url" returnKeyType="go" onSubmitEditing={go} error={err} />
            {preview && isCleartext(preview) ? (
              <View style={{ flexDirection: "row", gap: 10 }}><Icon name="warning-outline" size={20} color={c.warning} /><T v="small" color={c.warning} style={{ flex: 1 }}>This address is not encrypted. That is normal on a shop network, but don&apos;t use it over public Wi-Fi.</T></View>
            ) : null}
            <Button title="Connect" onPress={go} busy={busy} icon="link-outline" />
            <T v="small" muted>On a shop network without a certificate, an address like 192.168.1.40:8080 works too.</T>
          </View>
        </ScrollView>
      </KeyboardAvoidingView>
    </Screen>
  );
}
