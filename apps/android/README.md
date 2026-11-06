# Rechvix for Android

A native shell around a self-hosted Rechvix server. First launch asks for the
server address once (checks `/health/ready`), then opens straight into the
app. Adds what a browser tab can't: app icon, camera and file access for bill
scanning and imports, downloads, and tel:/mailto:/WhatsApp hand-off.

Build (no Gradle, no network): `./build.sh` → `build/Rechvix.apk`.
Needs a JDK 17+ and the Android SDK (`build-tools`, `platforms/android-35`);
paths default to `~/mobile-tools`, override with `ANDROID_HOME` / `JAVA_HOME`.
The signing key is created outside the repo (`~/.rechvix-android.keystore`,
override with `RECHVIX_KEYSTORE`); keep it, updates must be signed with the same key.

Test on the emulator: `adb install -r build/Rechvix.apk`, server `10.0.2.2:<port>`.
Plain `http://` is allowed (shop LAN installs have no certificate); the connect
screen defaults to https except for private addresses.
