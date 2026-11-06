import React from "react";
import { View } from "react-native";
import { ago } from "../lib/hooks";
import { useApp } from "../state/app";
import { space, usePalette } from "../theme";
import { Icon, Tap, type IconName } from "./kit";
import { T } from "./text";

/** One honest line about where the data is. Standalone phones say so, and offer the server link without nagging. */
export function SyncBanner() {
  const { linked, online, syncing, counts, lastSyncAt, syncNow, lastSync, startLinking } = useApp();
  const c = usePalette();
  const waiting = counts.pending + counts.syncing;
  let tone = c.positive, bg = c.positiveSoft, text = `All synced · ${ago(lastSyncAt)}`, icon: IconName = "checkmark-circle", action = "Sync";
  let onPress: () => void = () => void syncNow();

  if (!linked) {
    tone = c.muted; bg = c.surfaceAlt; icon = "phone-portrait-outline"; action = "Connect";
    text = "Working on this phone";
    onPress = startLinking;
  } else if (counts.failed > 0) { tone = c.negative; bg = c.negativeSoft; icon = "alert-circle"; text = `${counts.failed} sale${counts.failed === 1 ? "" : "s"} need${counts.failed === 1 ? "s" : ""} attention`; }
  else if (!online) { tone = c.warning; bg = c.warningSoft; icon = "cloud-offline-outline"; text = waiting ? `Offline · ${waiting} sale${waiting === 1 ? "" : "s"} saved on this phone` : "Offline · you can keep selling"; }
  else if (syncing) { tone = c.info; bg = c.infoSoft; icon = "sync"; text = "Syncing…"; }
  else if (waiting > 0) { tone = c.warning; bg = c.warningSoft; icon = "time-outline"; text = lastSync?.message ?? `${waiting} sale${waiting === 1 ? "" : "s"} waiting to sync`; }

  return (
    <Tap label={`${text}. ${action}`} onPress={onPress} haptic={false} style={{ backgroundColor: bg, paddingHorizontal: space.lg, paddingVertical: 9, flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flex: 1 }}>
        <Icon name={icon} size={16} color={tone} />
        <T v="small" color={tone} style={{ fontFamily: "IBMPlexSans_600SemiBold", flexShrink: 1 }}>{text}</T>
      </View>
      <T v="small" color={tone} style={{ fontFamily: "IBMPlexSans_600SemiBold" }}>{action}</T>
    </Tap>
  );
}
