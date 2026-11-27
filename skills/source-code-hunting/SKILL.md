---
name: source-code-hunting
description: 拿到目标源码/仓库（白盒或泄露源码）做代码审计时调用——危险 sink 定位、框架模式识别、git 历史挖掘、依赖 CVE、semgrep/gitleaks 用法与从代码到可利用性的验证路径。
tags:
  cwe: [CWE-89, CWE-78, CWE-502, CWE-22, CWE-798, CWE-287, CWE-918, CWE-1336]
  attack: [T1552.001, T1213, T1195]
  class: [whitebox, code-audit, sast, secrets, supply-chain]
---

# 源码审计方法论

## 与本平台的衔接

| 场景 | 用平台能力 |
|---|---|
| 源码登记 | `code_asset_register`（仓库/源码包 → 归属企业，返回 `code_asset_id`；**只在授权范围内登记**） |
| 静态扫描 | `code_scan_plan` 生成 semgrep/gitleaks/trivy 命令 → Bash 执行 → `code_scan_ingest` 把 JSON 入库。结果进图谱后规划者能看到「这个仓库有多少处 sink」，不再看完就丢 |
| 动态 → 源码反查 | `code_asset_context`（给运行时 URL/接口名，反查后端 handler 与 sink 位置、用了什么 ORM、有没有参数化——这是白盒能直接回答、黑盒只能猜的问题） |
| 依赖 CVE | `code_scan_plan` 的 trivy 一路（组件+版本进 `code_scan_results`）+ `search_vulns` |
| 代码级验证 | 起本地服务或 `http_request` 打授权目标；`session_login` 建身份 |
| 越权验证 | `auth_request` + `diff_responses`（A/B 对照） |
| 证据 | `save_evidence`（代码片段、请求响应、行号）；`report_finding` |
| 候选点记录 | 每点 `record_fact`：文件:行号、sink、输入来源、可达性、验证状态 |

> **静态线索不是漏洞**：`code_scan_ingest` 入库的是「可疑位置」，
> 报告里单列「静态扫描线索」并与已确认漏洞分开；要 `report_finding` 仍必须有动态证据。
> gitleaks 的命中值在入库时已脱敏（只留前 4 位 + 长度），复核时按行号回原仓库看。

## 一、先做三件事（比逐行读快 10 倍）

1. **看仓库全貌**：语言/框架/入口/路由表/依赖清单（pom.xml/package.json/go.mod/requirements.txt）；
2. **找秘密**：
   ```bash
   gitleaks detect --source . -v
   semgrep --config p/secrets .
   trufflehog filesystem . --only-verified
   ```
3. **git 历史**：
   ```bash
   git log --oneline --all | head -50
   git log -p -- .env
   git log -S "password" --oneline
   ```
   删除过又提交的密钥仍在历史里。

## 二、sink 驱动审计（从危险函数反向找输入）

| 类型 | 语言 | sink 关键词 |
|---|---|---|
| SQLi | Java/PHP/Py/Go | `Statement.execute*`、`${}`拼接、`db.Raw`、`cursor.execute(f"` |
| 命令注入 | 全部 | `Runtime.exec`、`ProcessBuilder`、`os.system`、`subprocess(...,shell=True)`、`exec.Command("sh","-c"` |
| 反序列化 | Java/PHP | `ObjectInputStream`、`readObject`、`JSON.parse*`、`unserialize`、XStream |
| SSRF | 全部 | `http.Get(req.Param`、`requests.get(`、`urllib`、`new URL(input)` |
| 文件 | 全部 | `../`拼接、`os.path.join(base,user)`、`FileInputStream(user)`、`sendFile` |
| 模板注入 | Java/Py | `Template.process`、`render_template_string`、`new Template(input)` |
| 越权 | 全部 | 直接用请求 id 查库（无 owner 校验）、`findById(param)` 后不比对当前用户 |
| XXE | Java/Py | `DocumentBuilderFactory`/`etree.parse` 未禁外部实体 |
| 开放重定向 | 全部 | `redirect(param)`、`Location: <input>` |

## 三、框架模式（国内常见）

- 若依 RuoYi：`@PreAuthorize` 缺失、`/common/download?fileName=`（穿越）、`gen` 模块 SQLi；
- Spring Boot：`@RequestMapping` 通配、Actuator 暴露、SpEL 注入（`#{...}`）；
- ThinkPHP：`input()` 进 `whereRaw`、`invokeFunction`、多语言 `lang` LFI；
- JeecgBoot：`/jmreport/*`、Freemarker 模板注入、`queryTableData` SQL；
- Laravel：`unserialize`、`.env` 暴露、Debugbar；
- Django：`raw()`/`extra()`、`mark_safe`、`DEBUG=True`；
- Shiro：`rememberMe` 反序列化（默认 key）。

## 四、从代码到可利用性

1. 确认入口可达（路由/鉴权过滤器是否拦）；
2. 确认参数可控（谁传进来、有没有校验）；
3. 本地起服务验证，或构造与线上一致的请求（`http_request`）；
4. 代码级证据 + 实际回显 = 高可信；只有代码推断 → 最多 medium 并注明「代码审计，未验证」。

## 五、semgrep / 依赖

```bash
semgrep --config auto . --severity ERROR
mvn dependency:tree | grep -Ei "fastjson|log4j|shiro"
npm audit --production
go list -m all
```

- 依赖命中同样要行为验证（版本匹配 ≠ 漏洞）；
- 用 `search_vulns` 对照 CVE 影响区间。

## 六、证据与验证纪律

- 每个候选点写 fact：文件:行号、sink、输入来源、是否可达、验证状态；
- 确认后 `report_finding` 的 evidence 附代码片段 + 实际请求响应；
- 修复建议具体到函数级（参数化/白名单/编码）；
- 未验证的代码推断不报 high/critical。

## 七、边界

- 只审计授权仓库；泄露源码只用于授权评估，不扩散；
- 不利用漏洞修改/删除生产数据；
- 密钥/凭据脱敏后再进报告。
