import { useEffect, useRef, useState, type ComponentRef } from "react";
import { Keyboard, Platform, View } from "react-native";

/** How far to lift a bar that sits at the bottom of a screen so the on-screen keyboard never covers it.
 *
 *  Attach `anchor` to a zero-height, absolutely positioned view at the bottom of the screen. On every keyboard event the anchor's
 *  position in the window is compared with the top of the keyboard, so it is right whether Android resizes the window for the
 *  keyboard (then nothing is covered and the lift is 0) or draws the keyboard over the app (then the bar is lifted by exactly the
 *  overlap). It works the same under any tab bar or safe-area padding because it measures, rather than assumes. */
export function useKeyboardLift() {
  const anchor = useRef<ComponentRef<typeof View>>(null);
  const [lift, setLift] = useState(0);
  useEffect(() => {
    const show = Keyboard.addListener(Platform.OS === "ios" ? "keyboardWillChangeFrame" : "keyboardDidShow", (e) => {
      const top = e.endCoordinates.screenY;
      anchor.current?.measureInWindow((_x, y) => setLift(Math.max(0, Math.round(y - top))));
    });
    const hide = Keyboard.addListener(Platform.OS === "ios" ? "keyboardWillHide" : "keyboardDidHide", () => setLift(0));
    return () => { show.remove(); hide.remove(); };
  }, []);
  return { anchor, lift };
}
