# Rechvix mobile (iOS + Android)

One Expo / React Native codebase (TypeScript) for both phones. Offline-first: sell with no signal, sync when you're back.

## How it works

- **Connect once** to your self-hosted server (address is checked against `/health/ready`), then sign in. The session
  cookie is kept in the OS keychain/keystore (`expo-secure-store`) and sent as a header, so it also works over a plain-http shop LAN.
- **Local SQLite** (`expo-sqlite`) holds the catalogue (with price and stock), customers, recent invoices and the **outbox** of sales made on the phone.
- **Selling offline**: a sale is written to the outbox first (it can never be lost), then synced in the background.
  Totals shown offline are *before tax*; the server computes tax and the grand total.
- **Sync = replay through the normal sales API** (`src/sync/saga.ts`): create draft -> add lines -> finalise -> receipt.
  Finalising posts the ledger, moves stock and assigns the next invoice number, so only the server does it, exactly once.
  Every step is resumable: the phone can be killed or lose signal at any instant and a retry never duplicates lines,
  invoices or receipts (`test/saga.test.ts` crashes the connection after every step to prove it).
  Each draft is tagged `mobile:<id>` in the customer reference.
- **When it syncs**: on reconnect, when the app opens, after sign-in, every 45 s while sales are waiting, and on "Sync now".
- Sales the server refuses (e.g. insufficient stock) are kept as **Needs attention** with the server's message; retry or discard.
- If the session expires, sales stay queued; sign in again and they sync.

Not in this version: creating customers/items on the phone (do that on the web), returns, purchases, printing. Offline conflicts
only exist for sales, and those never conflict: every invoice is new and numbered by the server.

## Develop

```bash
npm install
npm test            # 20 unit tests: sync saga under crashes, engine, API client (real SQLite via node:sqlite)
npm run typecheck
RECHVIX_URL=http://localhost:8099 RECHVIX_EMAIL=... RECHVIX_PASSWORD=... npm run test:live   # same code vs a real server
npx expo start      # needs a dev build (expo-sqlite, camera); Expo Go is not enough
```

## Build

`android/` and `ios/` are generated (`expo prebuild`) and not committed.

```bash
# Android APK (JDK 17+, Android SDK)
npx expo prebuild --platform android && cd android && ./gradlew assembleRelease -PreactNativeArchitectures=arm64-v8a,x86_64
# sign with a keystore you keep (updates must use the same key):
apksigner sign --ks ~/.rechvix-android.keystore app/build/outputs/apk/release/app-release.apk

# iOS (macOS + Xcode only): see .github/workflows/mobile.yml (workflow_dispatch) for the exact steps
npx expo prebuild --platform ios && cd ios && pod install
```

The Android `applicationId` is `in.nodedr.rechvix`, the same as the earlier web-shell APK, so it installs over it when signed with the same key.
iOS bundle id: `in.nodedr.rechvix`. A real iPhone build needs your Apple Developer team and signing; the workflow builds for the simulator.
