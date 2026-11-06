#!/usr/bin/env bash
# Builds Rechvix.apk with the bare Android SDK tools (no Gradle, no network).
# Needs: JDK 17+, Android SDK build-tools + platforms/android-35.
#   ANDROID_HOME (default ~/mobile-tools/sdk)  JAVA_HOME (default ~/mobile-tools/jdk21)
# Signing: uses $RECHVIX_KEYSTORE (created on first run, kept OUT of the repo).
set -euo pipefail
cd "$(dirname "$0")"
SDK="${ANDROID_HOME:-$HOME/mobile-tools/sdk}"; export JAVA_HOME="${JAVA_HOME:-$HOME/mobile-tools/jdk21}"; export PATH="$JAVA_HOME/bin:$PATH"
BT="$(ls -d "$SDK"/build-tools/* | sort -V | tail -1)"; JAR="$SDK/platforms/android-35/android.jar"
KS="${RECHVIX_KEYSTORE:-$HOME/.rechvix-android.keystore}"; OUT=build; rm -rf $OUT; mkdir -p $OUT/{res,gen,cls}
[ -f "$KS" ] || keytool -genkeypair -keystore "$KS" -alias rechvix -keyalg RSA -keysize 2048 -validity 10000 \
  -storepass rechvix-android -keypass rechvix-android -dname "CN=Rechvix, O=NodeDR Infotech" >/dev/null 2>&1
"$BT/aapt2" compile --dir res -o $OUT/res.zip
"$BT/aapt2" link -I "$JAR" --manifest AndroidManifest.xml --java $OUT/gen -o $OUT/base.apk $OUT/res.zip --min-sdk-version 24 --target-sdk-version 34
javac --release 17 -classpath "$JAR" -d $OUT/cls $(find src $OUT/gen -name '*.java')
"$BT/d8" --release --lib "$JAR" --min-api 24 --output $OUT $(find $OUT/cls -name '*.class')
cp $OUT/base.apk $OUT/unsigned.apk; (cd $OUT && zip -q -u unsigned.apk classes.dex)
"$BT/zipalign" -f -p 4 $OUT/unsigned.apk $OUT/aligned.apk
"$BT/apksigner" sign --ks "$KS" --ks-pass pass:rechvix-android --key-pass pass:rechvix-android --out $OUT/Rechvix.apk $OUT/aligned.apk
"$BT/apksigner" verify --print-certs $OUT/Rechvix.apk | head -2; ls -la $OUT/Rechvix.apk
