import { CameraView, useCameraPermissions } from "expo-camera";
import React, { useRef } from "react";
import { Linking, Modal, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { Body, Button } from "../ui/kit";

/** Full-screen barcode scanner. Calls onCode once per distinct scan and stays open so a cashier can scan item after item. */
export function Scanner({ visible, onCode, onClose, hint }: { visible: boolean; onCode: (code: string) => void; onClose: () => void; hint?: string | null }) {
  const [perm, ask] = useCameraPermissions();
  const last = useRef<{ code: string; at: number } | null>(null);
  return (
    <Modal visible={visible} animationType="slide" onRequestClose={onClose} presentationStyle="fullScreen">
      <SafeAreaView style={{ flex: 1, backgroundColor: "#000" }}>
        {!perm ? null : !perm.granted ? (
          <View style={{ flex: 1, padding: 24, justifyContent: "center", gap: 16 }}>
            <Body style={{ color: "#fff" }}>Rechvix needs the camera to scan barcodes.</Body>
            {perm.canAskAgain ? <Button title="Allow camera" onPress={() => void ask()} /> : <Button title="Open settings" onPress={() => void Linking.openSettings()} />}
            <Button title="Close" kind="quiet" onPress={onClose} />
          </View>
        ) : (
          <View style={{ flex: 1 }}>
            <CameraView
              style={{ flex: 1 }} facing="back"
              barcodeScannerSettings={{ barcodeTypes: ["ean13", "ean8", "upc_a", "upc_e", "code128", "code39", "qr"] }}
              onBarcodeScanned={({ data }) => {
                const now = Date.now();
                if (last.current && last.current.code === data && now - last.current.at < 2500) return;
                last.current = { code: data, at: now };
                onCode(data);
              }}
            />
            <View style={{ position: "absolute", left: 16, right: 16, bottom: 24, gap: 12 }}>
              {hint ? <View style={{ backgroundColor: "rgba(0,0,0,0.7)", padding: 12, borderRadius: 10 }}><Body style={{ color: "#fff" }}>{hint}</Body></View> : null}
              <Button title="Done" onPress={onClose} />
            </View>
          </View>
        )}
      </SafeAreaView>
    </Modal>
  );
}
