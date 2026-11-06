import React from "react";
import { Pressable, Text, View } from "react-native";
import { ago } from "../lib/hooks";
import { useApp } from "../state/app";
import { space, usePalette } from "../theme";

/** One honest line about where the data is: the thing a shop owner on a flaky connection most needs to know. */
export function SyncBanner() {
  const { online, syncing, counts, lastSyncAt, syncNow, lastSync } = useApp();
  const c = usePalette();
  const waiting = counts.pending + counts.syncing;
  let tone = c.positive, bg = c.positiveSoft, text = `All synced · ${ago(lastSyncAt)}`;
  if (counts.failed > 0) { tone = c.negative; bg = c.negativeSoft; text = `${counts.failed} sale${counts.failed === 1 ? "" : "s"} need${counts.failed === 1 ? "s" : ""} your attention`; }
  else if (!online) { tone = c.warning; bg = c.warningSoft; text = waiting ? `Offline · ${waiting} sale${waiting === 1 ? "" : "s"} saved on this phone` : "Offline · you can keep selling"; }
  else if (syncing) { tone = c.info; bg = c.infoSoft; text = "Syncing…"; }
  else if (waiting > 0) { tone = c.warning; bg = c.warningSoft; text = lastSync?.message ?? `${waiting} sale${waiting === 1 ? "" : "s"} waiting to sync`; }
  return (
    <Pressable accessibilityRole="button" accessibilityLabel={`${text}. Tap to sync now`} onPress={() => void syncNow()} style={{ backgroundColor: bg, paddingHorizontal: space.lg, paddingVertical: 10, flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flex: 1 }}>
        <View style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: tone }} />
        <Text style={{ color: tone, fontWeight: "600", fontSize: 14, flexShrink: 1 }}>{text}</Text>
      </View>
      <Text style={{ color: tone, fontSize: 13, fontWeight: "700" }}>Sync</Text>
    </Pressable>
  );
}
