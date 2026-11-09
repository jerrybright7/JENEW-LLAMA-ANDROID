#!/usr/bin/env bash
set -euo pipefail

APK="android-smoke/app/build/outputs/apk/debug/app-debug.apk"
LOG="android-smoke/zrok-smoke.log"

test -s "$APK"
adb wait-for-device
adb shell getprop sys.boot_completed | grep -q 1
adb install -r "$APK"
adb logcat -c
adb shell am force-stop com.jenew.zroksmoke
adb shell am start -n com.jenew.zroksmoke/.MainActivity
sleep 8

adb logcat -d -s ZrokSmoke:I AndroidRuntime:E > "$LOG" || true
cat "$LOG"

if grep -q "ZROK_SMOKE_PASS:" "$LOG"; then
  echo "Android emulator smoke test passed."
  exit 0
fi

echo "Android emulator smoke test failed. Full logcat follows."
adb logcat -d > android-smoke/logcat-full.txt || true
cat android-smoke/logcat-full.txt
exit 1
