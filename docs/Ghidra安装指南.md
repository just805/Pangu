# Ghidra 安装指南（原生 PE 伪代码反编译）

> 适用：Pangu「任务详情 → 逆向分析」里对原生 Windows PE 的伪代码反编译（P0c）。
> 本文以 **Ghidra 12.2（2026-10-16 发布）** 为例；换版本前先看 §1 的 JDK 对照。

## 0. 先确认要不要装

- 装了 Ghidra：原生 PE 会走 `analyzeHeadless`，逐函数导出 C 伪代码，前端可浏览、可参与敏感信息二次扫描。
- 没装也能用：PE 节区、熵、导入表、签名、字符串、二进制内硬编码凭据扫描都不依赖 Ghidra。
- 资源开销：安装包约 **543 MB**，解压后约 **1.5–2 GB**（官方最低标注 1 GB 磁盘）；官方最低要求 4 GB 内存。反编译是异步任务，单个样本最长 20 分钟。
- 安全边界：Pangu 只把样本交给 Ghidra 做**只读静态分析**，绝不执行样本。Ghidra 自身也出过安全公告，请使用官方最新版，并只在授权测试环境使用。

## 1. 版本与 JDK 对照

| Ghidra 版本 | 需要的 JDK |
| --- | --- |
| 12.x | JDK **25** 64-bit |
| 11.1 – 11.4.x | JDK **21** 64-bit |
| 11.0 / 10.4 | JDK **17** 64-bit |
| 10.0 – 10.3 | JDK **11** 64-bit |

以官方 [Getting Started](https://github.com/NationalSecurityAgency/ghidra/blob/master/GhidraDocs/GettingStarted.md) 为准。JDK 版本不匹配时，headless 会直接报 `UnsupportedClassVersionError`。

## 2. Linux 安装（推荐，与 Pangu 同机）

以下命令按 Debian/Ubuntu + `/opt/ghidra` 编写，需要 root 或 sudo。

### 2.1 安装 JDK 25（Temurin）

```bash
sudo apt-get update
sudo apt-get install -y --no-install-recommends ca-certificates curl gnupg unzip

sudo mkdir -p /etc/apt/keyrings
curl -fsSL https://packages.adoptium.net/artifactory/api/gpg/key/public \
  | sudo gpg --dearmor -o /etc/apt/keyrings/adoptium.gpg

CODENAME=$(. /etc/os-release && echo "$VERSION_CODENAME")
echo "deb [signed-by=/etc/apt/keyrings/adoptium.gpg] https://packages.adoptium.net/artifactory/deb ${CODENAME} main" \
  | sudo tee /etc/apt/sources.list.d/adoptium.list

sudo apt-get update
sudo apt-get install -y temurin-25-jdk
java -version   # 必须显示 25.x
```

离线机器：从 [Adoptium Temurin 25](https://adoptium.net/temurin/releases/?version=25) 下载 `OpenJDK25U-jdk_x64_linux_hotspot_*.tar.gz`，解压到 `/opt/jdk-25`，然后把 `/opt/jdk-25/bin` 加入 `PATH` 或设置 `JAVA_HOME=/opt/jdk-25`。

### 2.2 下载并校验 Ghidra

固定版本（推荐，可复现）：

```bash
GHIDRA_VER=12.2
GHIDRA_DATE=20261016
GHIDRA_SHA256=2c2cd776db3c1f6a097ba418922378ac3649cadd5e844a6f5f088ae628a7be67

mkdir -p /tmp/ghidra-dl && cd /tmp/ghidra-dl
curl -fL --retry 3 -o ghidra.zip \
  "https://github.com/NationalSecurityAgency/ghidra/releases/download/Ghidra_${GHIDRA_VER}_build/ghidra_${GHIDRA_VER}_PUBLIC_${GHIDRA_DATE}.zip"
echo "${GHIDRA_SHA256}  ghidra.zip" | sha256sum -c -
```

SHA-256 来自对应 Release 的说明正文；换版本时务必在 [Releases](https://github.com/NationalSecurityAgency/ghidra/releases) 页面重新核对，不要沿用本文的旧值。

### 2.3 解压安装

```bash
sudo rm -rf /opt/ghidra                 # 只清理旧目录；不要在旧目录上覆盖解压
sudo unzip -q /tmp/ghidra-dl/ghidra.zip -d /tmp/ghidra-dl
sudo mv /tmp/ghidra-dl/ghidra_12.2_PUBLIC /opt/ghidra
sudo chmod +x /opt/ghidra/support/analyzeHeadless
```

### 2.4 验证 headless 可运行

不带参数运行会打印用法（退出码非 0 属正常），确认 JDK 与启动脚本没报错：

```bash
/opt/ghidra/support/analyzeHeadless 2>&1 | head -n 20
```

再跑一次真实导入（分析 `/bin/ls` 即可，验证脚本、JDK 与原生组件都正常）：

```bash
rm -rf /tmp/ghidra-smoke && mkdir -p /tmp/ghidra-smoke
/opt/ghidra/support/analyzeHeadless /tmp/ghidra-smoke proj \
  -import /bin/ls -deleteProject
```

看到 `Import succeeded` / `ANALYZING` 且退出码为 0 即通过。首次运行会在当前用户 `$HOME` 下创建 `.ghidra` 设置与缓存目录，需要可写。

如果是源码部署（仓库就在本机），可以进一步用 Pangu 的实际脚本做冒烟，确认脚本与当前 Ghidra 版本兼容：

```bash
/opt/ghidra/support/analyzeHeadless /tmp/ghidra-smoke pangu \
  -import /path/to/sample.exe \
  -analysisTimeoutPerFile 600 \
  -scriptPath /opt/pangu/server/ghidra_scripts \
  -postScript DecompileAll.java /tmp/ghidra-out \
  -deleteProject
ls /tmp/ghidra-out | head
```

### 2.5 让 Pangu 找到它

Pangu 的探测顺序（见 `server/ghidra.go`）：

1. `PATH` 中的 `analyzeHeadless` / `analyzeHeadless.bat`
2. `GHIDRA_HOME` / `GHIDRA_INSTALL_DIR` 下的 `support/analyzeHeadless`
3. `/opt/ghidra`、`/usr/local/ghidra`、`/usr/share/ghidra`

推荐直接做软链，让后台探测和「宿主环境」页的静态检测同时命中：

```bash
sudo ln -sf /opt/ghidra/support/analyzeHeadless /usr/local/bin/analyzeHeadless
command -v analyzeHeadless && echo "PATH ok"
```

> 「宿主环境」页的 Ghidra 检测只认 `/opt/ghidra/support/analyzeHeadless` 与
> `/usr/local/bin/analyzeHeadless` 两个路径；装到别处时请做上面的软链。

**systemd 部署注意**：Pangu 服务单元默认 `Environment=HOME=<安装目录>`，PATH 不包含自定义目录。

- 用软链方案：无需改单元，重启即可。
- 用 `GHIDRA_HOME` 方案：需要把变量注入服务环境：

```bash
sudo systemctl edit pangu
```

```ini
[Service]
Environment=GHIDRA_HOME=/opt/ghidra
# 大样本建议同时抬 JVM 堆上限（Ghidra 读取该变量）
Environment=MAXMEM=4G
```

```bash
sudo systemctl daemon-reload
sudo systemctl restart pangu
```

验证服务身份下确实可见：

```bash
sudo -u pangu env -i \
  PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
  HOME=/opt/pangu \
  sh -c 'command -v analyzeHeadless' && echo "service can see ghidra"
```

### 2.6 在 Pangu 里验证

1. 「系统 → 宿主环境」确认 `ghidra` 显示已安装。
2. 任务详情 →「逆向分析」上传一个原生 PE（非 .NET）；确认类型为 `Windows PE`。
3. 点「反编译」：接口立即返回 `202 running`，按钮进入「反编译中…」，前端每 3 秒轮询。
4. 完成后自动列出 `.c` 文件并打开首个函数；敏感信息表会出现来源为「反编译」的命中。

产物目录：`data/tasks/<taskID>/reverse/decompiled/<sha256>/ghidra/`，随任务删除/归档一起处理。

## 3. Docker 部署

Pangu 的官方镜像**不内置 Ghidra**（体积与按需使用考虑）。两种做法：

### 3.1 方案 A：基于官方镜像构建派生镜像（推荐）

新建 `Dockerfile.ghidra`：

```dockerfile
ARG BASE=ghcr.io/just805/pangu:latest
FROM ${BASE}

# JDK 25（Ghidra 12.x 要求）
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl gnupg unzip \
 && mkdir -p /etc/apt/keyrings \
 && curl -fsSL https://packages.adoptium.net/artifactory/api/gpg/key/public \
      | gpg --dearmor -o /etc/apt/keyrings/adoptium.gpg \
 && CODENAME=$(. /etc/os-release && echo "$VERSION_CODENAME") \
 && echo "deb [signed-by=/etc/apt/keyrings/adoptium.gpg] https://packages.adoptium.net/artifactory/deb ${CODENAME} main" \
      > /etc/apt/sources.list.d/adoptium.list \
 && apt-get update && apt-get install -y --no-install-recommends temurin-25-jdk \
 && rm -rf /var/lib/apt/lists/*

# Ghidra（版本/日期/SHA 换版本时从 Release 页更新）
ARG GHIDRA_URL=https://github.com/NationalSecurityAgency/ghidra/releases/download/Ghidra_12.2_build/ghidra_12.2_PUBLIC_20261016.zip
ARG GHIDRA_SHA256=2c2cd776db3c1f6a097ba418922378ac3649cadd5e844a6f5f088ae628a7be67
RUN curl -fL --retry 3 -o /tmp/ghidra.zip "$GHIDRA_URL" \
 && echo "${GHIDRA_SHA256}  /tmp/ghidra.zip" | sha256sum -c - \
 && unzip -q /tmp/ghidra.zip -d /opt \
 && mv /opt/ghidra_12.2_PUBLIC /opt/ghidra \
 && chmod +x /opt/ghidra/support/analyzeHeadless \
 && ln -sf /opt/ghidra/support/analyzeHeadless /usr/local/bin/analyzeHeadless \
 && rm -f /tmp/ghidra.zip

ENV GHIDRA_HOME=/opt/ghidra
# 大样本反编译时的 JVM 堆上限；不需要可删掉
ENV MAXMEM=4G
```

构建并把编排指向新镜像：

```bash
docker build -f Dockerfile.ghidra -t pangu-ghidra:12.2 .
```

在 `.env` 里切换镜像：

```dotenv
PANGU_IMAGE=pangu-ghidra
PANGU_TAG=12.2
```

然后 `docker compose up -d`（Windows 记得叠加 `docker-compose.windows.yml`）。

注意：

- 镜像会增大 1.5 GB 以上，构建与拉取都更慢；
- Pangu 容器以 root 运行，Ghidra 的 `$HOME/.ghidra` 写在容器层，容器重建即丢失（只是缓存，不影响功能）；
- Docker Desktop 内存至少给 8 GB（宿主资源设置），否则 Ghidra 分析大 PE 容易 OOM；
- 升级 Pangu 镜像后需要按新基础镜像重新构建一次派生镜像。

### 3.2 方案 B：运行时临时装进容器（仅验证用）

```bash
docker exec -it <pangu容器> bash
# 在容器内按 §2.1–2.4 安装
```

容器重建后全部丢失，不要用于生产。

## 4. Windows（本机开发/验证）

生产环境的 Windows 请按《部署手册》走 Docker Desktop，不推荐原生跑 Pangu。仅本机开发时需要：

1. 安装 **JDK 25 x64**（Adoptium Temurin MSI），确认 `java -version` 为 25。
2. 从 [Releases](https://github.com/NationalSecurityAgency/ghidra/releases) 下载 `ghidra_<版本>_PUBLIC_<日期>.zip`，解压到无空格路径，例如 `D:\tools\ghidra_12.2_PUBLIC`。
3. 设置环境变量（系统级或当前终端）：

```powershell
$env:GHIDRA_HOME = "D:\tools\ghidra_12.2_PUBLIC"
$env:MAXMEM = "4G"
# 用系统自带的 notepad.exe 做一次冒烟导入
& "$env:GHIDRA_HOME\support\analyzeHeadless.bat" "$env:TEMP\ghidra-smoke" proj `
  -import "C:\Windows\System32\notepad.exe" -deleteProject
```

4. **重启 Pangu 进程**：Go 进程只会在启动时读取环境变量，改完 PATH/`GHIDRA_HOME` 后必须重启才生效。
5. 若走 Docker Desktop，请用 §3 的方案，不要试图把 Windows 的 `D:\tools\ghidra` 直接挂进 Linux 容器（JDK/原生组件与容器 glibc 不保证兼容）。

## 5. Pangu 侧的行为与限制

| 项目 | 行为 |
| --- | --- |
| 触发方式 | `POST /api/tasks/{id}/reverse/samples/{sid}/decompile`，body `{"engine":"auto\|ghidra"}` |
| 执行模式 | Ghidra 异步：立即返回 `202 {status:"running"}`，前端轮询 `GET .../decompiled` |
| 自动分析 | `-analysisTimeoutPerFile 600`（单文件 600 秒，超时后停止分析继续跑脚本） |
| 总超时 | 20 分钟；超时标记 `failed`，错误写入 `decompile_error` |
| 函数上限 | 每个样本最多 500 个函数，单函数反编译超时 60 秒 |
| 产物 | 每个函数一个 `<入口地址>_<函数名>.c`，文件名已做跨平台清洗 |
| 后续处理 | `.c/.h/.cpp/.hpp/.cc/.inc` 视为文本，可在线查看并参与秘密扫描（`source=decompile`） |
| 临时文件 | Ghidra 工程与内嵌脚本写在 `os.MkdirTemp` 目录，执行完自动删除 |
| 并发 | 同一样本同时只允许一个反编译任务；任务删除会等待反编译收尾 |

这些上限当前是代码常量，不提供 UI 配置；超大样本建议先用 `strings`/triage 缩小范围。

## 6. 常见问题

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 点反编译返回 409「Ghidra 未安装」 | 后台探测不到 `analyzeHeadless` | 做 `/usr/local/bin/analyzeHeadless` 软链或设置 `GHIDRA_HOME`；重启 Pangu |
| `UnsupportedClassVersionError` / 提示 Java 版本不符 | JDK 与 Ghidra 版本不匹配 | 按 §1 换 JDK；`JAVA_HOME` 优先级高于 PATH，注意别指到旧 JDK |
| `analyzeHeadless: command not found`（systemd） | 服务 PATH 不含安装目录 | 用软链或 `systemctl edit` 注入 `GHIDRA_HOME` |
| `Permission denied` | `support/analyzeHeadless` 无执行位 | `chmod +x /opt/ghidra/support/analyzeHeadless` |
| `OutOfMemoryError` / 分析中途退出 | JVM 堆不足 | 设置 `MAXMEM=4G`（或更大）并重启 Pangu；同时确认机器内存充足 |
| 任务一直 `running` | 大样本分析耗时接近上限 | 等待 20 分钟总超时；或换更小的样本验证；检查 `ps` 是否有残留 `analyzeHeadless` |
| 状态 `failed`，提示「没有产出文件」 | 分析失败 / 脚本未找到 / 样本不是可识别 PE | 用 §2.4 的命令手工复现；必要时加 `-scriptlog /tmp/ghidra.log` 查看脚本日志 |
| 反编译成功但只看到少量函数 | 加壳/混淆/函数识别失败，或达到 500 上限 | 先用 triage 看壳与熵；必要时人工脱壳后再上传 |
| Linux arm64 上 Ghidra 起不来 | 官方 Release 只带 Linux x86_64 原生组件 | 在目标机执行 `cd /opt/ghidra/support/gradle && ./gradlew buildNatives`，或改用 x86_64 节点 |
| 容器内 OOM / 被 kill | Docker Desktop 或宿主机内存不足 | 提高 Docker 内存到 ≥8 GB，并设置 `MAXMEM` |

首次运行后 Ghidra 会在 `$HOME` 下生成 `.ghidra` 缓存；systemd 部署时 `HOME` 被指到 Pangu 安装目录，属于正常现象，不要把它设成不可写路径。

## 7. 升级与卸载

升级（不要覆盖解压）：

```bash
sudo mv /opt/ghidra /opt/ghidra.bak
# 按 §2.2–2.3 解压新版本到 /opt/ghidra
sudo ln -sf /opt/ghidra/support/analyzeHeadless /usr/local/bin/analyzeHeadless
sudo systemctl restart pangu
# 验证通过后再删除备份
sudo rm -rf /opt/ghidra.bak
```

卸载：

```bash
sudo rm -f /usr/local/bin/analyzeHeadless
sudo rm -rf /opt/ghidra /opt/pangu/.ghidra
# 若在 systemd 里注入过变量：
sudo systemctl edit pangu   # 删除 GHIDRA_HOME / MAXMEM 两行
sudo systemctl daemon-reload && sudo systemctl restart pangu
```

Docker 部署升级时重新构建派生镜像即可；数据卷不受影响。

## 8. 参考资料

- [Ghidra Releases](https://github.com/NationalSecurityAgency/ghidra/releases)
- [Ghidra Getting Started（含 Java 版本要求）](https://github.com/NationalSecurityAgency/ghidra/blob/master/GhidraDocs/GettingStarted.md)
- 安装目录内的 `support/analyzeHeadlessREADME.html`（headless 全部参数）
- [Adoptium Temurin](https://adoptium.net/temurin/releases)
- [Ghidra Security Advisories](https://github.com/NationalSecurityAgency/ghidra/security/advisories)
