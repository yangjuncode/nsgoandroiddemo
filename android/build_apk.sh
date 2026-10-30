#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
ANDROID_DIR="$ROOT_DIR/android"
export ANDROID_HOME="${ANDROID_HOME:-/home/yj/Android/Sdk}"

BUILD_TOOLS="$ANDROID_HOME/build-tools/36.0.0"
PLATFORM_JAR="$ANDROID_HOME/platforms/android-34/android.jar"
TARGET_ARCH="${1:-android_x64}"

echo "=== [1/5] 使用官方 nsgo build -embed 生成 .aar 组件包 ($TARGET_ARCH) ==="
cd "$ROOT_DIR"
go tool nsgo build -embed -target "$TARGET_ARCH"

AAR_FILE="$ROOT_DIR/dist/NSGoServiceDemo-0.2.0-${TARGET_ARCH}/NSGoServiceDemo.aar"
if [[ ! -f "$AAR_FILE" ]]; then
    echo "Error: AAR file not found at $AAR_FILE" >&2
    exit 1
fi

WORK_DIR="$(mktemp -d /tmp/nsgo-host-apk-XXXXXX)"
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$ANDROID_DIR/res/mipmap-xxxhdpi"
cp -f "$ROOT_DIR/assets/Icon.png" "$ANDROID_DIR/res/mipmap-xxxhdpi/ic_launcher.png"

echo "=== [2/5] 解包 AAR 并使用 aapt2 编译外层 AndroidManifest.xml 与图标资源 ==="
mkdir -p "$WORK_DIR/aar" "$WORK_DIR/res_compiled" "$WORK_DIR/gen" "$WORK_DIR/classes" "$WORK_DIR/apk_root"
unzip -q "$AAR_FILE" -d "$WORK_DIR/aar"

"$BUILD_TOOLS/aapt2" compile --dir "$ANDROID_DIR/res" -o "$WORK_DIR/res_compiled/res.zip"
"$BUILD_TOOLS/aapt2" link \
    -I "$PLATFORM_JAR" \
    --manifest "$ANDROID_DIR/AndroidManifest.xml" \
    --java "$WORK_DIR/gen" \
    -o "$WORK_DIR/unaligned.apk" \
    "$WORK_DIR/res_compiled/res.zip"

echo "=== [3/5] 编译外层 Android 宿主工程 (MainActivity & ForegroundGoService) ==="
javac -source 11 -target 11 -encoding UTF-8 \
    -classpath "$PLATFORM_JAR:$WORK_DIR/aar/classes.jar" \
    -d "$WORK_DIR/classes" \
    $(find "$WORK_DIR/gen" "$ANDROID_DIR/src" -name "*.java")

echo "=== [4/5] 使用 d8 合并宿主类与 AAR classes.jar 生成 classes.dex ==="
jar cf "$WORK_DIR/host.jar" -C "$WORK_DIR/classes" .
"$BUILD_TOOLS/d8" \
    --min-api 28 \
    --lib "$PLATFORM_JAR" \
    --output "$WORK_DIR/apk_root" \
    "$WORK_DIR/host.jar" \
    "$WORK_DIR/aar/classes.jar"

echo "=== [5/5] 组装 Native 库 (libnsgo.so) 与 Assets 并使用 apksigner 签名 ==="
if [[ -d "$WORK_DIR/aar/jni" ]]; then
    mkdir -p "$WORK_DIR/apk_root/lib"
    cp -r "$WORK_DIR/aar/jni/"* "$WORK_DIR/apk_root/lib/"
fi
if [[ -d "$WORK_DIR/aar/assets" ]]; then
    mkdir -p "$WORK_DIR/apk_root/assets"
    cp -r "$WORK_DIR/aar/assets/"* "$WORK_DIR/apk_root/assets/"
fi

(
    cd "$WORK_DIR/apk_root"
    zip -q -r "$WORK_DIR/unaligned.apk" classes.dex
    if [[ -d assets ]]; then
        zip -q -r "$WORK_DIR/unaligned.apk" assets
    fi
    if [[ -d lib ]]; then
        zip -q -0 -r "$WORK_DIR/unaligned.apk" lib
    fi
)

OUT_APK="$ROOT_DIR/dist/NSGoServiceDemo-${TARGET_ARCH}.apk"
"$BUILD_TOOLS/zipalign" -f -p 4 "$WORK_DIR/unaligned.apk" "$OUT_APK"

KEYSTORE="$ANDROID_DIR/debug.keystore"
if [[ ! -f "$KEYSTORE" ]]; then
    keytool -genkeypair -v \
        -keystore "$KEYSTORE" \
        -storepass android \
        -keypass android \
        -alias androiddebugkey \
        -keyalg RSA -keysize 2048 -validity 10000 \
        -dname "CN=Android Debug,O=Android,C=US" >/dev/null 2>&1
fi

"$BUILD_TOOLS/apksigner" sign \
    --ks "$KEYSTORE" \
    --ks-pass pass:android \
    --key-pass pass:android \
    "$OUT_APK"

echo "✅ 外层 Android 宿主 APK 构建成功: $OUT_APK"
