---
name: binary-mobile-reversing
description: 目标有 APP(APK/IPA)、桌面客户端或需要逆向二进制找接口/密钥/漏洞时调用——静态分析、Jadx/apktool/frida、移动端常见漏洞（硬编码密钥、导出组件、证书校验、WebView）、以及从逆向结果回到服务端测试的完整路径。
tags:
  cwe: [CWE-798, CWE-312, CWE-926, CWE-295, CWE-489, CWE-532]
  attack: [T1409, T1552.001, T1512]
  class: [mobile, reverse, apk, ipa, binary, electron]
---

# 移动端 / 二进制逆向速查

> APK 深挖另见 `apk-reverse`；本技能负责「跨客户端通用方法 + 回到服务端」。

## 与本平台的衔接

| 场景 | 用平台能力 |
|---|---|
| 反编译/反汇编 | Bash 跑 `apktool`/`jadx`/`frida`/`ghidra`（宿主环境页按需装） |
| 抽接口/密钥 | 解包目录交给 `miniprogram_audit`（通用文本扫描）或 `js_audit`（前端 bundle） |
| 接口验证 | `http_request` + `session_login` + `auth_request` + `diff_responses` |
| 组件/依赖 CVE | `search_vulns` / `get_poc` |
| 证据 | `save_evidence`（截图、反编译片段、Frida 输出）；`report_finding` |
| 新域名/接口 | `insert_assets` / `record_fact` |

## 一、APK 静态分析

```bash
apktool d app.apk -o out/                 # 资源 + AndroidManifest.xml
jadx -d src/ app.apk                      # Java 源码
aapt dump badging app.apk                 # 包名/版本/权限/入口
apksigner verify --print-certs app.apk    # 签名信息
```

### 优先看

1. `AndroidManifest.xml`：`exported=true` 组件、权限、deep-link、`allowBackup`、`debuggable`、`networkSecurityConfig`；
2. 秘密（全文写 `record_fact`）：
   ```bash
   grep -rnE "https?://|api[_-]?key|secret|token|password|AKID|accessKey|-----BEGIN" src/ | head -100
   gitleaks detect --source src/ 2>/dev/null
   ```
3. 接口：okhttp/retrofit 注解、`/api/` 常量、签名/加密参数构造逻辑；
4. 导出组件：
   ```bash
   adb shell dumpsys package <pkg> | grep -A2 exported
   adb shell am start -n pkg/.Activity --es key value
   ```

## 二、IPA / 桌面端

- IPA：`unzip app.ipa`；Mach-O 用 `strings -n8`/`otool -L`；`class-dump` 看头文件；
- Flutter：找 `libapp.so`/`flutter_assets`，`strings` 抽接口；
- Electron：`npx asar extract app.asar out/` → 看 `preload.js`、IPC、`nodeIntegration`；
- 原生二进制：`radare2`/`ghidra`/`objdump`，定位鉴权与签名函数。

## 三、动态与网络

- 抓包：平台 MITM 代理 + CA 证书；客户端可能做 SSL pinning。
- 证书校验绕过（仅自有测试包）：
  ```bash
  frida -U -f <pkg> -l ssl_bypass.js
  objection -g <pkg> explore --startup-command "android sslpinning disable"
  ```
- 账号/设备标识：`deviceId`、`oaid`、`androidId` 常参与签名，改动会影响请求。

## 四、移动端常见漏洞

| 漏洞 | 验证 |
|---|---|
| 硬编码密钥 | 提取 AK/SK/token → 直接调 API，能列数据/调接口即 L3 |
| 未授权/越权 API | 逆向得接口 → 无 token/换身份调 |
| 导出组件 | `am start`/`am broadcast` 传参数，看是否拉起并生效 |
| 证书校验缺失 | 仅自有测试包做 MITM 证明 |
| WebView 注入 | deep-link 参数进入 `loadUrl`/`addJavascriptInterface` → 构造 URL |
| 备份/调试 | `allowBackup=true` → `adb backup`；`debuggable=true` |
| 日志泄露 | `logcat` 抓 token/手机号/密码 |
| 本地存储明文 | `adb shell run-as <pkg> ls` 看 SharedPreferences/SQLite |

## 五、从客户端回到服务端

- 逆向得到的接口/参数/签名算法是**测试服务端的钥匙**：
  用脚本还原签名 → 直接打 API 做越权/注入（比在 APP 里点快得多）；
- 客户端校验（签名/加密）通常服务端不校验——重点验证服务端是否真的校验；
- 接口清单落 `record_fact`，交给 `api-security-testing` 继续；
- 用 `diff_responses` 做 A/B 身份对照。

## 六、证据与验证纪律

- 逆向结论附：文件/类/行号、字符串片段、实际请求响应；
- 客户端校验绕过只在自有测试包做；生产目标只做「能读到流量」的最小证明；
- 密钥/凭据脱敏后再进报告。

## 七、边界

- 只逆授权目标的包；不破解付费/版权保护用于非测试目的；
- 不修改/重打包生产 APP 用于攻击；
- 不导出设备上的真实用户数据。
