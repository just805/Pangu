# Tor / I2P 隐藏服务渗透支持 — 实施计划

## Context

需求（来自 `新能能力.md#L1-7`）：在授权前提下支持对 `.onion`（Tor）与 `.i2p`（I2P）
后 Web 应用做探测/爬取/漏洞检测；支持配置代理/网关；针对高延迟提供超时/重试/并发；
提供配置说明与示例。

现状与缺口（已勘察）：
- 全局出口 `global_proxy` 支持 `http/https/socks5`，但 **agent 侧 HTTP 客户端与自定义
  HTTP 工具只用 `http.ProxyURL`**（[agent/tools_http.go#L323-L333](file:///d:/xm-v0.1.0/Pangu/agent/tools_http.go#L323-L333)、
  [server/customtool.go#L474-L510](file:///d:/xm-v0.1.0/Pangu/server/customtool.go#L474-L510)），
  不认 socks5 —— Tor 默认就是 SOCKS5，直接填会报 `unsupported protocol scheme`。
- 录制 MITM 的上游钩子按请求选代理且支持 socks5
  （[traffic/traffic.go#L233-L241](file:///d:/xm-v0.1.0/Pangu/traffic/traffic.go#L233-L241)），
  适合做「按域名后缀路由」，但目前只认全局/代理池。
- `proxyprobe.ClientFor` 已有 socks5/socks5h 正确实现（[proxyprobe/proxyprobe.go#L98-L144](file:///d:/xm-v0.1.0/Pangu/proxyprobe/proxyprobe.go#L98-L144)），
  但未导出可复用的 dialer/transport 配置。
- `http_probe` 是生成 curl 脚本，`--max-time 10`、无重试
  （[agent/tools_probe.go#L35-L73](file:///d:/xm-v0.1.0/Pangu/agent/tools_probe.go#L35-L73)），
  对隐藏服务延迟不够。
- `ToolSet.SetEgressProxy` 已定义但**没有调用点**，`http_request/session_login` 实际只走
  进程环境代理（与注释「与 Bash 的 HTTP_PROXY 同源」不符）——本次一并接上。

目标：按「域名后缀自动路由」——`.onion → Tor 代理`、`.i2p → I2P 代理`、其余走既有
全局/MITM 出口；捕获开时经 MITM 上游路由（流量仍留痕），捕获关时由 agent 客户端直连
对应代理。

---

## 1. 代理路由底座：`proxyprobe`

新增（复用现有 socks 实现，不引新依赖）：
- `type HiddenProxies struct { Tor, I2P string }`
- `func IsHiddenHost(host string) string` → `"tor"` / `"i2p"` / `""`（后缀匹配，
  `example.onion`、`x.y.i2p`，大小写不敏感，去端口）。
- `func ConfigureTransport(tr *http.Transport, platform string, hs HiddenProxies) error`：
  - `tr.Proxy` 按请求 host 返回 HTTP 型代理（Tor/I2P/平台），socks 型返回 nil；
  - `tr.DialContext` 对 socks 型代理的目标走 `newSocksDialer`，其余直连（含拨号到 HTTP
    代理自身）；
  - `socks5h` 归一化为 `socks5`（`x/net/proxy` 已把域名交给代理侧解析）。
- 导出 `func SocksDialContext(proxyURL string) (func(ctx, network, addr) (net.Conn, error), error)`
  供 traffic/其它客户端复用。
- 单测：host 匹配、`ConfigureTransport` 对 `.onion/.i2p/普通域` 的 `Proxy(req)` 返回、
  socks dialer 构造成功、`socks5h` 归一化。

## 2. 设置与管理：`server/manager.go`

新增设置键（默认值）：
- `hidden_service_enabled`（默认 `true`；只影响 .onion/.i2p，不改变普通流量）
- `tor_proxy`（默认 `socks5h://127.0.0.1:9050`）
- `i2p_proxy`（默认 `http://127.0.0.1:4444`；用户可改 socks5://127.0.0.1:4447）
- `hidden_service_timeout_seconds`（默认 90）
- `hidden_service_retries`（默认 2）
- `hidden_service_connect_timeout_seconds`（默认 30）

改动：
- `Manager` 字段 + 启动加载 + `HiddenServiceConfig()`（返回 enabled/proxies/timeout/retries）
  + `SetHiddenServiceConfig(...)`（校验代理 URL、写 settings、热更新 traffic）。
- 校验复用 `proxyprobe.Validate`；`tor_proxy` 允许 `socks5/socks5h`。
- 启动时把 Tor/I2P 代理下发给 traffic（见第 3 节）。

## 3. 录制 MITM：`traffic/traffic.go`

- 新增 `SetHiddenServiceUpstream(tor, i2p string) error`（原子存储，空串清除）。
- 在现有 upstream 钩子里，**隐藏后缀优先于代理池与全局**：
  ```
  if IsHiddenHost(req.Host) == "tor" && tor != nil { return tor }
  if IsHiddenHost(req.Host) == "i2p" && i2p != nil { return i2p }
  ... 现有 pool/global 逻辑
  ```
- 单测：给定 req.Host 为 `.onion/.i2p/普通`，钩子返回对应代理；未配置时回落原逻辑。

## 4. Agent HTTP 客户端与探活

### 4.1 `agent/tools_http.go`
- `ToolSet` 增 `hidden proxyprobe.HiddenProxies`、`hiddenTimeout time.Duration`、
  `hiddenRetries int`；新增 `SetHiddenService(...)`。
- `buildHTTPClient`：`proxyprobe.ConfigureTransport(tr, t.egressProxy, t.hidden)` 取代
  现在的 `http.ProxyURL`（同时修掉 socks5 不生效）。
- 请求执行处（`doRequest` 一带）：目标 host 为隐藏服务时，超时用 `hiddenTimeout`，
  连接/传输错误按 `hiddenRetries` 指数退避重试（仅幂等 GET/HEAD；POST 不自动重试）。

### 4.2 接线
- [agent/worker.go](file:///d:/xm-v0.1.0/Pangu/agent/worker.go#L505) 创建 `tsx` 后：
  `tsx.SetEgressProxy(w.proxyAddr, w.proxyCACert)` + `tsx.SetHiddenService(cfg)`；
  在 [server/task_llm.go](file:///d:/xm-v0.1.0/Pangu/server/task_llm.go#L710) 给 Worker 注入
  manager 的隐藏服务配置（新增 `Worker.SetHiddenService`）。
- planner/mainagent 的 `tsx` 同样接 egress/hidden（与 worker 一致）。
- [server/customtool.go](file:///d:/xm-v0.1.0/Pangu/server/customtool.go#L474-L510)
  `platformEgressTransport` / `httpProxyTransport` 改用 `ConfigureTransport`。

### 4.3 `agent/tools_probe.go`（curl 脚本）
- 生成脚本时嵌入 tor/i2p 地址、超时、重试：
  - `.onion` → `curl --proxy "$TOR"`；`.i2p` → `--proxy "$I2P"`；其余保持继承环境；
  - 隐藏服务加 `--connect-timeout <connect> --max-time <timeout> --retry <n> --retry-delay 2`；
  - 普通 URL 保持 10s、无重试；隐藏服务**串行**执行（脚本天然串行，写明并发=1）。
- 单测：脚本包含按 host 选择代理的分支与超时/重试参数。

### 4.4 WebFetch / 已知限制
- WebFetch 是 SDK 单代理，无法按 host 路由：`.onion/.i2p` 请用 `http_request`/`http_probe`；
  若必须用 WebFetch，把 `global_proxy` 临时设为 Tor（全量走 Tor）。
- `scan_with_nuclei` 只有单 `-proxy`：捕获开时经 MITM 上游自动路由；捕获关时建议把
  `global_proxy` 设为 Tor，或只对普通目标跑 nuclei。文档写明。

## 5. 设置 API 与前端

- [server/server.go](file:///d:/xm-v0.1.0/Pangu/server/server.go#L3691) GET 设置返回上述 6 个键；
  PUT 请求体加同名字段 + 校验（代理 URL、超时 10–600、重试 0–5）。
- 前端：
  - [types.ts](file:///d:/xm-v0.1.0/Pangu/web/src/lib/types.ts#L1262) 加字段；
  - [settings/page.tsx](file:///d:/xm-v0.1.0/Pangu/web/src/app/(main)/system/settings/page.tsx#L388-L397)
    在全局代理卡片旁新增「Tor / I2P 隐藏服务」卡片：启用开关、Tor 代理、I2P 代理、
    超时、重试、连接超时 + 保存；
  - [mock/data.ts](file:///d:/xm-v0.1.0/Pangu/web/src/lib/mock/data.ts#L3335) 补默认值。

## 6. 文档与使用示例

- [docs/部署手册.md](file:///d:/xm-v0.1.0/Pangu/docs/部署手册.md) 新增「Tor / I2P 隐藏服务」节：
  - Tor：安装/启动 tor，确认 `127.0.0.1:9050`，设置 `tor_proxy=socks5h://127.0.0.1:9050`；
    示例 `http_request(url="http://example.onion/")`、`http_probe`。
  - I2P：启动 I2P router，HTTP 代理 `127.0.0.1:4444`（或 SOCKS 4447），设置 `i2p_proxy`；
    示例。
  - 验证命令：`curl --socks5-hostname 127.0.0.1:9050 http://example.onion/`；
  - 超时/重试/并发建议（默认 90s、2 次、隐藏服务串行）；
  - 限制：WebFetch/nuclei 的单代理限制与规避。
- [skills/attack-surface-recon/SKILL.md](file:///d:/xm-v0.1.0/Pangu/skills/attack-surface-recon/SKILL.md)
  增一小节「Tor/I2P 隐藏服务」，指向设置与工具（不新建技能包）。

## 7. 验证

1. `go build ./...`、`go vet ./agent/ ./server/ ./db/ ./traffic/ ./proxyprobe/`。
2. 单测：
   - `go test ./proxyprobe/ -run 'TestHidden|TestConfigureTransport'`
   - `go test ./traffic/ -run 'TestHiddenUpstream'`
   - `go test ./agent/ -run 'TestProbeHiddenProxy|TestBuildHTTPClientHidden'`
3. 前端：`cd web && npx tsc --noEmit`、`$env:NEXT_EXPORT="1"; npx next build`。
4. 手工（需本机 Tor）：启动 tor → 设置 tor_proxy → `http_request` 访问授权 .onion；
   捕获开/关各验一次；确认流量留痕（捕获开）。
5. CHANGELOG `[Unreleased]` 增条目；**不提交**。

## 8. 关键文件清单

| 文件 | 动作 |
|---|---|
| `proxyprobe/proxyprobe.go` (+test) | 隐藏服务 host 匹配 + 可复用 transport/dialer |
| `server/manager.go` | 6 个设置键、加载、Getter/Setter、下发 traffic |
| `traffic/traffic.go` (+test) | 隐藏后缀优先的上游路由 |
| `server/customtool.go` | 平台出口 transport 改用统一配置 |
| `agent/tools_http.go` | socks 兼容 + 隐藏服务超时/重试 |
| `agent/tools_probe.go` (+test) | curl 脚本按 host 选代理 + 超时/重试 |
| `agent/worker.go` / `planner.go` / `mainagent.go` | 接线 egress/hidden 到 ToolSet |
| `server/task_llm.go` / `server/server.go` | 注入配置 + 设置 GET/PUT |
| `web/src/lib/types.ts`、`settings/page.tsx`、`mock/data.ts` | 前端字段/卡片 |
| `docs/部署手册.md`、`skills/attack-surface-recon/SKILL.md` | 配置说明与示例 |
| `CHANGELOG.md` | Unreleased 条目 |
