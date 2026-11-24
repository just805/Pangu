#!/usr/bin/env bash
# 探测并拉取微信小程序 .wxapkg（Android，需 adb；root 权限成功率更高）。
# 用法：collect_android.sh [输出目录]
set -u

OUT="${1:-./wxapkg-pull}"
mkdir -p "$OUT" || { echo "无法创建输出目录: $OUT"; exit 1; }

if ! command -v adb >/dev/null 2>&1; then
  echo "未找到 adb，请先安装 Android platform-tools"
  exit 1
fi
if ! adb get-state >/dev/null 2>&1; then
  echo "无设备或未授权 adb（请先 adb devices / 在手机上允许调试）"
  exit 1
fi

# 主路径：/data/data/com.tencent.mm/MicroMsg/<hash>/appbrand/pkg/
# 优先 root，失败再试普通权限（模拟器/调试包可能可读）。
list_pkgs() {
  adb shell "su -c 'ls /data/data/com.tencent.mm/MicroMsg/*/appbrand/pkg/*.wxapkg 2>/dev/null'" 2>/dev/null
  adb shell "ls /data/data/com.tencent.mm/MicroMsg/*/appbrand/pkg/*.wxapkg 2>/dev/null" 2>/dev/null
}

PKGS=$(list_pkgs | tr -d '\r' | sort -u)
if [ -z "$PKGS" ]; then
  echo "未找到 .wxapkg。可能原因："
  echo "  - 微信尚未打开过目标小程序（先打开一次再试）"
  echo "  - 当前微信版本路径变化或包已加密"
  echo "  - 设备未 root 且目录不可读"
  exit 1
fi

n=0
for p in $PKGS; do
  n=$((n+1))
  dest="$OUT/$(basename "$p")"
  if adb pull "$p" "$dest" >/dev/null 2>&1; then
    echo "已拉取: $p -> $dest"
  else
    echo "拉取失败（可能权限不足）: $p"
  fi
done

echo "共发现 $n 个包，输出目录: $OUT"
find "$OUT" -name '*.wxapkg' -print
