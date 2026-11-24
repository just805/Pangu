# 从 PC 微信缓存目录收集小程序 .wxapkg（Windows）。
# 用法：powershell -File collect_windows.ps1 -Out .\wxapkg-pull
param(
    [string]$Out = ".\wxapkg-pull"
)

New-Item -ItemType Directory -Force -Path $Out | Out-Null

$roots = @(
    (Join-Path $env:USERPROFILE "Documents\WeChat Files"),
    (Join-Path $env:USERPROFILE "Documents\xwechat_files"),
    (Join-Path $env:USERPROFILE "AppData\Roaming\Tencent\WeChat")
)

$found = @()
foreach ($root in $roots) {
    if (Test-Path $root) {
        $found += Get-ChildItem -Path $root -Recurse -Filter *.wxapkg -File -ErrorAction SilentlyContinue
    }
}

if ($found.Count -eq 0) {
    Write-Host "未找到 .wxapkg。请确认：微信已打开过目标小程序；或在 WeChat Files\Applet 下手动查找。"
    exit 1
}

$i = 0
foreach ($f in $found) {
    $i++
    $dest = Join-Path $Out ("{0}-{1}" -f $i, $f.Name)
    Copy-Item $f.FullName $dest -Force
    Write-Host ("{0} -> {1}" -f $f.FullName, $dest)
}

Write-Host ("已复制 {0} 个包到 {1}" -f $i, $Out)
