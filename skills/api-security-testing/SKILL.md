---
name: api-security-testing
description: 已有接口清单/流量/前端 JS 里的 API 时做接口安全测试调用——契约提取(api_contract)、双身份对照(auth_request)、响应差分(diff_responses)、越权/未授权/参数污染/限流/GraphQL 的验证路径。当意图涉及「测接口/越权/未授权访问/API」时使用。
tags:
  cwe: [CWE-639, CWE-862, CWE-306, CWE-284, CWE-915, CWE-770, CWE-352]
  attack: [T1190, T1078]
  class: [api-security, idor, broken-authz, contract, differential, graphql]
---

# API 安全测试（围绕平台接口工具）

## 平台工具怎么配合

| 工具 | 用途 |
|---|---|
| `api_contract` | 从前端 JS / 流量里提取接口契约（路径/方法/参数/是否带鉴权） |
| `auth_request` | 用**资产上已登记的凭据**组装带认证请求命令（只组命令，Bash 执行） |
| `diff_responses` | 对比两个响应的结构/字段差异（越权与信息泄露的判据） |
| `http_request` | 原生 HTTP 执行器，cookie jar 跨意图复用 |
| `session_login` | 建立 A/B 身份登录态 |
| `http_probe` | 批量探活/指纹，先筛可访问面 |
| `authz_record` / `authz_matrix` | 角色权限矩阵：建「角色×接口×期望/实测」基线并回填，缺口与越权线索可见（先查再派，避免重复测同一组合） |
| `fuzz_plan` | 生成模糊测试命令：`api` 契约驱动（schemathesis）、`param` 参数变异（ffuf）；只组命令，Bash 执行，流量照常留痕 |
| `listener_*` | OOB（SSRF/盲注） |

## 一、流程

1. **建契约清单**：`api_contract` 提取接口；没有前端时从流量/JS 手工补；
2. **分类打标**：公开 / 需登录 / 需特权 / 内部（看响应特征与 JS 调用上下文）；
3. **双身份对照**：
   - 身份 A（低权/无凭据）→ 身份 B（另一账号或已登记凭据）；
   - 同一资源分别请求，`diff_responses` 看数据是否相同/字段是否泄露；
4. **验证四类缺陷**：
   - 未授权访问：无 token 直接 200 + 业务数据；
   - 水平越权（IDOR）：A 的 token 读 B 的对象；
   - 垂直越权：低权账号打高权接口（返回 200 或仅前端隐藏）；
   - 参数污染/批量赋值：多传 `role/isAdmin/tenantId/userId` 看是否被采纳。
5. **系统化覆盖**：手工构造边界值收益低、参数位多时，用 `fuzz_plan` 生成契约驱动（`api`）
   或参数变异（`param`）命令，长跑用 `job_start`，异常样本落 `save_evidence`。

## 二、测试矩阵

| 维度 | 检查点 | 证据 |
|---|---|---|
| 认证 | 无 token / 过期 token / 伪造 token | 请求 + 响应 |
| 授权 | 水平/纵向/IDOR/租户隔离 | A/B 对照 |
| 参数 | 批量赋值、类型混淆、数组/对象注入 | 请求 + 字段变化 |
| 方法 | GET↔POST、PUT/PATCH/DELETE 未授权 | 方法与响应 |
| 版本 | `/v1` 旧版本未修、`/internal` 暴露 | 旧接口响应 |
| 分页 | `size=1000`、`page=0`、`fields=*` | 数据量 |
| 限流 | 登录/短信/导出是否无限流 | 少量计数 |
| 文件 | 上传类型/路径/大小校验 | 上传回执 |
| GraphQL | 内省、批量查询、深度/别名滥用 | 内省结果 |
| WebSocket | 握手鉴权、消息越权 | 消息记录 |

## 三、判定标准（写报告用）

- **L3 证据**：不同身份的响应体差异（含他人数据字段）、或未授权拿到业务数据；
- 仅返回 200 但无业务数据 → 不算越权，最多记为「接口存在」；
- 前端隐藏 ≠ 后端校验：必须直接调用接口确认；
- `diff_responses` 结果里指出具体差异字段（时间戳/ID/姓名等），不要只说「有差异」。

## 四、常见绕过

- 路径：`/api/v1/user/1` vs 尾斜杠、大小写、`//`、`%2e`、`;`；
- 方法：GET↔POST、`X-HTTP-Method-Override`、OPTIONS 探 Allow；
- 参数位置：query↔body↔header 三处都试（很多后端只校验一处）；
- 批量/分页：`size=1000`、`page=0`、`fields=*`、`expand=all` 拿全量；
- 签名/时间戳：参数排序、空值、时间窗重放（看服务端是否只查非空）；
- JSON 边界：重复键、`null`、数组代替对象、超长数字。

## 五、GraphQL / WebSocket

- GraphQL：先内省 `{__schema{types{name}}}`；再试批量/别名绕过限流、深层嵌套 DoS（谨慎）；
- WebSocket：握手是否校验 token；订阅/消息是否按用户隔离；用浏览器/脚本抓消息对照。

## 六、流量与证据

- 每个验证请求确认被 MITM 代理记录（HTTP 请求都会留流量 ID）；
- `report_finding` 绑定 baseline（正常请求）/proof（越权请求）两类流量；
- 敏感数据只取最小样本（一条记录），不批量拉取；
- 失败/否定结论写 `record_fact`，避免重复。

## 七、与侦察的分工

- 接口**发现**归 `api-recon`（前端路由/JS/参数）；本技能负责拿到清单后的**验证**；
- 发现新接口/新参数顺手 `record_fact`，供后续意图复用。
