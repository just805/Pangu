---
name: wechat-miniprogram
description: 目标包含微信小程序（wxapkg 包、小程序后端 API、云开发 CloudBase、微信开放能力）时调用——取包解包、抓包、登录态复用、云开发与开放能力测试、43 类小程序漏洞排查、检查清单与专项报告。也适用于「这个小程序有没有漏洞」类任务。
tags:
  cwe: [CWE-200, CWE-306, CWE-639, CWE-862, CWE-798, CWE-89, CWE-918, CWE-434, CWE-345, CWE-311]
  attack: [T1190, T1595, T1552, T1078]
  class: [wechat, miniprogram, wxapkg, cloudbase, mobile-api, src, methodology]
---

# 微信小程序渗透方法论（Pangu 适配版）

本技能是**场景入口**：`rules/` 放漏洞分类、清单、云开发/开放能力与报告格式，
`references/` 放取包抓包、登录态、案例与自动化细节。**按需打开，禁止通读**。
路径：`${SKILL_DIR}/rules/<名>.md`、`${SKILL_DIR}/references/<名>.md`。

## 与本平台的衔接（先读这段）

| 阶段 | 用平台能力 |
|---|---|
| 取包（Android） | Bash 跑 `${SKILL_DIR}/scripts/collect_android.sh`（adb 探测 + pull） |
| 取包（PC 微信） | PowerShell 跑 `${SKILL_DIR}/scripts/collect_windows.ps1`；或从 `WeChat Files/Applet` 手动复制 |
| 解包 | 原生工具 **`miniprogram_unpack`**（标准 v1 明文/XOR）；不支持变体用 wxappUnpacker/unveilr |
| 静态审计 | 原生工具 **`miniprogram_audit`**：接口、硬编码密钥、云环境/云函数、appid、sourcemap、openid 等 |
| 抓包 | 平台 MITM 记录代理（Bash 子进程自动走 `HTTP_PROXY`）+ `http_request`；两者流量都留痕 |
| 登录态 | `session_login`（任务级 cookie jar，跨意图复用）/ `auth_request`（用已登记凭据组请求） |
| 云开发 / 开放能力 | `http_request` + `diff_responses`（A/B 身份对照）+ `api_contract` |
| 回连/OOB | `listener_start` + `listener_sessions`（SSRF/盲注） |
| 事实/漏洞/资产 | `record_fact`（负结论也落）/ `report_finding`（critical/high 必须机械证据）/ `insert_assets` |
| 证据落盘 | `save_evidence`（截图/样本/日志，报告用 `![说明](artifact:<id>)` 引用） |
| 报告 | `rules/report-format.md`，与平台 Word/导出管线兼容 |
| 已知组件 CVE | `search_vulns` / `get_poc` / `scan_with_nuclei` |

## 工作流（严格按序，别跳步）

1. **授权与边界**：确认小程序主体、授权范围（哪些接口/域名/云环境）、禁止项（不刷短信、不批量拉用户数据、不投毒生产云数据库）。范围外只记录不测试。
2. **取包**：Android（root/备份/adb）或 PC 缓存；拿到 `.wxapkg` 后先落 `save_evidence` 留档。
3. **解包 + 静态审计**：`miniprogram_unpack` → `miniprogram_audit`。产出的接口清单、密钥、云环境是后续测试的靶标。
4. **抓包与接口盘点**：配置 MITM 代理（证书装到测试机），跑一遍小程序核心链路；用 `http_request` 复放关键请求，核对静态清单与真实流量差异。
5. **登录态与身份矩阵**：用 `session_login` 建立 A 身份；再建 B 身份（或未登录/低权限）做对照——越权类漏洞全靠这个矩阵。
6. **接口漏洞测试**：按 `rules/vuln-taxonomy-43.md` 的优先级（未授权/越权/IDOR → 注入 → 业务逻辑 → 云开发 → 开放能力）逐类验证；每条最小样本留证。
7. **云开发专项**：按 `rules/cloud-open-capabilities.md` 测云函数、云数据库、云存储、环境隔离。
8. **证据与报告**：`report_finding` 写清复现命令/请求响应/影响；按 `rules/report-format.md` 出专项章节。

## 优先级（时间有限时）

1. 硬编码 AppSecret / 云环境 ID（可直接换 openid、直连云数据库）；
2. 未授权 / 水平越权 / IDOR（openid、userId、订单号换一个）；
3. 云函数未授权调用 / 云数据库未授权读写；
4. 支付回调伪造 / 金额篡改；
5. 注入 / 上传 / SSRF。

## 边界（必须遵守）

- 只在授权范围内测试；不破坏业务可用性、不批量拉取真实用户数据、不删改生产数据。
- 手机号/身份证等敏感数据验证到「能读到非本人一条」即止，不做批量导出。
- 短信/验证码只做少量验证，禁止轰炸式压测。
- 云开发测试优先用测试环境/测试账号；生产环境只做只读验证。
- 所有出网验证用平台回连器留证，证据绑定流量 ID。

## 索引

- `rules/vuln-taxonomy-43.md` —— 43 类漏洞逐条判据、验证、证据、严重度、CWE
- `rules/checklist.md` —— 从取包到报告的检查清单（可勾选）
- `rules/cloud-open-capabilities.md` —— 云开发与微信开放能力专项
- `rules/report-format.md` —— 小程序专项报告结构
- `references/unpack-and-capture.md` —— 取包路径、解包、代理抓包、SSL pinning
- `references/login-state.md` —— code2session、openid/unionid、token、手机号解密、多身份
- `references/cases.md` —— 典型脱敏案例
- `references/automation.md` —— 批量接口测试与证据留痕模板
