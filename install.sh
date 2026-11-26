#!/usr/bin/env bash
# PANGU 安装 / 部署脚本（唯一入口）
#
# 运行后选择部署形态，再走对应流程；也可用 --mode 直接指定。
#
#   sudo ./install.sh                     交互选择（二进制 / Docker）
#   sudo ./install.sh --mode=binary       二进制：单文件 + systemd + 系统 PostgreSQL
#                                         （macOS 上自动走 launchd + Homebrew PostgreSQL，
#                                          不需要 sudo：brew 与用户级 launchd 都是当前用户）
#   sudo ./install.sh --mode=docker       Docker Compose：容器 + 容器内 PostgreSQL
#   sudo ./install.sh --mode=binary --with-tools
#
#   curl -fsSL .../install.sh | sudo bash    # 一条命令，交互式（模式/代理均可在 /dev/tty 里选）
#
# 幂等：重复执行 = 升级程序，保留数据（config.json / jwt.key / data / 数据库 / .env）。
#
# 卸载：二进制 systemctl disable --now pangu && rm -rf /opt/pangu
#       Docker   cd /opt/pangu && docker compose down -v && rm -rf /opt/pangu
set -euo pipefail

REPO="just805/Pangu"
INSTALL_DIR="/opt/pangu"
SERVICE="pangu"
RUN_USER="pangu"
VERSION="latest"
MODE="${MODE:-}"
WITH_TOOLS=0
LISTEN_ADDR="${LISTEN_ADDR:-:8787}"
DOMAIN="${DOMAIN:-}"
PROXY_ADDR="${PROXY_ADDR:-127.0.0.1:8788}"
PG_SUPER="${PG_SUPER:-postgres}"
# 安装期出网代理（apt / curl / go 下载走它）。留空 = 直连。
# 只作用于「装东西」，与运行期 Agent 的目标代理、LLM 代理互不相干。
INSTALL_PROXY="${INSTALL_PROXY:-${HTTPS_PROXY:-}}"
# macOS 自带 bash 3.2（不支持 ${var,,} 小写展开），用 tr 做兼容
IMAGE="ghcr.io/$(printf '%s' "$REPO" | tr '[:upper:]' '[:lower:]')"
ASSUME_YES=0
# 是否安装特权助手 pangu-hostd(提供「系统 → 宿主环境」页的一键安装)。
# 默认装;不想让 Web 端具备装包能力时用 --no-installer 关掉。
WITH_INSTALLER=1
# 宿主机调优 / 自动备份：默认开启；--no-tune / --no-backup 关闭，--tune / --backup 重开。
# 关闭状态写进 install.env（见下方状态沿用段），重跑升级时不会悄悄把人关掉的东西打开。
# TUNE_FORCE/BACKUP_FORCE：CLI 显式重开优先于状态文件里的关闭状态。
# NO_LIMITS 只对 Docker 形态有意义（容器的 mem_limit/pids_limit）。
DOCTOR=0
NO_TUNE=0; TUNE_FORCE=0
NO_BACKUP=0; BACKUP_FORCE=0
NO_LIMITS=0

# 管道执行（curl ... | sudo bash）时 stdin 是脚本本身，不能用来读输入。
# 只要 /dev/tty 可读，就仍可交互提问（宝塔式一键安装的关键）。
CAN_PROMPT=0
if [ -r /dev/tty ]; then CAN_PROMPT=1; fi

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

usage(){
  cat <<EOF
PANGU 安装脚本

用法: $0 [选项]

系统建议: Debian 12 / Ubuntu 22.04+ LTS（稳定）；Kali 适合开箱全套工具（滚动更新有风险）
          最低 2C4G/20G，推荐 4C8G/60G（含工具、字典与浏览器）

部署形态:
  --mode=binary|docker   二进制（默认，推荐）或 Docker。不给则交互选择
                         （管道执行 curl ... | bash 时同样会从 /dev/tty 交互）
  --with-tools           顺装常用渗透工具（Docker 形态下工具随镜像固定，见说明）

通用:
  --version=VER          版本，默认 latest（如 v0.3.20）
  --dir=PATH             安装目录（默认 /opt/pangu）
  --listen=ADDR          监听地址（默认 :8787）
  --proxy=ADDR           流量记录代理地址（默认 127.0.0.1:8788）
  --domain=NAME          域名部署（如 pangu.example.com）：预置 HTTPS 相关环境变量并按域名打印访问入口；不填 = 按 IP:端口 访问（交互安装时也会询问，留空 = IP 直连）
  --trusted-proxy=IP     域名形态下反代所在 IP（默认 127.0.0.1，即反代同机；不同机填反代 IP，登录防爆破才按真实客户端计数）
  --reverse-proxy=MODE   域名形态反代方案：caddy（自动安装配置，推荐）/ existing（自备反代）/ none（暂不配，先 IP 访问）。交互安装会询问，默认 caddy
  --install-proxy=URL    安装期出网代理，如 http://10.0.0.14:7897（默认取 \$HTTPS_PROXY）
  -y, --yes              非交互，全部用默认值
  -h, --help             显示帮助

宿主机调优与备份（仅 Linux；默认开启，关闭状态会记住，重跑仍生效）:
  --no-tune              不做宿主机调优（默认自动：无 swap 时创建、内核网络/句柄参数只升不降、
                         journald 与 Docker 容器日志 20MB×3 上限）；--tune 重新开启
  --no-backup            不装自动备份（默认装 systemd 定时器：每日 03:30 数据库全量，保留 14 天；
                         每周日另归档 data 目录，保留 2 份；升级前先自动做一次库备份）；--backup 重新开启
  --doctor               只体检不安装：对既有部署做只读检查（数据是否挂载到宿主机、日志上限、
                         内存/swap、备份、公网暴露面），不改任何东西，装完随时可重跑

二进制形态专用:
  --user=NAME            运行用户（默认 pangu）
  --pg-super=NAME        PostgreSQL 超级用户（默认 postgres）
  --no-installer         不安装特权助手 pangu-hostd（关掉 Web 端的一键安装能力）

Docker 形态专用:
  --image=REF            镜像名（默认 $IMAGE）
  --no-limits            不给容器设内存/进程数上限（默认按主机内存自动给系统与数据库留余量，
                         防止单个扫描任务把整机拖死；.env 里写 PANGU_LIMITS=0 同效）

示例:
  sudo ./install.sh --mode=binary --with-tools
  sudo ./install.sh --mode=docker --version=v0.1.28
  sudo ./install.sh --mode=binary --install-proxy=http://10.0.0.14:7897 --with-tools
  sudo ./install.sh --doctor          # 装完之后的体检，可随时重跑
EOF
}

# ── 参数 ─────────────────────────────────────────
for arg in "$@"; do
  case "$arg" in
    --mode=*)          MODE="${arg#--mode=}" ;;
    --with-tools)      WITH_TOOLS=1 ;;
    --version=*)       VERSION="${arg#--version=}" ;;
    --dir=*)           INSTALL_DIR="${arg#--dir=}"; DIR_EXPLICIT=1 ;;
    --listen=*)        LISTEN_ADDR="${arg#--listen=}" ;;
    --proxy=*)         PROXY_ADDR="${arg#--proxy=}" ;;
    --install-proxy=*) INSTALL_PROXY="${arg#--install-proxy=}" ;;
    --domain=*)        CLI_DOMAIN="${arg#--domain=}" ;;          # 先存 CLI 值，install.env 加载后再合并（CLI 优先）
    --trusted-proxy=*) CLI_TRUSTED_PROXY="${arg#--trusted-proxy=}" ;;
    --reverse-proxy=*) CLI_PROXY_MODE="${arg#--reverse-proxy=}" ;;
    --user=*)          RUN_USER="${arg#--user=}" ;;
    --pg-super=*)      PG_SUPER="${arg#--pg-super=}" ;;
    --image=*)         IMAGE="${arg#--image=}" ;;
    --no-installer)    WITH_INSTALLER=0 ;;
    --doctor)          DOCTOR=1 ;;
    --no-tune)         NO_TUNE=1 ;;
    --tune)            NO_TUNE=0; TUNE_FORCE=1 ;;
    --no-backup)       NO_BACKUP=1 ;;
    --backup)          NO_BACKUP=0; BACKUP_FORCE=1 ;;
    --no-limits)       NO_LIMITS=1 ;;
    -y|--yes)          ASSUME_YES=1 ;;
    -h|--help)         usage; exit 0 ;;
    *) die "未知参数: $arg（用 --help 查看帮助）" ;;
  esac
done

# ── 安装器状态沿用（升级不丢部署形态决策）──────────
# DOMAIN/TRUSTED_PROXY 持久化到 <安装目录>/install.env：重跑升级时沿用上次的
# 部署形态——否则交互重跑没填域名会把 systemd/compose 里的 COOKIE_SECURE/
# TRUSTED_PROXIES 静默清掉（域名形态漂移回 IP 形态，HTTPS 登录直接坏）。
# CLI 参数（--domain/--trusted-proxy）优先于状态文件；--domain=（空值）清除
# 域名形态回 IP 直连。状态文件随安装目录走，--dir 换目录即换状态。
# 注意：PROXY_MODE/COOKIE_SECURE_VAL 必须在加载/合并前初始化——脚本 set -u，
# 未定义变量直接报错（v0.1.9 首个交互冒烟即栽在这：初始化写在了合并之后）。
PROXY_MODE=""
COOKIE_SECURE_VAL="0"
INSTALL_STATE="$INSTALL_DIR/install.env"
if [ -f "$INSTALL_STATE" ]; then
  # shellcheck disable=SC1090
  . "$INSTALL_STATE"
  [ -n "$DOMAIN" ] && info "沿用既有域名部署形态: $DOMAIN（--domain= 显式覆盖/清除）"
fi
# 调优/备份的开关沿用上次的状态；CLI 显式给出时以 CLI 为准。
if [ "${HOST_TUNE:-1}" = "0" ] && [ "$TUNE_FORCE" != "1" ]; then NO_TUNE=1; fi
if [ "${HOST_BACKUP:-1}" = "0" ] && [ "$BACKUP_FORCE" != "1" ]; then NO_BACKUP=1; fi
# CLI 参数合并（source 会覆盖同名变量，故 CLI 值最后落）：
# --domain= 传空串 = 清除域名形态回 IP 直连；--trusted-proxy= 显式指定反代 IP。
if [ -n "${CLI_DOMAIN+x}" ]; then
  DOMAIN="$CLI_DOMAIN"
  [ -z "$DOMAIN" ] && info "已按 --domain= 清除域名形态（IP 直连）"
fi
if [ -n "${CLI_TRUSTED_PROXY:-}" ]; then
  TRUSTED_PROXY="$CLI_TRUSTED_PROXY"
fi
# 反代方案合并：状态文件沿用 → CLI 显式覆盖 → 域名形态缺省 existing（旧版语义）。
if [ -n "${CLI_PROXY_MODE:-}" ]; then
  case "$CLI_PROXY_MODE" in
    caddy|existing|none) PROXY_MODE="$CLI_PROXY_MODE" ;;
    *) die "--reverse-proxy 只能是 caddy / existing / none" ;;
  esac
elif [ -z "$PROXY_MODE" ] && [ -n "$DOMAIN" ]; then
  PROXY_MODE="existing"
fi
case "$PROXY_MODE" in
  caddy|existing) COOKIE_SECURE_VAL="1" ;;
  none)           COOKIE_SECURE_VAL="0" ;;
esac

# ── 前置检查 ─────────────────────────────────────
DIR_EXPLICIT="${DIR_EXPLICIT:-0}"
IS_MAC=0
case "$(uname -s)" in
  Linux)  : ;;
  Darwin) IS_MAC=1 ;;
  *) die "仅支持 Linux / macOS" ;;
esac

ARCH="$(uname -m)"
if [ "$IS_MAC" = "1" ]; then
  # macOS：brew 与用户级 launchd 都是当前用户体系，全程不需要 sudo。
  # 显式用 sudo 反而坏事：root 的 brew/HOME 与用户不一致，服务会落在 root 的
  # LaunchAgents 里起不来。目录默认也放 $HOME/pangu（/opt 需要特权）。
  [ "$(id -u)" = "0" ] && die "macOS 请直接运行本脚本（不要加 sudo）"
  case "$ARCH" in
    arm64)  PKG_ARCH="darwin-arm64" ;;
    x86_64) PKG_ARCH="darwin-amd64" ;;
    *) die "不支持的 Mac 架构: $ARCH（支持 arm64/x86_64）" ;;
  esac
  # /opt 在 macOS 需要特权；默认装到家目录，显式 --dir 优先
  if [ "$DIR_EXPLICIT" != "1" ]; then
    INSTALL_DIR="$HOME/pangu"
  fi
else
  [ "$(id -u)" = "0" ] || die "请用 root/sudo 运行"
  case "$ARCH" in
    x86_64)  PKG_ARCH="linux-amd64" ;;
    aarch64) PKG_ARCH="linux-arm64" ;;
    *) die "不支持的架构: $ARCH（支持 x86_64/aarch64）" ;;
  esac
fi

# curl 是两种形态的公共依赖
if ! command -v curl >/dev/null 2>&1; then
  command -v apt-get >/dev/null 2>&1 && { apt-get update -qq || true; apt-get install -y curl >/dev/null || true; }
  command -v dnf >/dev/null 2>&1 && dnf install -y curl >/dev/null || true
fi
command -v curl >/dev/null 2>&1 || die "curl 安装失败，请手动安装"

# CA 证书是后续全部 HTTPS 的前提。极简系统（比如用 --no-install-recommends
# 装的 curl 不会带入 Recommends 的 ca-certificates）会出现 curl: (77)
# "error setting certificate file"，所有 HTTPS 请求全挂且报错指不到真因。
# 两大发行版的 CA 包位置都判一下，缺哪个补哪个；装不上也不阻断（真实新
# 机器几乎都自带，这里只兜极端裁剪过的系统）。
# macOS 的系统证书在 /etc/ssl/cert.pem（随系统自带），Linux 才需要下面的兜底安装
if [ "$IS_MAC" != "1" ] && \
   [ ! -e /etc/ssl/certs/ca-certificates.crt ] && [ ! -e /etc/pki/tls/certs/ca-bundle.crt ]; then
  info "未检测到 CA 证书包，尝试安装 ca-certificates..."
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -qq >/dev/null 2>&1 || true
    apt-get install -y ca-certificates >/dev/null 2>&1 || true
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y ca-certificates >/dev/null 2>&1 || true
  fi
  [ -e /etc/ssl/certs/ca-certificates.crt ] || [ -e /etc/pki/tls/certs/ca-bundle.crt ] \
    || die "CA 证书包缺失且自动安装失败，请手动安装 ca-certificates 后重试"
  ok "CA 证书包就绪"
fi

# ════════════════════════════════════════════════════
# 宿主机体检 / 调优 / 备份
# ════════════════════════════════════════════════════
# 设计口径（改这一段之前先读）：
#   - 体检项只读，永不改系统；
#   - 调优项「只升不降 + 幂等 + 失败不阻断安装」：用户手工调大过的参数不动，
#     容器 / 只读内核里应用不了的参数跳过并如实说；
#   - 不自动改防火墙、SSH 配置——自动改可能把人锁在机器外面，这两类只报告；
#   - macOS 不做任何宿主调优（没有 systemd，且多为个人机）。
# 每一项「为什么要调」的依据是平台的负载特征（数字来自代码默认值与镜像实测，
# 见 docs/部署手册.md「服务器规格」）：每任务 3 个 worker；nuclei/httpx/ffuf
# 瞬时打开成百上千连接；Chromium 300–500MB、LibreOffice 200–400MB 的内存峰值。

# mem_mb_now / swap_mb_now：物理内存 / swap 总量（MB），读不到按 0。
mem_mb_now(){ awk '/MemTotal/{print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0; }
swap_mb_now(){ awk '/SwapTotal/{print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0; }

# fs_avail_mb：某路径所在文件系统的可用空间（MB）。路径还不存在时逐级上溯到存在的
# 父目录（安装目录创建之前就要问「这块盘够不够」）。
fs_avail_mb(){
  local p="$1"
  while [ -n "$p" ] && [ ! -d "$p" ]; do p="$(dirname "$p")"; done
  df -Pm "$p" 2>/dev/null | awk 'NR==2{print $4}'
}

# same_path A B：两个路径是否指向同一位置（解析不了时按「不同」处理，不猜）。
same_path(){
  local a b
  a="$(readlink -f "$1" 2>/dev/null || true)"
  b="$(readlink -f "$2" 2>/dev/null || true)"
  [ -n "$a" ] && [ -n "$b" ] && [ "$a" = "$b" ]
}

# env_set FILE KEY VALUE：往 .env 写键值。.env 是 KEY=VALUE 的 shell 格式，已有该键
# 就整行替换，没有就追加——同名键出现两次时取哪个是不确定的。
env_set(){
  if grep -q "^$2=" "$1"; then
    sed -i "s|^$2=.*|$2=$3|" "$1"
  else
    printf '%s=%s\n' "$2" "$3" >> "$1"
  fi
}

# host_timezone：宿主机时区名（如 Asia/Shanghai），识别不出返回空（compose 退回 UTC）。
host_timezone(){
  local tz=""
  if command -v timedatectl >/dev/null 2>&1; then
    tz="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
  fi
  if [ -z "$tz" ] && [ -L /etc/localtime ]; then
    tz="$(readlink /etc/localtime 2>/dev/null | sed 's|.*/zoneinfo/||' || true)"
  fi
  # 时区名进的是 .env 与容器环境变量：只放行区名用到的字符
  case "$tz" in ''|*[!A-Za-z0-9_+-/]*) return 0 ;; esac
  printf '%s' "$tz"
}

# as_pg CMD...：以数据库超级用户身份执行。本机 PostgreSQL 的 peer 认证只认 OS 用户，
# root 不能直接 -U postgres 连。有 sudo 走 sudo（与 deploy_binary 一致），没有就 su。
as_pg(){
  if command -v sudo >/dev/null 2>&1; then
    sudo -u "$PG_SUPER" "$@"
  else
    su -s /bin/sh "$PG_SUPER" -c "$(printf '%q ' "$@")"
  fi
}

# dump_ok FILE：备份文件确实是 PostgreSQL 全量转储（而不是失败留下的空壳）。
# 用 `gzip -dc`（不是 zcat）+ 把 grep 的退出码吞掉：grep 一命中就退出，上游收到
# SIGPIPE，在 pipefail 下整条管道会被判失败——文件明明是好的。
dump_ok(){
  [ -s "$1" ] && gzip -t "$1" 2>/dev/null \
    && [ "$(gzip -dc "$1" 2>/dev/null | grep -c 'PostgreSQL database cluster dump' || true)" -gt 0 ]
}

# ── swap ────────────────────────────────────────────
# 没有 swap 时，内存峰值一旦超出物理内存，进程被 OOM killer 直接杀掉（LibreOffice
# 转 PDF、Chromium 网页测试都是短时冲高的典型）；有 swap 则只是慢一点。所以这是
# 「必须开」的一项（部署手册「选购坑位」第 2 条）。
# 规则：只有完全没有 swap 时才建；已经有的（包括用户自己调过大小的）一律不动。
ensure_swap(){
  if [ "$(swap_mb_now)" -gt 0 ]; then return 0; fi
  local ram; ram="$(mem_mb_now)"
  [ "$ram" -gt 0 ] || return 0
  local size=4096
  [ "$ram" -gt 8192 ] && size=2048
  [ "$ram" -gt 32768 ] && return 0      # 大内存机器：物理内存足够吸收峰值，不占盘
  # 这些文件系统上 swapfile 内核不接受（overlay）/ 需要 nocow 等特殊处理（btrfs），
  # 与其建完再失败，不如一开始就说清楚。
  local fstype; fstype="$(findmnt -no FSTYPE / 2>/dev/null || true)"
  case "$fstype" in
    overlay|btrfs|zfs|tmpfs)
      warn "根分区是 $fstype（容器 / 特殊文件系统），不自动建 swap：请由宿主或云平台保证内存余量"
      return 0 ;;
  esac
  local file=/swapfile
  local avail; avail="$(fs_avail_mb /)"
  if [ -n "$avail" ] && [ "$avail" -lt $((size + 6144)) ]; then
    warn "根分区可用 ${avail}M，不足以安全创建 ${size}M swap 文件——跳过（建议先清盘或扩盘）"
    return 0
  fi
  info "未检测到 swap，创建 ${size}M（$file）..."
  if [ ! -f "$file" ]; then
    # fallocate 瞬间完成；dd 兜底给不支持 fallocate 的文件系统
    if ! fallocate -l "${size}M" "$file" 2>/dev/null; then
      dd if=/dev/zero of="$file" bs=1M count="$size" conv=fsync 2>/dev/null \
        || { rm -f "$file"; warn "创建 swap 文件失败，跳过"; return 0; }
    fi
  fi
  chmod 600 "$file" 2>/dev/null || true
  mkswap "$file" >/dev/null 2>&1 || { warn "mkswap 失败，跳过 swap"; return 0; }
  if ! swapon "$file" 2>/dev/null; then
    # fallocate 分配的文件在部分文件系统上内核拒绝当 swap：退回 dd 重写一次
    rm -f "$file"
    dd if=/dev/zero of="$file" bs=1M count="$size" conv=fsync 2>/dev/null \
      || { rm -f "$file"; warn "swap 启用失败，跳过"; return 0; }
    chmod 600 "$file" 2>/dev/null || true
    if ! mkswap "$file" >/dev/null 2>&1 || ! swapon "$file" 2>/dev/null; then
      rm -f "$file"; warn "swap 启用失败，跳过"; return 0
    fi
  fi
  # 写进 fstab：否则重启后 swap 就没了，下次又被 OOM 教育一遍
  if ! grep -q '^/swapfile' /etc/fstab 2>/dev/null; then
    printf '/swapfile none swap sw 0 0\n' >> /etc/fstab
  fi
  ok "已启用 ${size}M swap（/swapfile，已写入 fstab）"
}

# ── 内核参数 ────────────────────────────────────────
# tune_kernel 把大规模扫描会用到的内核参数提到够用的值，落盘到 /etc/sysctl.d/99-pangu.conf。
# 逐个 `sysctl -w` 试而不是整份文件写完再 apply：/proc/sys 的键只有在对应模块加载后
# 才存在（如 nf_conntrack），把不存在的键写进配置文件，开机时 sysctl 会报错。所以
# 「当前读得到 + 写得进去 + 现值确实更低」的键才落盘。各项含义见 kernel_report 的对照表。
tune_kernel(){
  local f=/etc/sysctl.d/99-pangu.conf
  local kept=()      # 文件里已有的键：原样保留（用户可能手工改过值），不重复追加
  local line k
  if [ -f "$f" ]; then
    while IFS= read -r line; do
      case "$line" in ''|'#'*) continue ;; esac
      k="$(printf '%s' "$line" | sed 's/[[:space:]]*=.*//;s/^[[:space:]]*//')"
      [ -n "$k" ] && kept+=("$k")
    done < "$f"
  else
    printf '# Pangu 宿主机调优（install.sh 生成；只升不降，值可手工改）\n' > "$f"
  fi
  local applied=0
  # in_kept KEY：该键是否已在配置文件里。
  in_kept(){ local q; for q in ${kept[@]+"${kept[@]}"}; do [ "$q" = "$1" ] && return 0; done; return 1; }
  # sysctl_raise KEY WANT：现值 ≥ 目标值就不动（只升不降）；写不进去就跳过。
  sysctl_raise(){
    local cur
    in_kept "$1" && return 0
    cur="$(sysctl -n "$1" 2>/dev/null || true)"
    [ -n "$cur" ] || return 0
    if [ "$cur" -ge "$2" ] 2>/dev/null; then return 0; fi
    if sysctl -q -w "$1=$2" 2>/dev/null; then
      printf '%s = %s\n' "$1" "$2" >> "$f"; kept+=("$1"); applied=$((applied + 1))
    else
      warn "内核参数 $1 写不进去（只读内核 / 容器环境？），跳过"
    fi
  }
  sysctl_raise fs.file-max 2097152
  sysctl_raise fs.inotify.max_user_watches 1048576
  sysctl_raise net.core.somaxconn 4096
  sysctl_raise net.ipv4.tcp_max_syn_backlog 8192
  sysctl_raise net.ipv4.tcp_tw_reuse 1
  # 连接跟踪表：Docker 开了 iptables 后本机发起的连接也被跟踪，默认 65536 条在
  # 大规模扫描时几秒就满，满了之后新连接直接被丢（表现为大量超时而不是报错）。
  sysctl_raise net.netfilter.nf_conntrack_max 1048576
  # 临时端口范围：默认 32768–60999 约 2.8 万个，高并发下耗尽报 EADDRNOTAVAIL。
  # 值是「低 高」两段，单独处理：两端各取更宽的合并——不收窄用户已经放宽的范围。
  if ! in_kept net.ipv4.ip_local_port_range; then
    local pr low high nl nh
    pr="$(sysctl -n net.ipv4.ip_local_port_range 2>/dev/null || true)"
    if [ -n "$pr" ]; then
      low="$(printf '%s' "$pr" | awk '{print $1}')"; high="$(printf '%s' "$pr" | awk '{print $2}')"
      nl="$low"; nh="$high"
      [ "$low" -gt 10240 ] 2>/dev/null && nl=10240
      [ "$high" -lt 65000 ] 2>/dev/null && nh=65000
      if [ "$nl $nh" != "$low $high" ] && sysctl -q -w "net.ipv4.ip_local_port_range=$nl $nh" 2>/dev/null; then
        printf 'net.ipv4.ip_local_port_range = %s %s\n' "$nl" "$nh" >> "$f"
        kept+=("net.ipv4.ip_local_port_range"); applied=$((applied + 1))
      fi
    fi
  fi
  # swappiness：有 swap 才谈。10 = 尽量不用 swap，只在真的吃紧时才换出——既保住
  # 「峰值不被 OOM 杀」，又不让常驻进程被早早换出去。用户调得更激进的（<10）不动。
  if [ "$(swap_mb_now)" -gt 0 ] && ! in_kept vm.swappiness; then
    local sw; sw="$(sysctl -n vm.swappiness 2>/dev/null || true)"
    if [ -n "$sw" ] && [ "$sw" -gt 10 ] 2>/dev/null && sysctl -q -w vm.swappiness=10 2>/dev/null; then
      printf 'vm.swappiness = 10\n' >> "$f"; kept+=("vm.swappiness"); applied=$((applied + 1))
    fi
  fi
  if [ "$applied" -gt 0 ]; then
    ok "内核参数已调优 ${applied} 项 → $f"
  else
    ok "内核网络 / 句柄参数已是够用值，无需调整"
  fi
  return 0
}

# kernel_report：只读。列出仍低于建议值的内核参数（--doctor 与装完后的体检用）。
# 对照（为什么是这些值）：
#   fs.file-max / somaxconn / tcp_max_syn_backlog / inotify —— 高并发探测的句柄与队列
#   nf_conntrack_max —— 连接跟踪表，满了新连接直接被丢（表现为大量超时而非报错）
kernel_report(){
  local pair key want cur missing=""
  for pair in 'fs.file-max:2097152' 'fs.inotify.max_user_watches:1048576' \
              'net.core.somaxconn:4096' 'net.ipv4.tcp_max_syn_backlog:8192' \
              'net.netfilter.nf_conntrack_max:1048576'; do
    key="${pair%%:*}"; want="${pair#*:}"
    cur="$(sysctl -n "$key" 2>/dev/null || true)"
    [ -n "$cur" ] || continue
    [ "$cur" -ge "$want" ] 2>/dev/null || missing="$missing $key=$cur（建议 $want）"
  done
  if [ -n "$missing" ]; then
    warn "内核参数偏低（sudo ./install.sh --tune 可自动调优）：$missing"
  else
    ok "内核网络 / 句柄参数在建议值以上"
  fi
}

# ── 日志上限 ────────────────────────────────────────
# tune_journald：systemd-journald 默认最多吃到文件系统的 10%（上限 4G）。二进制形态下
# 工具输出与扫描日志持续进 journal，不封顶迟早把系统盘写满。
tune_journald(){
  # 已经有人管过（主配置或任何 drop-in 里出现 SystemMaxUse）就不插手
  if grep -rqs '^[[:space:]]*SystemMaxUse' /etc/systemd/journald.conf /etc/systemd/journald.conf.d 2>/dev/null; then
    return 0
  fi
  mkdir -p /etc/systemd/journald.conf.d 2>/dev/null || return 0
  printf '[Journal]\n# Pangu 宿主机调优（install.sh 生成）：系统日志总量上限\nSystemMaxUse=200M\n' \
    > /etc/systemd/journald.conf.d/99-pangu.conf
  systemctl restart systemd-journald >/dev/null 2>&1 || true
  ok "系统日志（journald）总量上限 200M"
}

# tune_docker_daemon：给 Docker 守护进程的容器日志封顶。
# json-file 驱动默认不限大小，长跑服务的 stdout 几个月能涨到几十 G，而且
# `docker system df` 完全不统计这部分——这是最隐蔽的磁盘黑洞。
# compose 里已给本项目的容器单独设了 logging；这一项是守护进程级的兜底（机器上
# 其它容器也受保护）。
tune_docker_daemon(){
  local f=/etc/docker/daemon.json
  if [ -f "$f" ] && grep -qs '"max-size"' "$f"; then
    ok "Docker 容器日志上限已配置（$f）"
    return 0
  fi
  mkdir -p /etc/docker 2>/dev/null || return 0
  local swarm="inactive"
  swarm="$(docker info -f '{{.Swarm.LocalNodeState}}' 2>/dev/null || echo inactive)"
  # 合并而不是覆盖：国内机器的 daemon.json 里几乎都有 registry-mirrors，整份覆盖
  # 等于把人家的镜像加速器删掉。合并交给 python3 / jq；两者都没有且文件非空时不碰。
  local rc=0 out="" py
  py="$(mktemp)"
  if command -v python3 >/dev/null 2>&1; then
    cat > "$py" <<'PYEOF'
import json, os, shutil, sys
path, swarm = sys.argv[1], sys.argv[2]
data = {}
if os.path.exists(path) and os.path.getsize(path) > 0:
    try:
        with open(path) as fh:
            data = json.load(fh)
    except ValueError as e:
        sys.stderr.write("daemon.json is not valid JSON: %s\n" % e)
        sys.exit(3)
    if not isinstance(data, dict):
        sys.stderr.write("daemon.json top level is not an object\n")
        sys.exit(3)
changed = False
note = ""
# log-opts 的 max-size/max-file 只对 json-file / local 驱动有意义；别的驱动上塞这些
# 参数守护进程可能拒绝启动，所以不动。
if data.get("log-driver", "json-file") in ("json-file", "local"):
    opts = data.get("log-opts")
    if opts is None:
        opts = data["log-opts"] = {}
    if isinstance(opts, dict):
        if "max-size" not in opts:
            opts["max-size"] = "20m"; changed = True
        if "max-file" not in opts:
            opts["max-file"] = "3"; changed = True
else:
    note = "log-driver=%s，不是 json-file：不动它的日志参数（各驱动参数不通用）" % data["log-driver"]
# live-restore：守护进程重启 / 升级时容器不停机。swarm 模式与之不兼容。
if swarm != "active" and "live-restore" not in data:
    data["live-restore"] = True; changed = True
if not changed:
    sys.stdout.write(note)
    sys.exit(0)
if os.path.exists(path):
    shutil.copy2(path, path + ".pangu-bak")
with open(path, "w") as fh:
    json.dump(data, fh, indent=2, ensure_ascii=False)
    fh.write("\n")
sys.stdout.write(note)
PYEOF
    out="$(python3 "$py" "$f" "$swarm" 2>/dev/null || true)"; rc=$?
  elif command -v jq >/dev/null 2>&1 && [ -f "$f" ]; then
    out="$(jq '. + {"log-opts": ((.["log-opts"] // {}) + {"max-size":"20m","max-file":"3"})}' "$f" 2>/dev/null || true)"
    if [ -n "$out" ] && printf '%s' "$out" | jq -e . >/dev/null 2>&1; then
      cp -a "$f" "$f.pangu-bak"; printf '%s\n' "$out" > "$f"
    else
      rc=3
    fi
  else
    printf '{\n  "log-driver": "json-file",\n  "log-opts": { "max-size": "20m", "max-file": "3" },\n  "live-restore": true\n}\n' > "$f"
  fi
  rm -f "$py"
  if [ -n "$out" ]; then info "$out"; fi
  if [ "$rc" != 0 ]; then
    warn "/etc/docker/daemon.json 无法解析，未做改动——请手工加 log-opts: {max-size: 20m, max-file: 3} 后 systemctl restart docker"
    return 0
  fi
  # 日志上限与 live-restore 只在守护进程启动时读取。重启 docker 会短暂停掉所有容器，
  # 所以：只有本项目的容器在跑（或没有容器在跑）时才自动重启；机器上还有别的容器
  # 时只提示，由运维择机重启。
  local project foreign
  project="$(basename "$INSTALL_DIR" | tr 'A-Z' 'a-z' | tr -cd 'a-z0-9_-\n')"
  foreign="$(docker ps --format '{{.Names}}' 2>/dev/null | grep -v "^${project}-" || true)"
  if [ -n "$foreign" ]; then
    warn "已写 $f，但机器上还有其它容器在运行，未自动重启 docker——日志上限要在重启后生效（择机：systemctl restart docker）"
    return 0
  fi
  info "重启 docker 使日志上限生效..."
  if ! systemctl restart docker 2>/dev/null; then
    # 改完起不来：立刻回滚——docker 挂了比日志不封顶严重得多
    if [ -f "$f.pangu-bak" ]; then cp -a "$f.pangu-bak" "$f"; else rm -f "$f"; fi
    systemctl restart docker 2>/dev/null || true
    warn "docker 重启失败，已回滚 daemon.json（journalctl -u docker -n 30 看原因）"
    return 0
  fi
  local i
  for i in $(seq 1 30); do docker info >/dev/null 2>&1 && break; sleep 1; done
  if [ "$swarm" = "active" ]; then
    ok "Docker 容器日志上限 20MB×3 已生效（$f）"
  else
    ok "Docker 容器日志上限 20MB×3 已生效，并开启 live-restore（$f：以后重启 / 升级 docker 时容器不再停机）"
  fi
  return 0
}

# tune_host：安装前的宿主机调优总入口。
tune_host(){
  if [ "$IS_MAC" = "1" ]; then return 0; fi
  if [ "$NO_TUNE" = "1" ]; then
    info "已按设置跳过宿主机调优（--tune 可重新开启）"
    return 0
  fi
  info "宿主机调优..."
  ensure_swap
  tune_kernel
  tune_journald
}

# ── 备份 ────────────────────────────────────────────
# PostgreSQL 是平台的唯一存储（任务 / 发现 / 证据索引 / 账号），丢了无法重建。
#   - 升级前：install.sh 自动做一次库备份（升级会跑 schema 迁移，这是唯一没有
#     「撤销」按钮的操作）；
#   - 平时：pangu-backup.timer 每日 03:30 库全量 + 每周日 data 归档。
# 备份落在本机（$INSTALL_DIR/backups）：防误删、误升级、坏迁移，不防整盘故障——
# 整盘要靠云快照或把 backups 同步到别处，这一点在体检里如实提示。
backup_db_now(){   # $1=文件名里的标签（pre-upgrade）
  local dest="$INSTALL_DIR/backups" out rc=0 old
  mkdir -p "$dest"; chmod 700 "$dest" 2>/dev/null || true
  out="$dest/db-$1-$(date +%Y%m%d-%H%M%S).sql.gz"
  if [ "$MODE" = "docker" ]; then
    ( cd "$INSTALL_DIR" && docker compose exec -T postgres sh -c 'pg_dumpall -U "$POSTGRES_USER" | gzip -1' ) > "$out" 2>/dev/null || rc=1
  elif [ -f "$INSTALL_DIR/config.json" ]; then
    as_pg pg_dumpall 2>/dev/null | gzip -1 > "$out" || rc=1
  else
    return 0
  fi
  if [ "$rc" != 0 ] || ! dump_ok "$out"; then
    rm -f "$out"
    warn "升级前数据库备份没有成功（数据库没在运行？）——继续安装，装好后建议手工跑一次 $INSTALL_DIR/backup.sh"
    return 0
  fi
  chmod 600 "$out"
  ok "升级前数据库备份: $out（$(du -h "$out" | cut -f1)）"
  # 升级前备份只留最近 5 份
  old="$(ls -1t "$dest"/db-"$1"-*.sql.gz 2>/dev/null | tail -n +6 || true)"
  if [ -n "$old" ]; then printf '%s\n' "$old" | xargs rm -f; fi
  return 0
}

install_backup(){
  [ "$IS_MAC" = "1" ] && return 0
  if [ "$NO_BACKUP" = "1" ]; then
    info "已按设置跳过自动备份（--backup 可重新开启）"
    return 0
  fi
  local bash_bin; bash_bin="$(command -v bash || echo /bin/bash)"
  cat > "$INSTALL_DIR/backup.sh" <<'BKEOF'
#!/usr/bin/env bash
# Pangu 备份脚本（由 install.sh 生成，重跑 install.sh 会覆盖，勿手工改）。
#   由 pangu-backup.timer 每日 03:30 触发：
#     数据库全量（保留 14 天）；每周日另归档 data 目录（保留 2 份）
# 手工执行：sudo ./backup.sh          做当前到期该做的
#           sudo ./backup.sh --full   立刻做数据库 + data 归档
set -uo pipefail
DIR="$(cd "$(dirname "$0")" && pwd)"
DEST="$DIR/backups"
MODE="__MODE__"
PG_SUPER="__PG_SUPER__"
KEEP_DB_DAYS=14
KEEP_DATA=2
ts="$(date +%Y%m%d-%H%M%S)"
rc=0
mkdir -p "$DEST" && chmod 700 "$DEST"

# ① 数据库（唯一存储，最关键）
db_dump="$DEST/db-$ts.sql.gz"
fail=0
if [ "$MODE" = "docker" ]; then
  (cd "$DIR" && docker compose exec -T postgres sh -c 'pg_dumpall -U "$POSTGRES_USER" | gzip -1') > "$db_dump" 2>/dev/null || fail=1
else
  if command -v sudo >/dev/null 2>&1; then
    sudo -u "$PG_SUPER" pg_dumpall 2>/dev/null | gzip -1 > "$db_dump" || fail=1
  else
    su -s /bin/sh "$PG_SUPER" -c pg_dumpall 2>/dev/null | gzip -1 > "$db_dump" || fail=1
  fi
fi
# 校验：真是一份 PostgreSQL 全量转储，而不是失败留下的空壳
if [ "$fail" = 0 ] && ! gzip -t "$db_dump" 2>/dev/null; then fail=1; fi
if [ "$fail" = 0 ] && [ "$(gzip -dc "$db_dump" 2>/dev/null | grep -c 'PostgreSQL database cluster dump' || true)" -eq 0 ]; then fail=1; fi
if [ "$fail" = 0 ]; then
  chmod 600 "$db_dump"
  echo "[backup] db -> $db_dump ($(du -h "$db_dump" | cut -f1))"
else
  rm -f "$db_dump"; echo "[backup] db FAILED" >&2; rc=1
fi

# ② data 目录（任务工作区 / 流量录制 / 证据 / 会话）：每周日归档一次。
#    排除 nuclei-templates——那是可重新下载的模板库，常有 1GB 上下，不值得占备份空间。
if [ "${1:-}" = "--full" ] || [ "$(date +%u)" = "7" ]; then
  if [ -d "$DIR/data" ]; then
    size_mb="$(du -sm "$DIR/data" 2>/dev/null | cut -f1)"
    avail_mb="$(df -Pm "$DEST" | awk 'NR==2{print $4}')"
    # 余量不足就跳过：备份把盘写满比不备份更糟（会把正在跑的服务一起拖死）
    if [ "${size_mb:-0}" -gt 0 ] && [ "${avail_mb:-0}" -gt $((size_mb * 2 + 1024)) ]; then
      arc="$DEST/data-$ts.tar.gz"
      # tar 退出码 1 =「文件在读取时发生了变化」，对正在运行的目录是常态，不算失败
      if ( tar -C "$DIR" --exclude='data/nuclei-templates' -cf - data 2>/dev/null; [ $? -le 1 ] ) | gzip -1 > "$arc"; then
        chmod 600 "$arc"; echo "[backup] data -> $arc ($(du -h "$arc" | cut -f1))"
      else
        rm -f "$arc"; echo "[backup] data FAILED" >&2; rc=1
      fi
    else
      echo "[backup] data skipped: data ${size_mb:-?}M, avail ${avail_mb:-?}M (not enough headroom)"
    fi
  fi
fi

# ③ 清理过期备份
find "$DEST" -maxdepth 1 -name 'db-*.sql.gz' -mtime +"$KEEP_DB_DAYS" -delete 2>/dev/null
ls -1t "$DEST"/data-*.tar.gz 2>/dev/null | tail -n +"$((KEEP_DATA + 1))" | while IFS= read -r stale; do rm -f "$stale"; done
exit "$rc"
BKEOF
  sed -i "s|^MODE=.*|MODE=\"$MODE\"|;s|^PG_SUPER=.*|PG_SUPER=\"$PG_SUPER\"|" "$INSTALL_DIR/backup.sh"
  chmod 700 "$INSTALL_DIR/backup.sh"
  cat > /etc/systemd/system/pangu-backup.service <<UNIT
[Unit]
Description=PANGU backup (database dump + data archive)
# 数据库没起来时不做：备份脚本会自己失败并留日志，不在这里重试
After=docker.service postgresql.service

[Service]
Type=oneshot
ExecStart=$bash_bin $INSTALL_DIR/backup.sh
# 备份是后台杂务：给扫描任务让路
Nice=10
IOSchedulingClass=idle
UNIT
  cat > /etc/systemd/system/pangu-backup.timer <<UNIT
[Unit]
Description=Run PANGU backup daily

[Timer]
OnCalendar=*-*-* 03:30:00
# 错过的（关机期间）开机后补跑一次
Persistent=true
RandomizedDelaySec=15m

[Install]
WantedBy=timers.target
UNIT
  systemctl daemon-reload
  if systemctl enable --now pangu-backup.timer >/dev/null 2>&1; then
    ok "自动备份已启用：每日 03:30 数据库全量（保留 14 天），每周日另归档 data（保留 2 份）→ $INSTALL_DIR/backups"
  else
    warn "备份定时器未能启用（非 systemd 环境？）——可手工跑：$INSTALL_DIR/backup.sh"
  fi
  return 0
}

# ── 升级前的两项保护 ────────────────────────────────
# 任务运行期间重启服务，正在跑的 worker 这一步会被取消（已写回的事实还在，但那一步
# 的探测结果丢了）。所以先数一下，把决定权交给用户。
RUNNING_TASKS_SQL="SELECT count(*) FROM tasks WHERE status NOT IN ('done','failed','timeout')"
warn_running_tasks(){
  local n=""
  if [ "$MODE" = "docker" ]; then
    n="$(printf '%s' "$RUNNING_TASKS_SQL" \
        | ( cd "$INSTALL_DIR" && docker compose exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "${POSTGRES_DB:-pangu}" -tA' ) 2>/dev/null \
        | tr -d '[:space:]' || true)"
  elif [ -f "$INSTALL_DIR/config.json" ] && command -v psql >/dev/null 2>&1; then
    n="$(printf '%s' "$RUNNING_TASKS_SQL" | as_pg psql -d pangu -tA 2>/dev/null | tr -d '[:space:]' || true)"
  fi
  case "$n" in ''|*[!0-9]*|0) return 0 ;; esac
  warn "有 $n 个任务还没到终态：现在重启会掐断正在跑的这一步（已写回的事实不受影响，事后可在页面重跑该意图）"
  if [ "$ASSUME_YES" != "1" ] && [ "$CAN_PROMPT" = "1" ] && [ -r /dev/tty ]; then
    printf '仍然继续？(y/N) '
    local ans=""; read -r -t 60 ans </dev/tty || ans=""
    case "$ans" in
      y|Y|yes|YES) : ;;
      *) die "已中止（等任务跑完再升级，或先在页面暂停任务）" ;;
    esac
  fi
}

# warn_port_busy PORT 允许的进程名...：端口已经被别的程序占着时提前说。拉完 3.66G
# 镜像、替换完二进制才发现端口冲突，白等。
warn_port_busy(){
  local port="$1"; shift
  command -v ss >/dev/null 2>&1 || return 0
  local names p allowed okp busy=""
  names="$(ss -Hltnp "sport = :$port" 2>/dev/null | grep -o '"[^"]*"' | tr -d '"' | sort -u || true)"
  [ -n "$names" ] || return 0
  for p in $names; do
    okp=0
    for allowed in "$@"; do [ "$p" = "$allowed" ] && okp=1; done
    if [ "$okp" != 1 ]; then busy="$busy $p"; fi
  done
  [ -n "$busy" ] || return 0
  warn "端口 $port 已被其它进程占用（$busy）：新服务会起不来。先停掉它，或用 --listen / PANGU_UI_PORT 换端口"
}

# ── 安全体检（只读）────────────────────────────────
# 只报告不自动改：防火墙、SSH 这类配置自动改错了会连不上机器，由人来做决定。
security_report(){
  if [ "$IS_MAC" = "1" ]; then return 0; fi
  printf '\n── 安全体检（只报告，不改任何配置）──\n'
  local t perm fw
  if command -v sshd >/dev/null 2>&1; then
    t="$(sshd -T 2>/dev/null | grep -E '^(permitrootlogin|passwordauthentication) ' | tr '\n' ' ' || true)"
    case "$t" in
      *'permitrootlogin yes'*) warn "SSH 允许 root 登录：建议 PermitRootLogin prohibit-password 或改用密钥" ;;
    esac
    case "$t" in
      *'passwordauthentication yes'*) info "SSH 仍允许密码登录：公网机器建议改用密钥并装 fail2ban（本机有大量攻击工具，是重点目标）" ;;
    esac
  fi
  # Docker API 明文端口 = 任何能连上的人都是 root
  if command -v ss >/dev/null 2>&1 && ss -Hltn 2>/dev/null | grep -Eq ':(2375|2376)[[:space:]]'; then
    warn "Docker API 端口（2375/2376）在监听：等同把 root 交给任何能连上的人，建议立刻关掉"
  fi
  if [ -z "$DOMAIN" ]; then
    warn "当前按 IP + HTTP 访问：会话令牌与密码明文传输。公网部署请重跑 install.sh --domain=NAME（自动配 HTTPS）"
  fi
  if ! grep -rqs 'PANGU_INIT_TOKEN' "$INSTALL_DIR/.env" /etc/systemd/system/pangu.service 2>/dev/null; then
    info "公网部署建议设置 PANGU_INIT_TOKEN：否则服务启动到您设置管理员密码之间，先到的访客可以抢注管理员"
  fi
  if [ "$MODE" = "docker" ] && [ -f "$INSTALL_DIR/.env" ]; then
    perm="$(stat -c %a "$INSTALL_DIR/.env" 2>/dev/null || true)"
    if [ "$perm" != "600" ]; then warn ".env 权限是 $perm（内含数据库口令），建议 chmod 600"; fi
  fi
  fw="未检测到主机防火墙"
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q 'Status: active'; then
    fw="ufw 已启用"
  elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    fw="firewalld 已启用"
  elif command -v nft >/dev/null 2>&1 && [ -n "$(nft list ruleset 2>/dev/null | grep -vE '^[[:space:]]*(#|$)' | grep -vi docker | head -1)" ]; then
    fw="nftables 有规则"
  fi
  printf '  主机防火墙: %s\n' "$fw"
  if [ "$fw" = "未检测到主机防火墙" ]; then
    info "没有主机防火墙时，请确认云安全组只放行必要端口（SSH / 80 / 443 / UI 端口）；数据库端口 5432 不要对公网开放"
  fi
}

# ── --doctor：对既有部署做只读体检 ──────────────────
run_doctor(){
  local mode="binary"
  if [ -f "$INSTALL_DIR/docker-compose.yml" ]; then mode="docker"; fi
  MODE="$mode"      # --doctor 不经过形态选择，安全体检要靠探测到的形态判断（如 .env 权限）
  info "Pangu 部署体检（只读：不改任何文件、不重启任何服务）"
  printf '  部署形态: %s   安装目录: %s\n' "$mode" "$INSTALL_DIR"
  local port=8787 code cid src old crashes last
  if [ "$mode" = "docker" ]; then
    port="$(grep -E '^PANGU_UI_PORT=' "$INSTALL_DIR/.env" 2>/dev/null | cut -d= -f2)"
    if [ -z "$port" ]; then port=8787; fi
  else
    port="${LISTEN_ADDR##*:}"
  fi
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:${port}/api/health" 2>/dev/null || true)"
  if [ "$code" = "200" ]; then
    ok "服务健康（http://127.0.0.1:${port}/api/health）"
  else
    warn "服务未响应（HTTP ${code}）"
  fi
  if [ "$mode" = "docker" ]; then
    cid="$(cd "$INSTALL_DIR" && docker compose ps -q pangu 2>/dev/null | head -1 || true)"
    if [ -z "$cid" ]; then
      warn "pangu 容器未在运行（cd $INSTALL_DIR && docker compose ps / logs --tail 50 查看）"
    else
      src="$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Source}}{{end}}{{end}}' "$cid" 2>/dev/null || true)"
      if [ -n "$src" ] && same_path "$src" "$INSTALL_DIR/data"; then
        ok "数据已落在宿主机 $INSTALL_DIR/data（容器重建不丢）"
      else
        warn "容器的 /app/data 没有挂到 $INSTALL_DIR/data：数据在容器层里，容器重建即丢——重跑 install.sh --mode=docker 会在重建前自动迁移"
      fi
      case "$(docker inspect -f '{{.HostConfig.LogConfig.Config}}' "$cid" 2>/dev/null || true)" in
        *max-size*) ok "容器日志有大小上限" ;;
        *) warn "容器日志没有大小上限：只增不减，迟早写满磁盘——重跑 install.sh --mode=docker 自动加上" ;;
      esac
      ( cd "$INSTALL_DIR" && docker compose ps 2>/dev/null ) | sed 's/^/    /' || true
    fi
    old="$(docker images --format '{{.Repository}}:{{.Tag}}  {{.Size}}' 2>/dev/null | grep -i '/pangu:' | sed 's/^/    /' || true)"
    if [ -n "$old" ]; then printf '  本机镜像（旧版本确认不回滚后可 docker rmi 回收磁盘）:\n%s\n' "$old"; fi
  else
    case "$(systemctl is-active "$SERVICE" 2>/dev/null || true)" in
      active) ok "服务 $SERVICE 在运行" ;;
      *) warn "服务 $SERVICE 不在运行（systemctl status $SERVICE / journalctl -u $SERVICE -n 50）" ;;
    esac
    crashes="$(journalctl -u "$SERVICE" -n 200 --no-pager 2>/dev/null | grep -c '异常退出' || true)"
    if [ "${crashes:-0}" -gt 0 ]; then
      warn "最近 200 行日志里有 ${crashes} 次「异常退出」：多半是配置问题（数据库口令 / 端口 / 依赖），看 journalctl -u $SERVICE"
    fi
  fi
  if [ "$IS_MAC" != "1" ]; then host_report; fi
  security_report
  exit 0
}

# host_report：宿主机侧的只读体检（Linux only）。run_doctor 与每次装完都会跑——
# 「调了没有、生效没有」必须能验证，而不是装的时候打一行「已调优」就算数。
host_report(){
  printf '  内存: %sM（swap %sM）  根分区可用: %sM\n' "$(mem_mb_now)" "$(swap_mb_now)" "$(fs_avail_mb /)"
  if [ "$(swap_mb_now)" -eq 0 ]; then
    warn "没有 swap：内存峰值超出物理内存时进程会被直接杀掉（sudo ./install.sh --tune 自动创建）"
  fi
  kernel_report
  if grep -rqs '^[[:space:]]*SystemMaxUse' /etc/systemd/journald.conf /etc/systemd/journald.conf.d 2>/dev/null; then
    ok "系统日志（journald）有总量上限"
  else
    warn "系统日志（journald）没有总量上限（默认可吃到文件系统的 10%）"
  fi
  if systemctl is-enabled pangu-backup.timer >/dev/null 2>&1; then
    local last
    last="$(ls -1t "$INSTALL_DIR"/backups/db-*.sql.gz 2>/dev/null | head -1 || true)"
    if [ -n "$last" ]; then
      ok "自动备份已启用，最近一份: $last（$(du -h "$last" 2>/dev/null | cut -f1)）"
    else
      ok "自动备份已启用（每日 03:30 首次触发；想立刻验一次：$INSTALL_DIR/backup.sh）"
    fi
  else
    warn "未启用自动备份：PostgreSQL 是唯一存储，丢了无法重建（sudo ./install.sh --backup 开启）"
  fi
  if [ -d "$INSTALL_DIR/backups" ]; then
    info "备份与数据在同一台机器上：防误删 / 误升级 / 坏迁移，不防整盘故障——重要部署请定期把 $INSTALL_DIR/backups 同步到别处或做云快照"
  fi
}

# ── 资源前置检查（只提醒不阻断）────────────────────
mem_kb="$(awk '/MemTotal/{print $2}' /proc/meminfo 2>/dev/null || echo 0)"
swap_kb="$(awk '/SwapTotal/{print $2}' /proc/meminfo 2>/dev/null || echo 0)"
# IS_UPGRADE：安装目录里已经有部署产物 → 按升级处理（升级前自动备份数据库）。
IS_UPGRADE=0
if [ -f "$INSTALL_DIR/config.json" ] || [ -f "$INSTALL_DIR/docker-compose.yml" ]; then IS_UPGRADE=1; fi
if [ "${mem_kb:-0}" -gt 0 ] && [ "$mem_kb" -lt 3800000 ]; then
  warn "内存 < 4G：多 worker 并发 + 压缩器会吃紧（建议 4C8G 起）"
fi
if [ "$IS_MAC" != "1" ] && [ "${swap_kb:-0}" -eq 0 ] && [ "$mem_kb" -gt 0 ]; then
  if [ "$NO_TUNE" = "1" ]; then
    warn "没有 swap 且宿主调优已关闭：Chromium / LibreOffice 的内存峰值超出物理内存时进程会被直接杀掉"
  else
    info "没有 swap：将在「宿主机调优」阶段自动创建"
  fi
fi
if [ "$IS_MAC" != "1" ]; then
  # 时间不同步时，任务时间戳、报告、周期调度、LLM 接口签名全都不可信
  if command -v timedatectl >/dev/null 2>&1 && [ "$(timedatectl show -p NTPSynchronized --value 2>/dev/null || true)" = "no" ]; then
    warn "系统时间未与 NTP 同步（timedatectl 查看；apt install systemd-timesyncd 或 chrony）"
  fi
  # 不是「可能会出事」，是「已经出过事」
  _oom="$(dmesg 2>/dev/null | grep -ci 'out of memory' || true)"
  if [ -n "$_oom" ] && [ "$_oom" -gt 0 ]; then
    warn "内核日志里有 ${_oom} 条 OOM 记录：这台机器已经发生过内存耗尽（dmesg -T | grep -i oom 看是哪个进程）"
  fi
fi
disk_avail="$(df -Pk / 2>/dev/null | awk 'NR==2{print $4}')"
if [ -n "${disk_avail:-}" ] && [ "$disk_avail" -lt 10485760 ]; then
  warn "根分区可用 < 10G：装工具/SecLists(约2.5G)/nuclei 模板/chromium 可能不够（建议 ≥40G）"
fi
if [ "$IS_UPGRADE" = "1" ]; then
  info "检测到既有部署：按升级处理（升级前自动备份数据库，配置与数据保留）"
fi
if command -v python3 >/dev/null 2>&1; then
  pyver="$(python3 -c 'import sys;print("%d.%d"%sys.version_info[:2])' 2>/dev/null || echo "0.0")"
  case "$pyver" in
    3.10|3.11|3.12|3.13|3.14) ;;
    *) warn "python3 版本 ${pyver}：内置 FOFA MCP 需要 ≥3.10（不影响主服务）" ;;
  esac
else
  warn "未检测到 python3：内置 FOFA MCP 需要 Python ≥3.10（不影响主服务）"
fi

# ── --doctor：到此为止，只体检不安装（在任何交互 / 下载之前退出）────
if [ "$DOCTOR" = "1" ]; then
  run_doctor
fi

# ── 出网代理：交互式配置 + 自动验证 ────────────────
#
# 为什么交互式：装机最常卡住的就是「这台机器连不上 GitHub / apt 源」。
# 让用户现场填一个标准格式的代理（http / https / socks5 / socks5h），
# 脚本自己验证通不通，验证过了再走流程——比让人事后自己去翻日志找
# "curl: (7) Failed to connect" 快得多。
#
# 填了代理后所有出网路径统一走它：apt / curl / npm / git / pip / go install。
# 验证不过时可以选择重填或跳过（跳过=直连，后面该失败的会失败）。
normalize_proxy(){
  local p="$1"
  p="$(printf '%s' "$p" | tr -d ' \t"'"'"'' | sed 's|/*$||')"
  [ -z "$p" ] && return 0
  # 裸 host:port → 默认按 http 处理（绝大多数本地代理是 http 混合端口）
  case "$p" in
    *://*) : ;;
    *) p="http://$p" ;;
  esac
  printf '%s' "$p"
}

# proxy_scheme_ok 校验协议：只有 http/https/socks5/socks5h 有效。
proxy_scheme_ok(){
  case "$1" in
    http://*|https://*|socks5://*|socks5h://*) return 0 ;;
    *) return 1 ;;
  esac
}

# proxy_has_hostport 校验 host:port 齐全。
proxy_has_hostport(){
  local rest="${1#*://}"
  rest="${rest%%/*}"
  case "$rest" in
    *@*) rest="${rest#*@}" ;;
  esac
  case "$rest" in
    *:*) [ -n "${rest%%:*}" ] && [ -n "${rest##*:}" ] ;;
    *) return 1 ;;
  esac
}

# proxy_curl_args 按协议拼出 curl 的代理参数。
# socks5:// 这种 URL 形式 curl 的 -x 不认（会报 "unsupported protocol
# scheme"），它要的是 --socks5-hostname host:port。硬塞 URL 的表现是
# 「填了 socks 代理但所有下载都失败」，而报错完全指不到真因。
#
# --socks5-hostname vs --socks5：前者把域名也交给代理解析（= socks5h），
# 后者在本地解析 DNS。国内环境里本地 DNS 常常解析不了境外域名，所以用前者。
proxy_curl_args(){
  local p="$1"
  case "$p" in
    socks5://*|socks5h://*)
      local _p="${p#*://}" _auth=""
      case "$_p" in
        *@*) _auth="${_p%%@*}"; _p="${_p#*@}" ;;
      esac
      _p="${_p%%/*}"
      printf '%s\n' --socks5-hostname "$_p"
      [ -n "$_auth" ] && printf '%s\n' --socks5-basic -U "$_auth"
      ;;
    *)
      printf '%s\n' -x "$p"
      ;;
  esac
}

# verify_proxy 拿代理真的发一个请求验证可用性。
# 用 GitHub API 做靶子：装机流程本来就要访问它，验证与用途一致。
verify_proxy(){
  local p="$1"
  local -a pargs=()
  while IFS= read -r line; do pargs+=("$line"); done < <(proxy_curl_args "$p")
  local code
  code="$(curl -fsSL "${pargs[@]}" -o /dev/null -w '%{http_code}' \
        --connect-timeout 8 --max-time 20 \
        https://api.github.com/repos/projectdiscovery/nuclei/releases/latest 2>/dev/null)" || code="000"
  case "$code" in
    200|301|302|403) return 0 ;;   # 403 = GitHub 限流，链路是通的
    *) return 1 ;;
  esac
}

# configure_proxy 交互式询问 + 规范化。
# 只做「问」与「格式校验」，验证单独放在 CURL 构造之后（verify_proxy 需要
# 按协议拼 curl 参数，而那依赖 INSTALL_PROXY 已定型）。
#
# 显式给了 --install-proxy 也走同一套校验：填错协议/端口是最常见的失败原因。
configure_proxy(){
  if [ -n "$INSTALL_PROXY" ]; then
    INSTALL_PROXY="$(normalize_proxy "$INSTALL_PROXY")"
    if ! proxy_scheme_ok "$INSTALL_PROXY"; then
      die "代理协议不支持: $INSTALL_PROXY（支持 http/https/socks5/socks5h）"
    fi
    if ! proxy_has_hostport "$INSTALL_PROXY"; then
      die "代理缺少 host:port: $INSTALL_PROXY"
    fi
    return 0
  fi

  # 非交互场景不打扰：直连。
  [ "$ASSUME_YES" = "1" ] && return 0
  [ "$CAN_PROMPT" = "0" ] && return 0

  printf '\n'
  printf '是否配置出网代理？\n'
  printf '  装机要访问 apt 源 / GitHub / npm / pypi；直连不通时在这里填一个代理最省事。\n'
  printf '  支持格式：http://host:port 、https://host:port 、socks5://host:port 、socks5h://host:port\n'
  printf '  带认证：http://user:pass@host:port ；留空 = 直连\n'
  printf '代理地址（留空直连）: '
  local ans=""
  read -r ans </dev/tty || ans=""
  ans="$(printf '%s' "$ans" | tr -d ' \t"'"'"'')"
  [ -z "$ans" ] && { info "使用直连"; return 0; }

  ans="$(normalize_proxy "$ans")"
  if ! proxy_scheme_ok "$ans"; then
    warn "协议不支持: $ans"
    warn "支持 http/https/socks5/socks5h"
    return 0
  fi
  if ! proxy_has_hostport "$ans"; then
    warn "缺少 host:port: $ans"
    return 0
  fi
  INSTALL_PROXY="$ans"
  return 0
}

configure_proxy

# ── curl 的代理参数：按协议分发 ───────────────────
#
# curl 不接受 socks5:// 这种 URL 形式的 -x（会报 "unsupported protocol
# scheme"），它要的是 --socks5-hostname host:port。照 URL 形式硬塞进去的
# 表现是「填了 socks 代理但所有下载都失败」，而报错完全指不到真因。
#
# --socks5-hostname vs --socks5：前者把域名也交给代理解析（= socks5h），
# 后者在本地解析 DNS。国内环境里本地 DNS 常常解析不了境外域名，所以用前者。
# --retry-all-errors：不加它，curl 的 --retry **只重试瞬时错误**（超时/429/5xx），
# SSL 中断（SSL_read: unexpected eof）这类传输错误直接失败——实测 2026-10-22
# 在一台真实服务器上经代理下载 17MB 发行包时，连接被切断后 curl 立刻放弃，
# 重试参数形同虚设。
#
# --retry-delay：重试之间退避，避免连着打同一个不稳的出口。
#
# 参数按 curl 版本分级（实测 CentOS 7 自带 curl 7.29 连 --retry 都没有，直接
# 报 "option --retry-all-errors: is unknown" 整条命令失败——这是老系统装机
# 第一步「获取最新版本号」就挂掉的原因）。降级不是最优但比装不上强：
#   - < 7.52：仅 -fsSL，无重试（老 curl 单次失败即见，网络问题显式暴露）；
#   - ≥ 7.52：+ --retry 5 --retry-delay 3 --retry-max-time 1800；
#   - ≥ 7.71：再 + --retry-all-errors（否则 --retry 只重试瞬时错误，
#     SSL 中断类传输错误直接失败——实测经代理下载 17MB 包时被切断后立刻放弃）。
curl_version() { curl --version 2>/dev/null | head -1 | awk '{print $2}'; }
version_ge() { [ "$(printf '%s\n' "$2" "$1" | sort -V | head -1)" = "$2" ]; }
_curl_ver=$(curl_version)
CURL=(curl -fsSL)
if version_ge "$_curl_ver" "7.52"; then
  CURL+=(--retry 5 --retry-delay 3 --retry-max-time 1800)
fi
if version_ge "$_curl_ver" "7.71"; then
  CURL+=(--retry-all-errors)
fi
if [ -n "$INSTALL_PROXY" ]; then
  while IFS= read -r _parg; do
    [ -n "$_parg" ] && CURL+=("$_parg")
  done < <(proxy_curl_args "$INSTALL_PROXY")
  info "安装期使用代理: $INSTALL_PROXY"

  # 代理必须验证过才能往下走：装机流程后面全靠它拉 GitHub 资产，
  # 代理不通时每一处下载都会失败并刷一堆看不懂的报错。
  printf '验证代理 ... '
  if verify_proxy "$INSTALL_PROXY"; then
    ok "代理可用"
  else
    printf '\n'
    warn "代理验证失败（GitHub API 不可达）"
    if [ "$ASSUME_YES" = "1" ] || [ "$CAN_PROMPT" = "0" ]; then
      die "代理不可用，中止安装（用 --install-proxy= 修正，或不传该参数走直连）"
    fi
    printf '仍然使用这个代理继续？(y/N) '
    local _ans=""; read -r _ans </dev/tty || _ans=""
    case "$_ans" in
      y|Y|yes|YES) : ;;
      *) die "已中止" ;;
    esac
  fi
fi

# 统一代理环境：git / npm / go / pip 这类工具只认 HTTP_PROXY/HTTPS_PROXY/
# ALL_PROXY，不认 curl 的 -x。导出后它们自动走同一个代理。
# socks5 用 ALL_PROXY（curl 那条路已单独处理，ALL_PROXY 给 git/npm 用）。
if [ -n "$INSTALL_PROXY" ]; then
  case "$INSTALL_PROXY" in
    socks5://*|socks5h://*)
      export ALL_PROXY="$INSTALL_PROXY"
      export all_proxy="$INSTALL_PROXY"
      ;;
    *)
      export HTTP_PROXY="$INSTALL_PROXY"  HTTPS_PROXY="$INSTALL_PROXY"
      export http_proxy="$INSTALL_PROXY"  https_proxy="$INSTALL_PROXY"
      ;;
  esac
  export NO_PROXY="localhost,127.0.0.1,::1"
  export no_proxy="$NO_PROXY"
fi

# systemd 单元里的代理环境行。hostd 是独立进程，不共享主服务环境，
# 所以主服务与 hostd 两个单元都要带这一行。
INSTALL_PROXY_ENV=""
if [ -n "$INSTALL_PROXY" ]; then
  case "$INSTALL_PROXY" in
    socks5://*|socks5h://*)
      INSTALL_PROXY_ENV="Environment=ALL_PROXY=${INSTALL_PROXY}"
      ;;
    *)
      INSTALL_PROXY_ENV="Environment=HTTP_PROXY=${INSTALL_PROXY}
Environment=HTTPS_PROXY=${INSTALL_PROXY}"
      ;;
  esac
fi

# 域名部署的环境预置：--domain 提供时假定走「反代 TLS」形态——
#   - PANGU_COOKIE_SECURE=1：HTTPS 下会话 cookie 才带 Secure；
#   - PANGU_TRUSTED_PROXIES：本机反代是最常见形态（默认 127.0.0.1）；反代在
#     其它机器时由交互追问（或 --trusted-proxy=）写入反代 IP——否则登录防
#     爆破按反代 IP 计数，所有用户会被一起锁。
TRUSTED_PROXY="${TRUSTED_PROXY:-127.0.0.1}"
# 域名形态的反代方案：caddy（脚本自动装并配置，推荐）/ existing（用户自备）/
# none（暂不配，先用 IP:8787 访问）。cookie Secure 跟随方案：caddy/existing=1，
# none=0（配好反代后再开）。
# DOMAIN_ENV（binary systemd 的域名形态环境变量）延迟到 ask_domain 之后组装——
# cookie Secure/受信代理的最终值要等反代方案选定（此前在此处组装会拿到初值）。
# PROXY_MODE/COOKIE_SECURE_VAL 的初始化在 install.env 加载前（见上方状态沿用段）。
DOMAIN_ENV=""

# ── 选择部署形态 ─────────────────────────────────
choose_mode(){
  if [ -n "$MODE" ]; then
    case "$MODE" in binary|docker) return ;; *) die "--mode 只能是 binary 或 docker"; esac
  fi
  if [ "$ASSUME_YES" = "1" ] || [ "$CAN_PROMPT" = "0" ]; then
    MODE="binary"
    info "非交互模式，默认使用二进制部署（--mode 可显式指定）"
    return
  fi
  printf '\n请选择部署形态：\n'
  printf '  1) 二进制部署（推荐）  单文件 + systemd + 系统 PostgreSQL，运行时零依赖\n'
  printf '  2) Docker 部署         容器编排，工具随镜像固定\n'
  printf '选择 [1]: '
  local pick=""
  read -r -t 60 pick </dev/tty || true
  case "${pick:-1}" in
    2|docker|D) MODE="docker" ;;
    *)          MODE="binary" ;;
  esac
  printf '\n'
}

# ── 询问访问形态（域名 vs IP）────────────────────────
# 只在交互模式且未用 --domain 显式指定时问。留空 = 按 IP:端口 直连（默认形态，
# 行为与不问时完全一致）。域名形态走「反代 TLS」：会预置
# PANGU_COOKIE_SECURE=1 / PANGU_TRUSTED_PROXIES=127.0.0.1（反代不在本机时改成
# 其网段），安装摘要按域名打印访问入口与核对清单。
choose_mode
# ── 询问访问形态（域名 vs IP）────────────────────────
# 只在交互模式且未用 --domain 显式指定时问。留空 = 按 IP:端口 直连（默认形态，
# 行为与不问时完全一致）。域名形态走「反代 TLS」：会预置
# PANGU_COOKIE_SECURE=1 / PANGU_TRUSTED_PROXIES=127.0.0.1（反代不在本机时改成
# 其网段），安装摘要按域名打印访问入口与核对清单。
ask_domain(){
  [ -n "$DOMAIN" ] && return 0          # --domain 已显式指定，不再问
  [ "$ASSUME_YES" = "1" ] && return 0   # 非交互模式
  [ "$CAN_PROMPT" = "0" ] && return 0   # 无 /dev/tty（纯管道无终端）
  printf '\n访问形态：将通过域名 + HTTPS 反代访问时填域名（如 pangu.example.com）\n'
  printf '  直接 http://IP:%s 访问则留空（回车默认 IP 直连）。\n' "${LISTEN_ADDR##*:}"
  printf '域名/留空 [IP 直连]: '
  local ans=""
  read -r -t 90 ans </dev/tty || ans=""
  ans="$(printf '%s' "$ans" | tr -d ' \t"'"'"'')"
  [ -z "$ans" ] && { info "按 IP:端口 直连形态安装（要改域名形态可重跑 install.sh --domain=NAME，幂等）"; return 0; }
  # 归一化：用户常把完整 URL 粘进来（http://xxx/）——剥 scheme 与路径，只留主机名。
  ans="${ans#*://}"
  ans="${ans%%/*}"
  # 粘的是 IP 或 IP:端口：那是「IP 直连」形态的意图，域名形态（反代 TLS/cookie
  # Secure）对 IP 无意义，按 IP 直连继续。
  if printf '%s' "$ans" | grep -Eq '^[0-9]+(\.[0-9]+){3}(:[0-9]+)?$'; then
    info "输入是 IP 地址，按 IP:端口 直连形态安装"
    return 0
  fi
  if ! printf '%s' "$ans" | grep -Eq '^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$'; then
    warn "域名格式不合法: $ans（只认主机名，如 pangu.example.com；中文域名请先用 punycode）——按 IP 直连继续"
    info "按 IP:端口 直连形态安装（要改域名形态可重跑 install.sh --domain=NAME，幂等）"
    return 0
  fi
  DOMAIN="$ans"
  # 反代方案三选一：默认自动 Caddy——「支持域名部署」的口径是装完即可用
  # https://域名 访问，而不是把配反代甩给用户。
  printf '\n域名反代方案：\n'
  printf '  1) 自动安装并配置 Caddy（推荐：同机反向代理自动 HTTPS，装完即可用域名访问）\n'
  printf '  2) 已有自己的反向代理（Pangu 只做端口收口与受信代理配置）\n'
  printf '  3) 暂不配反代（先用 http://IP:%s 访问，配好反代后再开 cookie Secure）\n' "${LISTEN_ADDR##*:}"
  printf '选择 [1]: '
  local pick=""
  read -r -t 60 pick </dev/tty || pick=""
  case "$(printf '%s' "$pick" | tr -d ' \t')" in
    2|existing)
      PROXY_MODE="existing"
      COOKIE_SECURE_VAL="1"
      # TRUSTED_PROXIES 错了会导致登录防爆破按反代 IP 计数——所有用户共享一个
      # 计数器，一人输错密码全员被锁。这里直接问清，写对值。
      printf '反向代理与 Pangu 同机吗？(Y/n，同机 = 默认 127.0.0.1) '
      local rp=""
      read -r -t 60 rp </dev/tty || rp=""
      case "$(printf '%s' "$rp" | tr -d ' \t' | tr 'A-Z' 'a-z')" in
        n|no)
          printf '反代所在机器 IP（如 10.0.0.9，回车 = 保持 127.0.0.1 装完手改 .env）: '
          local rip=""
          read -r -t 60 rip </dev/tty || rip=""
          rip="$(printf '%s' "$rip" | tr -d ' \t')"
          if printf '%s' "$rip" | grep -Eq '^[0-9]+(\.[0-9]+){3}$'; then
            TRUSTED_PROXY="$rip"
            ok "PANGU_TRUSTED_PROXIES 将设为 $rip"
          else
            warn "IP 不合法，保持 127.0.0.1（装完在 .env 里改 PANGU_TRUSTED_PROXIES）"
          fi
          ;;
        *) : ;;  # 同机（默认）
      esac
      ;;
    3|none)
      PROXY_MODE="none"
      COOKIE_SECURE_VAL="0"
      warn "暂不配反代：装完先用 http://IP:8787 访问（cookie Secure 已临时关）；配好反代后把 PANGU_COOKIE_SECURE 改 1 并重启服务"
      ;;
    *)
      PROXY_MODE="caddy"
      COOKIE_SECURE_VAL="1"
      ;;
  esac
  ok "域名部署形态: $DOMAIN（反代: $PROXY_MODE，cookie Secure=$COOKIE_SECURE_VAL，TRUSTED_PROXIES=$TRUSTED_PROXY）"
}
ask_domain
# 形态决策落盘（重跑沿用；显式 CLI 参数已覆盖，这里写最终值）
mkdir -p "$INSTALL_DIR"
{
  printf 'DOMAIN=%s\n' "$DOMAIN"
  printf 'TRUSTED_PROXY=%s\n' "$TRUSTED_PROXY"
  printf 'PROXY_MODE=%s\n' "$PROXY_MODE"
  if [ "$IS_MAC" = "1" ]; then
    # macOS 不做宿主调优/备份，不落开关（免得带到别的机器上产生误导）
    :
  else
    printf 'HOST_TUNE=%s\n' "$([ "$NO_TUNE" = "1" ] && echo 0 || echo 1)"
    printf 'HOST_BACKUP=%s\n' "$([ "$NO_BACKUP" = "1" ] && echo 0 || echo 1)"
  fi
} > "$INSTALL_STATE"
# DOMAIN_ENV 组装（deploy_binary 用）：此时反代方案/受信代理已是最终值。
if [ -n "$DOMAIN" ]; then
  DOMAIN_ENV="Environment=PANGU_COOKIE_SECURE=$COOKIE_SECURE_VAL
Environment=PANGU_TRUSTED_PROXIES=$TRUSTED_PROXY"
fi
ok "部署形态: $MODE  目标目录: $INSTALL_DIR"

# ── 解析版本 ─────────────────────────────────────
resolve_version(){
  # 注意：这里不能写成 `[ "$VERSION" = latest ] || return`——测试为假时 return 会带着
  # 测试的退出码 1 返回，set -e 会当场终止脚本。显式指定 --version 的调用必踩。
  if [ "$VERSION" != "latest" ]; then
    return 0
  fi
  VERSION="$("${CURL[@]}" "https://api.github.com/repos/$REPO/releases/latest" \
    | grep -oE '"tag_name": *"[^"]+"' | head -1 | cut -d'"' -f4)" || VERSION=""
  [ -n "$VERSION" ] || die "无法获取最新版本号（检查网络或用 --install-proxy 指定代理）"
}

# ── 下载并校验 Release 包（两种形态都要：二进制用程序，Docker 用 skills）──
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fetch_release(){
  resolve_version
  ok "目标版本: $VERSION"
  local zip="pangu-${VERSION#v}-${PKG_ARCH}.zip"
  info "下载 $zip"
  # 用原名落盘：SHA256SUMS 里写的就是这个名字，改名保存会让 sha256sum -c 找不到文件。
  #
  # -C - 是断点续传：17MB 的包经不稳的出口传输时断线是常态，续传比重下省时间
  # （实测一次安装因 SSL 中断白等了 40 分钟）。
  #
  # 下载失败必须在这里 die：原来不判就往下走，最后报「解压失败（需要 unzip）」
  # ——把网络问题误导成包损坏，用户会去查错方向。
  "${CURL[@]}" "https://github.com/$REPO/releases/download/$VERSION/$zip" -C - -o "$TMP/$zip" \
    || die "下载 $zip 失败（网络不通？代理不稳？可用 --install-proxy 指定代理）"

  # SHA256SUMS 是供应链校验的凭据：取不到就**没有校验**，此时继续装等于
  # 接受未经校验的二进制。原来只 warn 然后跳过校验——那是硬伤。
  "${CURL[@]}" "https://github.com/$REPO/releases/download/$VERSION/SHA256SUMS" -o "$TMP/SHA256SUMS" \
    || die "取不到 SHA256SUMS，无法校验 $zip（拒绝安装未经校验的二进制；可重试或用 --install-proxy 换出口）"
  ( cd "$TMP" && grep " $zip\$" SHA256SUMS | sha256sum -c - ) || die "SHA256 校验失败"
  ok "SHA256 校验通过"

  unzip -oq "$TMP/$zip" -d "$TMP/unpack" || die "解压失败（需要 unzip）"
}

# ══════════════════════════════════════════════════
# 二进制部署
# ══════════════════════════════════════════════════
deploy_binary(){
  if [ "$IS_MAC" = "1" ]; then deploy_binary_darwin; return; fi
  command -v systemctl >/dev/null 2>&1 || die "未检测到 systemd"
  command -v unzip   >/dev/null 2>&1 || { apt-get install -y unzip >/dev/null 2>&1 || dnf install -y unzip >/dev/null 2>&1 || die "需要 unzip"; }

  # 两项提前到下载之前：有任务在跑（用户可能选择中止）或端口被占（必然起不来）
  # 时，不该先让人白等一次下载。
  warn_running_tasks
  warn_port_busy "${LISTEN_ADDR##*:}" pangu

  fetch_release
  local src="$TMP/unpack/pangu-${VERSION#v}-${PKG_ARCH}"
  [ -d "$src" ] || die "发行包结构异常: 找不到 $src"

  mkdir -p "$INSTALL_DIR"
  # 程序/skills 覆盖；config.json / jwt.key / data 不在 zip 里，天然不被动
  # 二进制不能直接 cp 覆盖正在运行的进程（Linux 报 ETXTBSY "Text file busy"），
  # 先落到同目录临时名再 rename——rename 对运行中的可执行文件是合法的。
  # 这一步之前漏了，导致「重跑 install.sh 即升级」在服务运行状态下必然失败。
  for b in pangu pangu-hostd pangu-maint; do
    [ -f "$src/$b" ] || continue
    cp "$src/$b" "$INSTALL_DIR/.$b.new"
    chmod +x "$INSTALL_DIR/.$b.new"
    mv -f "$INSTALL_DIR/.$b.new" "$INSTALL_DIR/$b"
  done
  # 其余文件（skills / README / start.sh / config.example.json）直接覆盖
  for f in "$src"/*; do
    base="$(basename "$f")"
    case "$base" in pangu|pangu-hostd|pangu-maint) continue ;; esac
    cp -r "$f" "$INSTALL_DIR/"
  done
  chmod +x "$INSTALL_DIR/start.sh"
  ok "程序就绪: $INSTALL_DIR/pangu"

  # 运行用户
  if ! id "$RUN_USER" >/dev/null 2>&1; then
    useradd --system --home "$INSTALL_DIR" --shell /usr/sbin/nologin "$RUN_USER"
    ok "已创建系统用户 $RUN_USER"
  fi
  chown -R "$RUN_USER:$RUN_USER" "$INSTALL_DIR"

  # PostgreSQL + config.json（仅首次）
  if [ ! -f "$INSTALL_DIR/config.json" ]; then
    info "配置 PostgreSQL..."
    if ! command -v psql >/dev/null 2>&1; then
      # 这处 apt 必须走代理：apt 源在国内机器上常常要代理才拉得到，
      # 而 postgres 是硬依赖，装不上整条流程就断了。
      if command -v apt-get >/dev/null 2>&1; then
        local PP=()
        [ -n "$INSTALL_PROXY" ] && PP=(-o "Acquire::http::Proxy=$INSTALL_PROXY" -o "Acquire::https::Proxy=$INSTALL_PROXY")
        apt-get "${PP[@]}" update -qq && apt-get "${PP[@]}" install -y postgresql
      elif command -v dnf >/dev/null 2>&1; then
        # dnf --setopt=proxy="" 会被当成"代理指向空地址"而不是"不设代理"，
        # 所以只在确实有代理时才加这个参数。
        local DP=()
        [ -n "$INSTALL_PROXY" ] && DP=(--setopt=proxy="$INSTALL_PROXY")
        dnf "${DP[@]}" install -y postgresql-server && postgresql-setup --initdb
      else
        die "未识别的包管理器，请手动安装 PostgreSQL"
      fi
      systemctl enable --now postgresql
    fi
    systemctl is-active --quiet postgresql || systemctl start postgresql
    local DB_PASS
    DB_PASS="$(head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)"
    # 角色已存在时**必须同步密码**，不能只跳过。
    #
    # 踩过的坑（2026-10-22 真实安装）：机器上残留了旧的 pangu 角色（重装 /
    # 换端口 / 忘了删角色），脚本走「角色已存在 → 跳过创建」这条分支，
    # 于是 config.json 里写的是**新生成**的密码，而库里的角色还是**旧密码**
    # → 服务起来就反复 SASL 认证失败退出，而报错完全指不到真因。
    #
    # 同步密码是安全的：config.json 由本脚本独占写入（不覆盖已有安装），
    # 密码以它为准。角色已存在但 config.json 也在时，install.sh 根本不会
    # 走到这里（见上面的「仅首次」判断），所以不会误改既有安装的密码。
    if sudo -u "$PG_SUPER" psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='pangu'" | grep -q 1; then
      info "数据库角色 pangu 已存在，同步密码"
      sudo -u "$PG_SUPER" psql -c "ALTER ROLE pangu LOGIN PASSWORD '$DB_PASS'" >/dev/null
    else
      sudo -u "$PG_SUPER" psql -c "CREATE ROLE pangu LOGIN PASSWORD '$DB_PASS'"
    fi
    sudo -u "$PG_SUPER" psql -tAc "SELECT 1 FROM pg_database WHERE datname='pangu'" | grep -q 1 \
      || sudo -u "$PG_SUPER" createdb -O pangu pangu
    cat > "$INSTALL_DIR/config.json" <<JSON
{
  "database": {
    "host": "127.0.0.1",
    "port": 5432,
    "user": "pangu",
    "password": "$DB_PASS",
    "dbname": "pangu",
    "sslmode": "disable"
  }
}
JSON
    chown "$RUN_USER:$RUN_USER" "$INSTALL_DIR/config.json"
    chmod 600 "$INSTALL_DIR/config.json"
    ok "数据库 pangu 就绪（密码已写入 config.json，权限 600）"
  else
    ok "沿用既有 config.json（升级不覆盖）"
  fi

  # systemd 单元（加固版：够安全，但不再打断渗透工具）
  #
  # ProtectSystem=strict 必须配 ReadWritePaths，否则整个文件系统只读、服务写不了自己的
  # data 目录：流量索引(SQLite)、任务产物、证据、transcripts 会全部静默失败（日志刷
  # "attempt to write a readonly database"），页面「一键更新」也换不动二进制。
  #
  # 下面这组指令是逐条实测出来的，改动前请先读注释——这里踩过的坑都是"配上了但工具跑不了"：
  #   - MemoryDenyWriteExecute=true 会让 nuclei 启动即 panic（Go 的 sonic 加载器要做
  #     mprotect(PROT_EXEC)）。硬化收益远小于"扫描器不能用"，故不设。
  #   - 缺 CAP_NET_RAW 时 masscan 报 "PF_PACKET sockets not supported"、nmap -sS 报
  #     "requires root privileges"，agent 只能退回 -sT connect 扫描；RestrictAddressFamilies
  #     里也必须放行 AF_PACKET，否则连 raw socket 都建不出来。
  #   - 指令名是 SystemCallErrorNumber。写成不存在的 SystemCallErrorAction 时 systemd
  #     只会静默忽略（日志一行 "Unknown key ... ignoring"），超纲系统调用走默认动作
  #     SIGSYS 直接杀进程，而不是这里要的"干净拒绝"。
  #   - Environment=HOME 指到安装目录：ProtectHome + 家目录不可写会让 nuclei 的模板/缓存
  #     目录(~/nuclei-templates 等)写不进去。
  # 注：--dir 若指向 /home 或 /root 底下，ProtectHome=true 会把它整个藏起来，需自行去掉该项。
  cat > "/etc/systemd/system/${SERVICE}.service" <<UNIT
[Unit]
Description=PANGU - AI Autonomous Penetration Testing System
Documentation=https://github.com/${REPO}
After=network-online.target postgresql.service
Wants=network-online.target postgresql.service
# 反复失败要能被 systemd 判为 failed，而不是无限重启。
#
# start.sh 自己也有崩溃次数上限（连续 5 次异常退出就放弃自拉起并非 0 退出），
# 但那只是第一道防线：systemd 的 Restart=always 会立刻再拉起 start.sh，
# 于是又变成「status 显示 active (running)、实际全挂」的状态（start.sh 活着，
# pangu 本体在崩）。这里的 StartLimit 是第二道：达到上限后单元真正进入
# failed，运维一眼能看出服务是坏的，而不是被「running」骗过去。
StartLimitIntervalSec=300
StartLimitBurst=5

[Service]
Type=simple
User=${RUN_USER}
WorkingDirectory=${INSTALL_DIR}
Environment=HOME=${INSTALL_DIR}
ExecStart=${INSTALL_DIR}/start.sh -addr ${LISTEN_ADDR} -proxy ${PROXY_ADDR}
# 装机代理：宿主环境页的「一键安装」和后台情报同步用。hostd 是独立进程，
# 不共享主服务环境，所以它自己的单元也要带（见下方 hostd 单元）。
${INSTALL_PROXY_ENV}
${DOMAIN_ENV}
Restart=always
RestartSec=5
# 安全加固
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=${INSTALL_DIR}
ProtectHome=true
ProtectControlGroups=true
ProtectKernelModules=true
ProtectKernelTunables=true
RestrictNamespaces=true
RestrictRealtime=true
LockPersonality=true
# AF_PACKET/AF_NETLINK：masscan 与 nmap 的原始套接字要它们
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_PACKET AF_NETLINK
SystemCallFilter=@system-service
SystemCallErrorNumber=EPERM
# 不是 root，但给足原始套接字能力：masscan / ping / AF_PACKET 直接用；nmap 按 euid 判断，
# 特权扫描需 --privileged（提示词里已写明）。CAP_NET_BIND_SERVICE 供 --listen 用低位端口。
# 注：加了能力就不该再加 NoNewPrivileges 之外的提权面；这三项是渗透扫描的最小集。
CapabilityBoundingSet=CAP_NET_RAW CAP_NET_ADMIN CAP_NET_BIND_SERVICE
AmbientCapabilities=CAP_NET_RAW CAP_NET_ADMIN CAP_NET_BIND_SERVICE
# 文件句柄上限：nuclei/httpx/ffuf 都是句柄大户，systemd 默认软限 1024 在高并发
# 探测时会让扫描器自己报 too many open files。
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable "$SERVICE" 2>/dev/null || true
  # 升级场景下服务可能已经在跑：enable --now 不会重启它，必须显式 restart
  # 才会用上新换的二进制。重启之前先留一份库备份：新二进制起来会跑 schema
  # 迁移，这是整个升级流程里唯一不可回退的一步。
  if [ -f "$INSTALL_DIR/config.json" ]; then backup_db_now "pre-upgrade"; fi
  systemctl restart "$SERVICE"
  ok "systemd 服务已启动（${SERVICE}）"

  # 内置 MCP 服务器随包落地（FOFA 资产测绘），并把 uv 备好，预设开箱即用。
  if [ -d "$INSTALL_DIR/mcp-servers/fofa" ]; then
    ok "内置 MCP 服务器已安装（$INSTALL_DIR/mcp-servers/fofa）"
  fi
  ensure_uv

  if [ "$WITH_INSTALLER" = "1" ]; then install_hostd; fi
}

# ── 把代理写进运行期设置 ─────────────────────────
# 装机时代理只作用于脚本自身；Web「系统 → 宿主环境」页的「一键安装」和
# 后台的情报同步走的是数据库里的 install_proxy 设置。两者不联动的话，
# 用户装完机器后还得再手填一遍代理——正是用户说的「所有该走代理的就走代理」。
#
# 走数据库而不是 HTTP API：设置接口要 JWT，而管理员密码是用户首次访问
# 时自己设的，install.sh 拿不到。settings 表结构简单（key/value），
# 直接 UPSERT 最省事。
# ══════════════════════════════════════════════════
# 二进制部署 · macOS（launchd + Homebrew PostgreSQL）
# ══════════════════════════════════════════════════
# 与 Linux 版的差异（其余流程——下载/校验/解压/config.json/一键更新——完全复用）：
#   - 包管理器 apt/dnf → Homebrew；PostgreSQL 走 postgresql@16 formula，
#     superuser 就是当前用户（brew 不建 postgres 系统用户），psql 直连即可；
#   - 服务化 systemd → launchd：LaunchAgents plist 拉 start.sh（KeepAlive）。
#     start.sh 本身就是守护脚本（崩溃退避重拉 + 一键更新换装都在里面），
#     launchd 只负责「开机自启 + 意外退出拉起」，不重复实现守护逻辑；
#   - 不装 pangu-hostd（apt 体系工具安装不可用），不建系统用户；
#   - 全程当前用户，不需要 sudo（sudo 反而会破坏 brew/launchd 的一致性）。
deploy_binary_darwin(){
  # Homebrew：没有就装（官方安装器，需要交互确认一次密码给 /usr/local 写权限的
  # Intel 机器；Apple Silicon 装到 /opt/homebrew 不需要密码）
  if ! command -v brew >/dev/null 2>&1; then
    warn "未检测到 Homebrew，先安装它（可能要求输入密码/回车确认）..."
    /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"       || die "Homebrew 安装失败；也可手动安装后重跑本脚本"
    eval "$(/opt/homebrew/bin/brew shellenv 2>/dev/null || true)"
    eval "$(/usr/local/bin/brew shellenv 2>/dev/null || true)"
  fi
  command -v brew >/dev/null 2>&1 || die "brew 不可用（PATH 未包含？）"
  local BREW_PREFIX
  BREW_PREFIX="$(brew --prefix)"
  export PATH="$BREW_PREFIX/bin:$BREW_PREFIX/opt/postgresql@16/bin:$PATH"

  command -v unzip >/dev/null 2>&1 || brew install unzip >/dev/null 2>&1 || die "需要 unzip"

  # PostgreSQL 16（硬依赖）。按 formula 是否安装来判断，而不是 command -v psql：
  # 机器上可能有别的 psql（旧版 formula / 系统 CLI），会让我们误以为 @16 已就位。
  if ! brew list --formula 2>/dev/null | grep -q "^postgresql@16$"; then
    info "安装 PostgreSQL 16（brew，首次几分钟）..."
    brew install postgresql@16 || die "postgresql@16 安装失败"
  fi
  export PATH="$BREW_PREFIX/opt/postgresql@16/bin:$PATH"
  command -v psql >/dev/null 2>&1 || die "psql 不在 PATH（postgresql@16 是 keg-only，脚本已显式加 PATH，不应走到这）"
  if ! brew services list 2>/dev/null | grep -q "postgresql@16.*started"; then
    info "启动 PostgreSQL 服务（brew services = launchd）..."
    brew services start postgresql@16 || die "启动 PostgreSQL 失败"
  fi
  local i
  for i in $(seq 1 30); do pg_isready -q >/dev/null 2>&1 && break; sleep 2; done
  pg_isready -q >/dev/null 2>&1 || die "PostgreSQL 60s 内未就绪（brew services list 看状态）"
  ok "PostgreSQL 就绪"
  # psql 不带 -d 时默认连「与当前用户同名」的库（通常不存在）——显式钉到 postgres
  export PGDATABASE=postgres

  fetch_release
  local src="$TMP/unpack/pangu-${VERSION#v}-${PKG_ARCH}"
  [ -d "$src" ] || die "发行包结构异常: 找不到 $src"

  mkdir -p "$INSTALL_DIR"
  for b in pangu pangu-hostd pangu-maint; do
    [ -f "$src/$b" ] || continue
    cp "$src/$b" "$INSTALL_DIR/.$b.new"
    chmod +x "$INSTALL_DIR/.$b.new"
    mv -f "$INSTALL_DIR/.$b.new" "$INSTALL_DIR/$b"
  done
  local f base
  for f in "$src"/*; do
    base="$(basename "$f")"
    case "$base" in pangu|pangu-hostd|pangu-maint) continue ;; esac
    cp -r "$f" "$INSTALL_DIR/"
  done
  chmod +x "$INSTALL_DIR/start.sh"
  ok "程序就绪: $INSTALL_DIR/pangu"

  # config.json（仅首次；升级不覆盖）
  if [ ! -f "$INSTALL_DIR/config.json" ]; then
    local DB_PASS
    DB_PASS="$(head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)"
    # brew 的 PostgreSQL superuser 就是当前用户，无需 sudo -u postgres
    if psql -tAc "SELECT 1 FROM pg_roles WHERE rolname='pangu'" 2>/dev/null | grep -q 1; then
      info "数据库角色 pangu 已存在，同步密码"
      psql -c "ALTER ROLE pangu LOGIN PASSWORD '$DB_PASS'" >/dev/null
    else
      psql -c "CREATE ROLE pangu LOGIN PASSWORD '$DB_PASS'" >/dev/null
    fi
    psql -tAc "SELECT 1 FROM pg_database WHERE datname='pangu'" 2>/dev/null | grep -q 1       || createdb -O pangu pangu
    cat > "$INSTALL_DIR/config.json" <<JSON
{
  "database": {
    "host": "127.0.0.1",
    "port": 5432,
    "user": "pangu",
    "password": "$DB_PASS",
    "dbname": "pangu",
    "sslmode": "disable"
  }
}
JSON
    chmod 600 "$INSTALL_DIR/config.json"
    ok "数据库 pangu 就绪（密码已写入 config.json，权限 600）"
  else
    ok "沿用既有 config.json（升级不覆盖）"
  fi

  # launchd 用户级服务（开机自启 + KeepAlive 崩溃拉起）
  # 拆开声明：local 是命令，同一行 `local A=1 B=$A` 的 $A 在 A 赋值前展开，
  # set -u 下直接报 unbound variable
  local PLIST_LABEL="com.pangu"
  local PLIST_DIR="$HOME/Library/LaunchAgents"
  local PLIST="$PLIST_DIR/$PLIST_LABEL.plist"
  mkdir -p "$PLIST_DIR"
  cat > "$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$PLIST_LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>$INSTALL_DIR/start.sh</string>
  </array>
  <key>WorkingDirectory</key><string>$INSTALL_DIR</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$INSTALL_DIR/pangu.launchd.log</string>
  <key>StandardErrorPath</key><string>$INSTALL_DIR/pangu.launchd.log</string>
</dict>
</plist>
PLIST
  launchctl bootout "gui/$(id -u)/$PLIST_LABEL" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$PLIST" || die "launchd 注册失败（$PLIST）"
  ok "launchd 服务已注册（开机自启，崩溃自动拉起）"

  info "可选依赖（按需自装，不影响核心功能）："
  info "  brew install libreoffice --cask   # Word/证据渲染（装完 ln -sf "/Applications/LibreOffice.app/Contents/MacOS/soffice" "$BREW_PREFIX/bin/soffice"）"
  info "  brew install poppler imagemagick  # PDF/图片预览"
  info "  brew install nmap sqlmap nuclei ffuf ...  # 渗透工具按需装，缺失项在「宿主环境」页如实显示"
}

persist_install_proxy(){
  [ -n "$INSTALL_PROXY" ] || return 0
  [ -f "$INSTALL_DIR/config.json" ] || { warn "找不到 config.json，跳过写入运行期代理"; return 0; }
  command -v psql >/dev/null 2>&1 || { warn "没有 psql，跳过写入运行期代理"; return 0; }

  # 整个写入过程交给 Python 完成——不用 bash 变量在中间传递连接参数。
  #
  # 之前拆成两步（bash eval 读 config.json → 再传给 psql）踩了两个坑：
  #   1. `eval "$(python3 ... <<'HEREDOC' ... HEREDOC
  #      )"` 这种「heredoc 套在 $() 里」的写法，heredoc 体与命令替换的边界
  #      在 bash 里很难伺候——实测 eval 静默失败、db_pass 变空串，于是
  #      psql 连不上、整个写入失败而外层只看到「写入失败」。
  #   2. psql 的 `:'proxy'` 变量替换在 `-c` 参数里**不生效**（那是 -f 读文件
  #      或交互式会话才有的路径），会报 "syntax error at or near :"。
  # 现在：Python 读 config.json 取连接参数、拼好并转义 SQL、调用 psql。
  if python3 - "$INSTALL_DIR/config.json" "$INSTALL_PROXY" <<'PYEOF'
import json, os, subprocess, sys

cfg_path, proxy = sys.argv[1], sys.argv[2]
try:
    d = json.load(open(cfg_path)).get("database", {}) or {}
except Exception:
    d = {}
host = str(d.get("host") or "127.0.0.1")
port = str(d.get("port") or 5432)
user = str(d.get("user") or "pangu")
dbname = str(d.get("dbname") or "pangu")
password = str(d.get("password") or "")

# SQL 字符串字面量转义:单引号翻倍。代理地址已过 URL 校验(仅 http/https/socks5
# 且必须带 host:port),不含控制字符。
lit = "'" + proxy.replace("'", "''") + "'"
sql = ("INSERT INTO settings(key, value) VALUES ('install_proxy', %s) "
       "ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()" % lit)

env = dict(os.environ)
if password:
    env["PGPASSWORD"] = password
p = subprocess.run(
    ["psql", "-h", host, "-p", port, "-U", user, "-d", dbname,
     "-v", "ON_ERROR_STOP=1", "-c", sql],
    env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
)
sys.exit(p.returncode)
PYEOF
  then
    ok "运行期装机代理已写入（宿主环境页的一键安装会沿用）"
  else
    warn "写入运行期装机代理失败，可在 Web「系统 → 宿主环境」页手动填一次"
  fi
}

# ── 特权安装助手 pangu-hostd ─────────────────────
# 主服务单元带 NoNewPrivileges + ProtectSystem=strict,进程无法提权、/usr 只读,
# 所以「一键安装」做不了——必须在外面用一个 root 助手。它只监听一个 Unix socket,
# 且只认 hosttools 里的白名单工具 id,不接受自由命令。
install_hostd(){
  if [ ! -f "$INSTALL_DIR/pangu-hostd" ]; then
    warn "发行包里没有 pangu-hostd（旧版本包），跳过；体检仍可用，一键安装不可用"
    return 0
  fi
  # 同上：目标可能是正在运行的助手，先落临时名再 rename 避免 ETXTBSY。
  install -m 0755 "$INSTALL_DIR/pangu-hostd" /usr/local/bin/.pangu-hostd.new
  mv -f /usr/local/bin/.pangu-hostd.new /usr/local/bin/pangu-hostd
  cat > "/etc/systemd/system/${SERVICE}-hostd.service" <<UNIT
[Unit]
Description=PANGU host tool installer (privileged helper)
Documentation=https://github.com/${REPO}
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
ExecStart=/usr/local/bin/pangu-hostd -socket /run/pangu/hostd.sock -socket-group ${RUN_USER}
${INSTALL_PROXY_ENV}
Restart=always
RestartSec=5
# /run/pangu 由 systemd 建好并随停止清理;socket 本身被助手下调成 0660 root:${RUN_USER}
RuntimeDirectory=pangu
RuntimeDirectoryMode=0755
# 助手要调 apt/dnf、写 /usr/local/bin,所以不能像主服务那样把根挂成只读。
#
# 这里**刻意不设 NoNewPrivileges**:实测它会连带把 CAP_SETUID/CAP_SETGID 从进程的
# 有效能力集里摘掉(CapEff 末位由 ff 变 7f),而 apt 下载时要降权到 _apt 用户,
# 于是必然报 "seteuid 42 failed - Operation not permitted",所有 apt 类工具都装不上。
# 助手的真实安全边界是「socket 权限 + 代码里的工具白名单」,不是这条开关。
#
# CapabilityBoundingSet 显式列出 apt/dpkg/维护脚本需要的项(顺带挡掉 CAP_SYS_ADMIN、
# CAP_SYS_PTRACE、CAP_NET_ADMIN 等安装用不到的)。若将来某个包的 postinst 需要更多,
# 按报错往里加即可。
PrivateTmp=true
ProtectHome=true
ProtectClock=true
ProtectHostname=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictNamespaces=true
RestrictRealtime=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
LockPersonality=true
# 刻意不设 SystemCallFilter：助手要以 root 调 apt/dpkg，而 dpkg 会执行包自带的任意
# 维护脚本，白名单永远赶不上它们的实际需要，一旦超纲就是 SIGSYS 硬杀——装到一半被
# 杀比安装报错更难排查。助手的真实安全边界是 socket 权限 + 代码里的工具白名单。
CapabilityBoundingSet=CAP_CHOWN CAP_DAC_OVERRIDE CAP_DAC_READ_SEARCH CAP_FOWNER CAP_FSETID CAP_KILL CAP_SETGID CAP_SETUID CAP_SETPCAP CAP_NET_BIND_SERVICE CAP_NET_RAW CAP_SYS_CHROOT CAP_MKNOD CAP_AUDIT_WRITE CAP_SETFCAP
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT
  systemctl daemon-reload
  systemctl enable "${SERVICE}-hostd" 2>/dev/null || true
  # 同主服务：已在跑时要显式 restart 才会换成新装的二进制。
  systemctl restart "${SERVICE}-hostd"
  ok "安装助手已启动（${SERVICE}-hostd）→ socket /run/pangu/hostd.sock"
}

# ══════════════════════════════════════════════════
# Docker 部署
# ══════════════════════════════════════════════════
deploy_docker(){
  command -v docker >/dev/null 2>&1 || die "未检测到 docker（安装见 https://docs.docker.com/engine/install/）"
  docker compose version >/dev/null 2>&1 || die "未检测到 docker compose 插件（安装 docker-compose-plugin）"
  docker info >/dev/null 2>&1 || die "Docker 守护进程未运行（systemctl start docker）"
  command -v unzip >/dev/null 2>&1 || { apt-get install -y unzip >/dev/null 2>&1 || dnf install -y unzip >/dev/null 2>&1 || die "需要 unzip"; }

  # 磁盘前置：镜像解压后 3.66GB，Docker 数据目录与系统盘同盘时，拉取中途把盘写满
  # 不只是安装失败——宿主机自己也会跟着出问题。低于 4G 直接拒绝。
  local docker_root avail
  docker_root="$(docker info -f '{{.DockerRootDir}}' 2>/dev/null || echo /var/lib/docker)"
  avail="$(fs_avail_mb "$docker_root")"
  if [ -n "$avail" ]; then
    if [ "$avail" -lt 4096 ]; then
      die "Docker 数据目录可用空间不足 4G（$docker_root 仅剩 ${avail}M）：镜像解压后 3.66GB。先清理（docker system prune / 删旧镜像）再装"
    fi
    if [ "$avail" -lt 20480 ]; then
      warn "Docker 数据目录可用 ${avail}M（手册口径：与数据卷同盘时至少留 20G，否则升级解压到一半会因空间不足失败）"
    fi
  fi
  # 两项提前到拉镜像之前：有任务在跑（用户可能选择中止）或端口被占（必然起不来）
  # 时，不该先让人白等一次 3.66G 的下载。
  warn_running_tasks
  local _pre_port; _pre_port="$(grep -E '^PANGU_UI_PORT=' "$INSTALL_DIR/.env" 2>/dev/null | cut -d= -f2)"
  warn_port_busy "${_pre_port:-8787}" docker-proxy

  # skills 要挂进容器：空目录挂载会覆盖镜像内置的 /app/skills，所以先从发行包取一份
  fetch_release
  local src="$TMP/unpack/pangu-${VERSION#v}-${PKG_ARCH}"
  mkdir -p "$INSTALL_DIR/data" "$INSTALL_DIR/skills"
  if [ ! -f "$INSTALL_DIR/skills/.keep" ] && [ -z "$(ls -A "$INSTALL_DIR/skills" 2>/dev/null)" ]; then
    [ -d "$src/skills" ] && cp -r "$src/skills/." "$INSTALL_DIR/skills/"
  fi
  touch "$INSTALL_DIR/skills/.keep"

  # .env（首次生成随机 PG 密码；升级不覆盖）
  if [ ! -f "$INSTALL_DIR/.env" ]; then
    local PG_PASS
    PG_PASS="$(head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)"
    # 容器时区跟随宿主机：否则任务时间戳、报告、周期调度全部是 UTC，比本地慢 8 小时
    local HOST_TZ=""
    HOST_TZ="$(host_timezone)"
    cat > "$INSTALL_DIR/.env" <<ENV
POSTGRES_USER=pangu
POSTGRES_PASSWORD=$PG_PASS
POSTGRES_DB=pangu
PANGU_IMAGE=$IMAGE
PANGU_TAG=$VERSION
PANGU_INSTALL_PROXY=$INSTALL_PROXY
PROXY_PORT=$PROXY_PORT
# UI 对外端口（compose 的 8787 映射宿主侧）。走 Cloudflare 代理时必须用
# 它支持的端口（80/443/2052/2083/2086/2087/8080/8880/8443），如 PANGU_UI_PORT=2052。
PANGU_UI_PORT=${PORT}
PANGU_LIMITS=$([ "$NO_LIMITS" = "1" ] && echo 0 || echo 1)
TZ=${HOST_TZ}
ENV
    chmod 600 "$INSTALL_DIR/.env"
    ok "已生成 $INSTALL_DIR/.env（含随机数据库密码，权限 600）"
  else
    ok "沿用既有 .env（升级不覆盖）"
    # 升级必校验 compose 插值必需变量：旧 .env（更早版本生成、手改丢行等）缺
    # POSTGRES_PASSWORD 时，docker compose 会在 parsing 阶段直接报错，卡在拉
    # 镜像之后极难定位。缺了就补随机密码——注意若 postgres 数据卷此前已用旧
    # 密码初始化过，POSTGRES_PASSWORD 改动不会重置卷内密码，需要用户把旧密码
    # 填回。
    if ! grep -Eq '^POSTGRES_PASSWORD=.+' "$INSTALL_DIR/.env"; then
      local PG_PASS_FIX
      PG_PASS_FIX="$(head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24)"
      # 空值行（POSTGRES_PASSWORD=）先删掉再追加，避免同名键出现两次。
      sed -i '/^POSTGRES_PASSWORD=$/d' "$INSTALL_DIR/.env"
      printf 'POSTGRES_PASSWORD=%s\n' "$PG_PASS_FIX" >> "$INSTALL_DIR/.env"
      warn ".env 缺 POSTGRES_PASSWORD，已补随机密码"
      warn "若 postgres 数据卷此前已用旧密码初始化（pangu 起来后连不上库），请把旧密码填回 .env"
    fi
    # 镜像与 tag 也要跟着本次安装走：.env 只在首次生成时写过 PANGU_TAG/PANGU_IMAGE，
    # 沿用不更新会让「升级」后 compose 仍拉旧版本镜像，白装。
    if grep -q '^PANGU_TAG=' "$INSTALL_DIR/.env"; then
      sed -i "s|^PANGU_TAG=.*|PANGU_TAG=$VERSION|" "$INSTALL_DIR/.env"
    else
      printf 'PANGU_TAG=%s\n' "$VERSION" >> "$INSTALL_DIR/.env"
    fi
    if grep -q '^PANGU_IMAGE=' "$INSTALL_DIR/.env"; then
      sed -i "s|^PANGU_IMAGE=.*|PANGU_IMAGE=$IMAGE|" "$INSTALL_DIR/.env"
    else
      printf 'PANGU_IMAGE=%s\n' "$IMAGE" >> "$INSTALL_DIR/.env"
    fi
    # 但代理要跟着本次安装走：它不是"首次生成"的密钥，升级时也该同步，
    # 否则用户改了代理后重跑安装，旧 .env 里的旧代理还在生效。
    if [ -n "$INSTALL_PROXY" ]; then
      if grep -q '^PANGU_INSTALL_PROXY=' "$INSTALL_DIR/.env"; then
        sed -i "s|^PANGU_INSTALL_PROXY=.*|PANGU_INSTALL_PROXY=$INSTALL_PROXY|" "$INSTALL_DIR/.env"
      else
        printf 'PANGU_INSTALL_PROXY=%s\n' "$INSTALL_PROXY" >> "$INSTALL_DIR/.env"
      fi
      ok "已同步 .env 里的装机代理"
    fi
    # 早期版本生成的 .env 没有时区与容器资源上限两键：补上（已有则不动）。
    if ! grep -q '^TZ=' "$INSTALL_DIR/.env"; then
      local _tz; _tz="$(host_timezone)"
      if [ -n "$_tz" ]; then printf 'TZ=%s\n' "$_tz" >> "$INSTALL_DIR/.env"; fi
    fi
    if grep -q '^PANGU_LIMITS=' "$INSTALL_DIR/.env"; then
      if [ "$NO_LIMITS" = "1" ]; then env_set "$INSTALL_DIR/.env" PANGU_LIMITS 0; fi
    else
      printf 'PANGU_LIMITS=%s\n' "$([ "$NO_LIMITS" = "1" ] && echo 0 || echo 1)" >> "$INSTALL_DIR/.env"
    fi
    # 照着 README 的 .env.example 抄、却没改口令的部署：这个口令在仓库里是公开的，
    # 拿到数据库口令可以通过 reset-password.sh 重置管理员密码。
    if grep -Eq '^POSTGRES_PASSWORD=(change-me-please|changeme|password|postgres|pangu|123456|admin)$' "$INSTALL_DIR/.env"; then
      warn ".env 里的 POSTGRES_PASSWORD 仍是示例值——数据库对知道这个值的人敞开，请改成强口令（openssl rand -base64 18）"
      warn "注意：postgres 数据卷一旦用旧口令初始化过，改 .env 不会重置卷内口令，需要同步 ALTER ROLE"
    fi
  fi
  # 域名部署：预置 HTTPS 相关变量（反代 TLS 下会话 cookie 才带 Secure）。
  # 幂等：升级重跑不重复追加、已有值不覆盖。TRUSTED_PROXY 来自交互追问或
  # --trusted-proxy=（默认 127.0.0.1 = 反代同机）。
  if [ -n "$DOMAIN" ] && ! grep -q '^PANGU_COOKIE_SECURE=' "$INSTALL_DIR/.env"; then
    printf 'PANGU_COOKIE_SECURE=%s\nPANGU_TRUSTED_PROXIES=%s\n' "$COOKIE_SECURE_VAL" "$TRUSTED_PROXY" >> "$INSTALL_DIR/.env"
    ok "已按域名部署预置 PANGU_COOKIE_SECURE=$COOKIE_SECURE_VAL / PANGU_TRUSTED_PROXIES=$TRUSTED_PROXY（反代方案: $PROXY_MODE）"
  fi

  local PORT="${LISTEN_ADDR##*:}" PROXY_PORT="${PROXY_ADDR##*:}"
  # 域名形态且反代方案为 caddy/existing：宿主端口绑 127.0.0.1（UI 只经反代
  # 访问，8787 不裸奔公网）；none = UI 直接对外（反代就绪前的过渡形态）。
  local UI_BIND=""
  if [ -n "$DOMAIN" ] && [ "$PROXY_MODE" != "none" ]; then
    UI_BIND="127.0.0.1:"
  fi
  # 容器资源上限：不给的话，一个扫描任务的内存峰值能把整机拖死（数据库、SSH 一起
  # 卡住）；给了则最坏只是该容器被杀。上限按主机内存给系统与数据库留出余量来定；
  # 主机有 swap 时允许容器用到 2 倍上限（「峰值不被硬杀」靠的就是 swap）。
  local limit_pangu="" limit_pg=""
  if ! grep -q '^PANGU_LIMITS=0' "$INSTALL_DIR/.env" 2>/dev/null; then
    local ram m
    ram="$(mem_mb_now)"
    if [ "$ram" -gt 0 ]; then
      m=$(( ram - 1536 ))
      [ "$m" -lt 1024 ] && m=1024
      limit_pangu="    mem_limit: ${m}m"
      if [ "$(swap_mb_now)" -gt 0 ]; then
        limit_pangu="$limit_pangu"$'\n'"    memswap_limit: $(( m * 2 ))m"
      fi
      # 进程数上限（线程也计入）：防某个工具进程泄漏 / fork 失控拖垮宿主机
      limit_pangu="$limit_pangu"$'\n'"    pids_limit: 4096"
      limit_pg="    mem_limit: 512m"
    fi
  fi
  cat > "$INSTALL_DIR/docker-compose.yml" <<COMPOSE
# 由 install.sh 生成。数据在 ./data，skills 在 ./skills，数据库在卷 pgdata。
# 日志上限 / 时区 / 健康检查 / 停机宽限 / 资源上限由 install.sh 按宿主机情况生成，
# 重跑 install.sh 会覆盖本文件；要长期自定义请新建 docker-compose.override.yml。
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: \${POSTGRES_USER:-pangu}
      POSTGRES_PASSWORD: \${POSTGRES_PASSWORD:?请在 .env 设置 POSTGRES_PASSWORD}
      POSTGRES_DB: \${POSTGRES_DB:-pangu}
    volumes:
      - pgdata:/var/lib/postgresql/data
    # 并行查询 / 大排序溢出到共享内存，默认 64M 偏小
    shm_size: 256mb
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U \${POSTGRES_USER:-pangu} -d \${POSTGRES_DB:-pangu}"]
      interval: 5s
      timeout: 3s
      retries: 10
    restart: unless-stopped
    # 容器日志只增不减：不封顶迟早写满磁盘（docker system df 还不统计这部分）
    logging:
      driver: json-file
      options:
        max-size: "20m"
        max-file: "3"
${limit_pg}

  pangu:
    image: \${PANGU_IMAGE}:\${PANGU_TAG}
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      PANGU_PG_DSN: postgres://\${POSTGRES_USER:-pangu}:\${POSTGRES_PASSWORD}@postgres:5432/\${POSTGRES_DB:-pangu}?sslmode=disable
      ANTHROPIC_API_KEY: \${ANTHROPIC_API_KEY:-}
      OPENAI_API_KEY: \${OPENAI_API_KEY:-}
      PANGU_LLM_PROVIDER: \${PANGU_LLM_PROVIDER:-}
      PANGU_LLM_MODEL: \${PANGU_LLM_MODEL:-}
      PANGU_LLM_BASE_URL: \${PANGU_LLM_BASE_URL:-}
      PANGU_LLM_PROXY: \${PANGU_LLM_PROXY:-}
      # 装机代理：宿主环境页的「一键安装」和后台情报同步用。Docker 形态没有
      # config.json，只能走环境变量；服务端读它作为数据库 setting 的兜底。
      PANGU_INSTALL_PROXY: \${PANGU_INSTALL_PROXY:-}
      # 时区跟随宿主机（.env 的 TZ）：否则任务时间戳 / 报告 / 周期调度全是 UTC
      TZ: \${TZ:-UTC}
    ports:
      # 对外端口从 .env 的 PANGU_UI_PORT 读（默认 8787）。走 Cloudflare 等
      # CDN 代理时改成其支持的端口（如 2052），改完 docker compose up -d 生效。
      # 域名形态绑定 127.0.0.1：UI 只经反代访问，8787 不裸奔公网（IP 形态才
      # 直接对外）。
      - "${UI_BIND}\${PANGU_UI_PORT:-8787}:8787"
      - "127.0.0.1:\${PROXY_PORT:-8788}:8788"
      # 回连端口映射（按需放开）：容器内 MSF/listener 监听的端口要映射到宿主，
      # agent 才能收到 reverse shell。示例：
      # - "4444-4450:4444-4450"
    volumes:
      - ./data:/app/data
      - ./skills:/app/skills
    restart: unless-stopped
    # 停机宽限：docker stop 默认 10 秒后 SIGKILL，在跑的 worker 这一步直接断掉。
    # 给足 60 秒让它写完手头的结果。
    stop_grace_period: 60s
    # 健康检查：start.sh 包装进程活着不等于 pangu 活着（本体反复崩溃时容器状态
    # 仍是 running）。`docker compose ps` 显示 healthy 才是真的起来了。
    healthcheck:
      test: ["CMD-SHELL", "curl -fsS -o /dev/null http://127.0.0.1:8787/api/health || exit 1"]
      interval: 15s
      timeout: 5s
      retries: 5
      start_period: 120s
    # 文件句柄：nuclei/httpx/ffuf 都是句柄大户
    ulimits:
      nofile:
        soft: 65535
        hard: 65535
    logging:
      driver: json-file
      options:
        max-size: "20m"
        max-file: "3"
${limit_pangu}

volumes:
  pgdata:
COMPOSE

  ok "已生成 $INSTALL_DIR/docker-compose.yml"
  info "拉取镜像 $IMAGE:$VERSION ..."
  ( cd "$INSTALL_DIR" && docker compose pull ) \
    || die "镜像拉取失败（网络 / 代理不通？或磁盘空间不足——df -h 看 Docker 数据目录）"

  # 升级前的三道保护，顺序不能换：
  #   ① 备份数据库——新版本起来会跑 schema 迁移，这是升级里唯一不可回退的一步；
  #   ② 旧容器的数据若还在容器层里，先迁到 ./data（否则 ③ 之后的重建会把它丢掉）；
  #   ③ Docker 守护进程的日志上限。
  if [ "$IS_UPGRADE" = "1" ]; then backup_db_now "pre-upgrade"; fi
  rescue_container_data
  if [ "$NO_TUNE" = "1" ]; then
    info "已按设置跳过 Docker 守护进程日志上限（--tune 可重新开启）"
  else
    tune_docker_daemon
  fi
  ( cd "$INSTALL_DIR" && docker compose up -d ) \
    || die "容器启动失败（上方有 docker 的报错；常见：端口被占 / 磁盘已满 / .env 的数据库口令与既有数据卷不一致）"
  ok "容器已启动"
  docker_post_checks
}

# rescue_container_data：升级时的数据抢救。
# 标准 compose 会把 ./data 挂到容器的 /app/data；但旧容器若是手工 docker run 起的、
# 或来自没写 volumes 的旧 compose，数据就全在容器可写层里——`compose up -d` 一重建
# 容器，tasks / traffic / 证据 / 会话全部随旧容器消失，而且没有任何报错。
# 所以重建之前核对一次：挂载不是「bind 到 ./data」就先把数据迁出来。
rescue_container_data(){
  local cid src
  cid="$(cd "$INSTALL_DIR" && docker compose ps -a -q pangu 2>/dev/null | head -1 || true)"
  [ -n "$cid" ] || return 0
  docker inspect "$cid" >/dev/null 2>&1 || return 0
  # Type:Source；没挂载时为空，匿名卷时 Source 是 /var/lib/docker/volumes/<id>/_data
  src="$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Type}}:{{.Source}}{{end}}{{end}}' "$cid" 2>/dev/null || true)"
  if [ "${src%%:*}" = "bind" ] && same_path "${src#*:}" "$INSTALL_DIR/data"; then return 0; fi
  warn "旧容器的 /app/data 没有挂载到 $INSTALL_DIR/data：数据在容器层里，容器一重建就丢。重建前先迁出来"
  # 先停再拷：一边写一边拷拿到的可能是写了一半的文件
  ( cd "$INSTALL_DIR" && docker compose stop pangu ) >/dev/null 2>&1 || true
  mkdir -p "$INSTALL_DIR/data"
  if [ "${src%%:*}" = "bind" ] && [ -n "${src#*:}" ]; then
    # 挂在别的宿主机路径上（手改过 compose）：从原路径拷过来
    cp -a "${src#*:}/." "$INSTALL_DIR/data/" || die "数据迁移失败，已中止（旧容器保留着，数据未丢）"
  else
    docker cp "$cid:/app/data/." "$INSTALL_DIR/data/" || die "数据抢救失败，已中止（旧容器保留着，数据未丢）"
  fi
  ok "已把旧容器数据迁移到 $INSTALL_DIR/data（$(du -sh "$INSTALL_DIR/data" 2>/dev/null | cut -f1)）——之后重建容器不再丢数据"
}

# docker_post_checks：启动后的核对——「以为挂上了」和「真的挂上了」要靠 inspect 验。
docker_post_checks(){
  local cid src old
  # 悬空镜像（<none>：被新版本顶掉的同名层，已无容器引用），纯占磁盘
  docker image prune -f >/dev/null 2>&1 || true
  # 旧版本镜像留着给回滚，只列不删（保留「当前 + 上一个」是部署手册的口径）
  old="$(docker images --format '{{.Repository}}:{{.Tag}}  {{.Size}}' 2>/dev/null | grep -i '/pangu:' | grep -v ":$VERSION " || true)"
  if [ -n "$old" ]; then
    info "本机还有旧版本镜像（留给回滚；确认不回滚后 docker rmi <tag> 回收磁盘）："
    printf '%s\n' "$old" | sed 's/^/    /'
  fi
  cid="$(cd "$INSTALL_DIR" && docker compose ps -q pangu 2>/dev/null | head -1 || true)"
  src="$(docker inspect -f '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Source}}{{end}}{{end}}' "$cid" 2>/dev/null || true)"
  if [ -n "$src" ] && same_path "$src" "$INSTALL_DIR/data"; then
    ok "数据已落在宿主机 $INSTALL_DIR/data（容器重建不丢；备份 / 迁移直接拷这个目录）"
  else
    warn "容器的 /app/data 没有挂到 $INSTALL_DIR/data：数据在容器层里，重建容器会丢——请把 docker inspect $cid --format '{{json .Mounts}}' 的输出发给维护者"
  fi
}

# ── 域名反代：自动安装并配置 Caddy（PROXY_MODE=caddy，两种形态通用）──
# 「支持域名部署」的口径：装完即可用 https://域名 访问。Caddy 同机反代到
# 127.0.0.1:PORT，DNS 解析就绪后自动签发 TLS 证书。Caddyfile 只追加不覆盖
# （幂等：已有该域名块时跳过，不毁用户其它站点配置）。
install_caddy(){
  local PORT="${LISTEN_ADDR##*:}"
  if [ "$IS_MAC" = "1" ]; then
    # macOS 走 launchd 体系，无 systemctl：自动 Caddy 仅支持 apt/dnf 形态。
    warn "macOS 暂不支持自动 Caddy，已按「自备反代」处理：请自行把 ${DOMAIN} 反代到 http://127.0.0.1:${PORT}（brew install caddy 也可），并保持 PANGU_COOKIE_SECURE=1"
    PROXY_MODE="existing"
    return 0
  fi
  if ! command -v caddy >/dev/null 2>&1; then
    info "安装 Caddy（反向代理，自动 HTTPS）..."
    if command -v apt-get >/dev/null 2>&1; then
      apt-get install -y caddy >/dev/null 2>&1 || { apt-get update -qq && apt-get install -y caddy; } || die "Caddy 安装失败（apt 源不可用？可用 --install-proxy 指定代理，或重跑选方案 2 自备反代）"
    elif command -v dnf >/dev/null 2>&1; then
      dnf install -y caddy || die "Caddy 安装失败（dnf 源不可用？）"
    else
      die "未识别的包管理器，请手动安装 Caddy 后重跑（或选 --reverse-proxy=existing 自备反代）"
    fi
  else
    ok "Caddy 已安装，直接配置"
  fi
  local CF="/etc/caddy/Caddyfile"
  [ -f "$CF" ] || printf '' > "$CF"
  if ! grep -q "^${DOMAIN} {" "$CF"; then
    printf '\n%s {\n    reverse_proxy 127.0.0.1:%s\n}\n' "$DOMAIN" "$PORT" >> "$CF"
    ok "已写入 Caddyfile：$DOMAIN → 127.0.0.1:$PORT"
  else
    ok "Caddyfile 已含 $DOMAIN 配置，跳过追加"
  fi
  systemctl enable caddy >/dev/null 2>&1 || true
  systemctl restart caddy || die "Caddy 启动失败（journalctl -u caddy 查看；80/443 被占用？）"
  ok "Caddy 已配置：https://$DOMAIN（DNS 解析就绪后自动签发 TLS 证书，无需手工操作）"
}

# ── --with-tools：宿主渗透工具 ───────────────────
install_tools(){
  if [ "$MODE" = "docker" ]; then
    warn "Docker 形态下工具固化在镜像里，不在运行时安装。"
    warn "需要更多工具时：基于官方镜像自建（见 README「Docker 部署」），或改用 --mode=binary。"
    return 0
  fi
  info "安装常用渗透工具（系统源内）..."
  local APT_PROXY=() DNF_PROXY=()
  if [ -n "$INSTALL_PROXY" ]; then
    APT_PROXY=(-o "Acquire::http::Proxy=$INSTALL_PROXY" -o "Acquire::https::Proxy=$INSTALL_PROXY")
    DNF_PROXY=(--setopt=proxy="$INSTALL_PROXY")
  fi
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get "${APT_PROXY[@]}" update -qq || warn "apt update 失败，继续尝试"
    # seclists 在 apt 源里没有稳定包名，单独克隆（见 install_wordlists）；
    # 其余一次装齐，缺的包不阻断整体（不同发行版源里包名有出入）。
    # imagemagick：报告证据拼图/文件名水印（v0.4.10 起依赖）；
    # libreoffice-writer/calc/impress + poppler-utils：文档证据转图片预览
    # （v0.4.21 起，Word/Excel/PPT→PDF→JPEG）。
    # 安装策略：先按组整装（快），整组失败再**逐包重试**。
    # 为什么必须逐包兜底：apt-get install 是原子事务，弱网下 200 个包里任何一个
    # 下载失败会让整组回滚——实测丢 git（script 类工具全挂 127）这类关键小包。
    apt_install() {
      apt-get "${APT_PROXY[@]}" install -y "$@" >/dev/null 2>&1 && return 0
      local p
      for p in "$@"; do
        apt-get "${APT_PROXY[@]}" install -y "$p" >/dev/null 2>&1 \
          || warn "  apt 缺包: $p"
      done
    }
    # 关键基础包优先（git/ca 证书这类缺失会让后续 script 安装全面失败）
    apt_install git jq perl ruby ca-certificates curl wget unzip openssl openssh-client netcat-openbsd
    # 渗透工具组
    apt_install nmap masscan sqlmap hydra whatweb nikto gobuster ffuf john \
      dnsutils whois nodejs npm python3-impacket gitleaks imagemagick \
      hashcat testssl.sh default-jre-headless apktool adb aapt awscli tcpdump sshuttle
    # 文档渲染组（体积大、弱网下最容易拖垮整组，单独一组）
    apt_install libreoffice-writer libreoffice-calc libreoffice-impress poppler-utils
  elif command -v dnf >/dev/null 2>&1; then
    dnf "${DNF_PROXY[@]}" install -y nmap masscan sqlmap hydra whatweb nikto john nmap-ncat bind-utils unzip \
      nodejs python3-impacket ImageMagick hashcat openssl tcpdump \
      libreoffice-writer libreoffice-calc libreoffice-impress poppler-utils \
      || warn "dnf 部分包缺失，已装可用项"
  else
    warn "未识别的包管理器，跳过系统源工具安装"
  fi
  ok "系统源工具安装完成"

  # 非系统源工具（Go 系 / GitHub Release），装不上的在 Web「系统 → 宿主环境」页按需装
  install_github_tools
  install_wordlists
  install_pip_tools
  install_browser_mcp
  install_fonts
  # catalog 预装（v0.4.68）：宿主助手按 90 项工具目录补齐上面未覆盖的
  # （移动逆向/内网穿透/二进制利用等），与 Web 一键安装同一套清单。
  install_catalog_tools
}

# ── catalog 预装：复用 hostd 的白名单配方与串行队列 ──────────
# 为什么放在最后一棒：apt 基础包与 GitHub 表驱动安装是验证最久的路径，
# catalog 只补它们没覆盖的工具（幂等：已可用的会跳过）。
install_catalog_tools(){
  local bin="/usr/local/bin/pangu-hostd"
  if [ ! -x "$bin" ]; then
    warn "  未安装 pangu-hostd，跳过 catalog 预装（Web 端仍可一键安装）"
    return 0
  fi
  info "按 catalog 预装缺失工具（首次较慢：apt/pip/GitHub 全量）..."
  local was_active=0
  if systemctl is-active --quiet pangu-hostd 2>/dev/null; then
    was_active=1
    systemctl stop pangu-hostd 2>/dev/null || true
  fi
  # 弱网下 GitHub/pip 抖动是常态：失败项自动重试三轮（每轮只重装缺失项，
  # 已装好的直接跳过），大幅提高无人值守安装的成功率。
  local attempt ok_any=0
  for attempt in 1 2 3; do
    if "$bin" -install-all; then
      ok "catalog 工具预装完成"
      ok_any=1
      break
    fi
    if [ "$attempt" -lt 3 ]; then
      warn "catalog 预装第 $attempt 轮存在失败项，30 秒后重试…"
      sleep 30
    fi
  done
  if [ "$ok_any" != "1" ]; then
    warn "catalog 工具预装仍有失败项；可在 Web「系统 → 宿主环境」逐项重试"
  fi
  [ "$was_active" = "1" ] && systemctl start pangu-hostd 2>/dev/null || true
}

# ── 公文字体：报告 PDF 渲染 ────────────────────────
# docx 里字体已内嵌（odttf），Word/WPS 打开不需要系统字体；但 LibreOffice
# 转 PDF 时读系统字体，新机器不装会 fallback。从仓库 raw 拉三件套（ASCII
# 文件名，TTF 内部 family 名仍是仿宋_GB2312 等，docx 引用按内部名匹配）。
# 下载失败只告警——不影响 Word 导出。
install_fonts(){
  local dir="/usr/share/fonts/gongwen"
  mkdir -p "$dir" || { warn "无法创建 $dir，跳过公文字体"; return 0; }
  local base="https://raw.githubusercontent.com/${REPO}/main/report/fonts"
  local f
  for f in fangsong_gb2312.ttf kaiti_gb2312.ttf fzxiaobiaosong_gbk.ttf; do
    if [ ! -s "$dir/$f" ]; then
      "${CURL[@]}" -fsSL "$base/$f" -o "$dir/$f" >/dev/null 2>&1         || { rm -f "$dir/$f"; warn "  公文字体 $f 下载失败（仅影响本机 docx→PDF）"; }
    fi
  done
  command -v fc-cache >/dev/null 2>&1 && fc-cache -f "$dir" >/dev/null 2>&1 || true
  ok "公文字体就位（$dir）"
}

# ── GitHub Release 单文件工具 ───────────────────
# 表驱动：repo|资产名模板|二进制名。模板占位符 {ver}(不带v) {verv}(带v)
# {arch}(amd64/arm64) {arch2}(x86_64/aarch64) {arch3}(x86/arm) {arch4}(x64/arm64)。
#
# 各项目的资产命名差异很大（架构在前/后、版本带不带 v、扩展名 .zip/.tar.gz/.gz），
# 所以模板必须逐个按实测资产名写，不能指望一个通式。
#
# 不用 `go install`：宿主未必有 Go 工具链，而且 go install 每次都要下模块。
# 不用 apt 源：这些工具的 apt 包要么没有要么版本极旧。
install_github_tools(){
  local GOBIN_DIR="/usr/local/bin"
  local arch="amd64" arch2="x86_64" arch3="x86" arch4="x64" lin="x86_64"
  if [ "$ARCH" = "aarch64" ]; then
    arch="arm64"; arch2="aarch64"; arch3="arm"; arch4="arm64"; lin="arm64"
  fi

  # repo|资产模板|二进制名
  local specs="
projectdiscovery/nuclei|nuclei_{ver}_linux_{arch}.zip|nuclei
projectdiscovery/httpx|httpx_{ver}_linux_{arch}.zip|httpx
projectdiscovery/subfinder|subfinder_{ver}_linux_{arch}.zip|subfinder
projectdiscovery/naabu|naabu_{ver}_linux_{arch}.zip|naabu
projectdiscovery/dnsx|dnsx_{ver}_linux_{arch}.zip|dnsx
projectdiscovery/katana|katana_{ver}_linux_{arch}.zip|katana
projectdiscovery/tlsx|tlsx_{ver}_linux_{arch}.zip|tlsx
projectdiscovery/notify|notify_{ver}_linux_{arch}.zip|notify
projectdiscovery/interactsh|interactsh-client_{ver}_linux_{arch}.zip|interactsh-client
gitleaks/gitleaks|gitleaks_{ver}_linux_{arch4}.tar.gz|gitleaks
jpillora/chisel|chisel_{ver}_linux_{arch}.gz|chisel
OWASP/Amass|amass_linux_{arch}.tar.gz|amass
hahwul/dalfox|dalfox-{verv}-linux-{arch2}.tar.gz|dalfox
epi052/feroxbuster|{arch2}-linux-feroxbuster.tar.gz|feroxbuster
OJ/gobuster|gobuster_Linux_{lin}.tar.gz|gobuster
"
  local spec repo tmpl bin tag ver url asset file tmpd
  for spec in $specs; do
    repo="$(printf '%s' "$spec" | cut -d'|' -f1)"
    tmpl="$(printf '%s' "$spec" | cut -d'|' -f2)"
    bin="$(printf '%s' "$spec" | cut -d'|' -f3)"
    if command -v "$bin" >/dev/null 2>&1; then
      ok "  $bin 已存在"
      continue
    fi
    tag="$("${CURL[@]}" "https://api.github.com/repos/$repo/releases/latest" \
      | grep -oE '"tag_name": *"[^"]+"' | head -1 | cut -d'"' -f4)" || tag=""
    if [ -z "$tag" ]; then
      warn "  $bin 无法解析最新版本（GitHub API 受限，可加 --install-proxy）"
      continue
    fi
    ver="${tag#v}"
    asset="$tmpl"
    asset="${asset//\{ver\}/$ver}"
    asset="${asset//\{verv\}/$tag}"
    asset="${asset//\{arch\}/$arch}"
    asset="${asset//\{arch2\}/$arch2}"
    asset="${asset//\{arch3\}/$arch3}"
    asset="${asset//\{arch4\}/$arch4}"
    asset="${asset//\{lin\}/$lin}"
    url="https://github.com/$repo/releases/download/${tag}/${asset}"
    file="$TMP/$(basename "$asset")"
    tmpd="$TMP/ghtool"
    rm -rf "$tmpd"; mkdir -p "$tmpd"
    if ! "${CURL[@]}" "$url" -o "$file"; then
      warn "  $bin 下载失败（$url，可加 --install-proxy 走代理）"
      rm -rf "$file" "$tmpd"
      continue
    fi
    # 三种归档形态：.zip / .tar.gz / 单文件 .gz
    local extracted=""
    case "$asset" in
      *.zip)  unzip -oq "$file" -d "$tmpd" 2>/dev/null || true ;;
      *.tar.gz|*.tgz) tar -xzf "$file" -C "$tmpd" 2>/dev/null || true ;;
      *.gz)   gzip -dc "$file" > "$tmpd/$bin" 2>/dev/null || true ;;
    esac
    if [ -f "$tmpd/$bin" ]; then
      extracted="$tmpd/$bin"
    else
      # 归档里可能带一层目录，用 find 兜底
      local found
      found="$(find "$tmpd" -type f -name "$bin" 2>/dev/null | head -1)"
      [ -n "$found" ] && extracted="$found"
    fi
    if [ -n "$extracted" ] && [ -s "$extracted" ]; then
      mv "$extracted" "$GOBIN_DIR/$bin" && chmod +x "$GOBIN_DIR/$bin"
      ok "  $bin $tag → $GOBIN_DIR/$bin"
    else
      warn "  $bin 解压后找不到二进制（资产名可能变了：$url）"
    fi
    rm -rf "$file" "$tmpd"
  done
}

# SecLists：通用字典集(目录/参数/用户名爆破语料)。apt 源里没有稳定包名，
# 只能克隆。它有 2.5G，用 --depth 1 浅克隆。
install_wordlists(){
  local dir="/opt/SecLists"
  if [ -d "$dir" ] && [ -d "$dir/Discovery" ]; then
    ok "  SecLists 已存在 → $dir"
    return 0
  fi
  info "克隆 SecLists 字典集（浅克隆，约 2.5G）..."
  if command -v git >/dev/null 2>&1; then
    if git clone --depth 1 https://github.com/danielmiessler/SecLists.git "$dir" >/dev/null 2>&1; then
      ok "  SecLists → $dir"
    else
      warn "  SecLists 克隆失败（可加 --install-proxy 走代理）"
    fi
  else
    warn "  未装 git，SecLists 跳过"
  fi
}

# uv：内置 MCP「FOFA 资产测绘」用它拉起 stdio server（相对路径 mcp-servers/fofa）。
#
# 关键点（真实踩坑）：pangu 服务是 systemd 用户，PATH 只保证
# /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin；pipx 以 root 安装
# 会把 uv 放到 /root/.local/bin（服务用户既无权穿越 /root 也不在 PATH）——必须
# 把二进制统一落到 /usr/local/bin，否则「预设能加、MCP 起不来」。best-effort：
# 装不上只提示，不影响主服务。
ensure_uv(){
  local cand=""
  for c in /usr/local/bin/uv /usr/bin/uv /root/.local/bin/uv "$HOME/.local/bin/uv"; do
    if [ -x "$c" ]; then cand="$c"; break; fi
  done
  if [ -z "$cand" ]; then
    if command -v pipx >/dev/null 2>&1; then
      pipx install --global uv >/dev/null 2>&1 || pipx install uv >/dev/null 2>&1 || true
    fi
    if [ -z "$cand" ] && command -v python3 >/dev/null 2>&1; then
      python3 -m pip install --break-system-packages -q uv >/dev/null 2>&1 || true
    fi
    for c in /usr/local/bin/uv /root/.local/bin/uv "$HOME/.local/bin/uv"; do
      if [ -x "$c" ]; then cand="$c"; break; fi
    done
  fi
  if [ -n "$cand" ] && [ "$cand" != "/usr/local/bin/uv" ]; then
    cp -f "$cand" /usr/local/bin/uv 2>/dev/null || true
    chmod 755 /usr/local/bin/uv 2>/dev/null || true
  fi
  if [ -x /usr/local/bin/uv ] && /usr/local/bin/uv --version >/dev/null 2>&1; then
    ok "uv 就绪（/usr/local/bin/uv，FOFA MCP 可用）"
  else
    warn "uv 未就绪：FOFA MCP 预设需要它（pip install uv 后复制到 /usr/local/bin）"
  fi
  return 0
}

# pip 工具：wafw00f(WAF 识别)、impacket 的部分脚本。
# 优先 pipx（隔离环境，不污染系统 Python，也不撞 PEP668 externally-managed），
# 没有 pipx 就退回 python3 -m pip --break-system-packages。
install_pip_tools(){
  command -v python3 >/dev/null 2>&1 || { warn "  没有 python3，pip 工具跳过"; return 0; }
  local PIPX=()
  if command -v pipx >/dev/null 2>&1; then
    PIPX=(pipx install)
  else
    PIPX=(python3 -m pip install --break-system-packages)
  fi
  local proxy_args=()
  [ -n "$INSTALL_PROXY" ] && proxy_args=(--proxy "$INSTALL_PROXY")
  # 包名|可执行文件名(空=该包不提供命令行)。已存在就跳过——
  # pipx 对已装的包会报错退出，不该当成"安装失败"。
  local specs="wafw00f|wafw00f
semgrep|semgrep
pwntools|pwntools
uv|uv"
  # netexec 不装：NetExec 2026 起商业化，PyPI 发行文件已撤下（装必失败）；
# 需要时从 GitHub 源码安装（hosttools netexec 条目有说明）。
  local spec pkg bin
  for spec in $specs; do
    pkg="$(printf '%s' "$spec" | cut -d'|' -f1)"
    bin="$(printf '%s' "$spec" | cut -d'|' -f2)"
    if [ -n "$bin" ] && command -v "$bin" >/dev/null 2>&1; then
      ok "  $bin 已存在"
      continue
    fi
    if "${PIPX[@]}" "${proxy_args[@]}" "$pkg" >/dev/null 2>&1; then
      ok "  $pkg ✓"
    else
      warn "  $pkg 安装失败"
    fi
  done
}

# Playwright MCP：browser MCP 用 `npx @playwright/mcp` 启动，需要 node+npx；
# 再预置 chromium 浏览器，否则首次启动还要联网下载。
#
# 浏览器装到 /opt/ms-playwright 而不是 ~/.cache/ms-playwright：
# 服务以 pangu 用户跑，root 家目录里的缓存它看不见。
install_browser_mcp(){
  command -v node >/dev/null 2>&1 || { warn "  没有 node，browser MCP 跳过"; return 0; }
  command -v npm >/dev/null 2>&1 || { warn "  没有 npm，browser MCP 跳过"; return 0; }

  # NPMENV 必须在两个分支外定义：set -u 下未定义变量会直接终止脚本。
  local NPMENV=()
  [ -n "$INSTALL_PROXY" ] && NPMENV=(HTTP_PROXY="$INSTALL_PROXY" HTTPS_PROXY="$INSTALL_PROXY")

  if ! npm ls -g @playwright/mcp >/dev/null 2>&1; then
    info "  安装 Playwright MCP（全局 npm）..."
    if env "${NPMENV[@]}" npm install -g @playwright/mcp@latest >/dev/null 2>&1; then
      ok "  @playwright/mcp ✓"
    else
      warn "  @playwright/mcp 安装失败"
    fi
  fi

  local BROWSER_DIR="/opt/ms-playwright"
  if [ -d "$BROWSER_DIR" ]; then
    ok "  chromium 已预置 → $BROWSER_DIR"
    return 0
  fi
  info "  预置 chromium 浏览器（首次约 150MB）..."
  if env "${NPMENV[@]}" PLAYWRIGHT_BROWSERS_PATH="$BROWSER_DIR" \
       npx --yes playwright@latest install --with-deps chromium >/dev/null 2>&1; then
    ok "  chromium → $BROWSER_DIR"
  else
    warn "  chromium 预置失败（首次启动 MCP 时仍可自动下载）"
  fi
}

# ── 宿主机调优（swap / 内核参数 / 系统日志上限）────
# 放在部署之前：这些是「服务起来时就该是对的」参数；Docker 守护进程的日志上限
# 属于 docker 形态自己的流程（要在 compose up 之前重启守护进程），在 deploy_docker 里做。
tune_host

# ── 执行部署 ─────────────────────────────────────
case "$MODE" in
  binary) deploy_binary ;;
  docker) deploy_docker ;;
  *) die "未知部署形态: $MODE" ;;
esac

if [ "$WITH_TOOLS" = "1" ]; then
  if [ "$IS_MAC" = "1" ]; then
    # macOS：apt/dnf 体系不可用，改用 Homebrew 逐包 best-effort 安装核心件
    # （渗透/GitHub 工具组按需 brew install；装不上的体检页如实显示）。
    if ! command -v brew >/dev/null 2>&1; then
      warn "未检测到 Homebrew，先安装它（可能要求输入密码/回车确认）..."
      /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" \
        || die "Homebrew 安装失败"
      eval "$(/opt/homebrew/bin/brew shellenv 2>/dev/null || true)"
      eval "$(/usr/local/bin/brew shellenv 2>/dev/null || true)"
    fi
    BREW_PREFIX="$(brew --prefix)"
    export PATH="$BREW_PREFIX/bin:$BREW_PREFIX/opt/postgresql@16/bin:$PATH"
    info "Homebrew 安装核心依赖（PostgreSQL/文档渲染/渗透工具组，逐包容错）..."
    for pkg in postgresql@16 poppler imagemagick nmap masscan sqlmap hydra whatweb nikto \
               gobuster ffuf john gitleaks dnsutils; do
      brew list --formula 2>/dev/null | grep -q "^${pkg}@\|^${pkg}$" \
        || brew install "$pkg" >/dev/null 2>&1 \
        || warn "  brew 缺包: $pkg"
    done
    # LibreOffice 是 cask：装完把 soffice 软链进 PATH，报告 docx→PDF 才能找到它。
    if [ ! -x "$BREW_PREFIX/bin/soffice" ] && [ -x "/Applications/LibreOffice.app/Contents/MacOS/soffice" ]; then
      ln -sf "/Applications/LibreOffice.app/Contents/MacOS/soffice" "$BREW_PREFIX/bin/soffice"
      ok "soffice 已软链 → $BREW_PREFIX/bin/soffice"
    fi
    ok "macOS 核心件安装完成（brew install 按需补其余工具）"
  else
    install_tools
  fi
fi

# 代理要落地到运行期设置，宿主环境页的「一键安装」才用同一个代理。
# 放在最后：此时服务已起、schema 已建。
persist_install_proxy

# ── 健康检查 ─────────────────────────────────────
PORT="${LISTEN_ADDR##*:}"
if [ "$MODE" = "docker" ] && [ -f "$INSTALL_DIR/.env" ]; then
  # Docker 形态对外端口可经 .env 的 PANGU_UI_PORT 覆盖（Cloudflare 等 CDN
  # 只转发特定端口，用户会改成 2052 之类）——健康检查必须打映射后的端口。
  _ui_port="$(grep -E '^PANGU_UI_PORT=' "$INSTALL_DIR/.env" | cut -d= -f2)"
  [ -n "$_ui_port" ] && PORT="$_ui_port"
fi
# ── 域名反代（PROXY_MODE=caddy）：服务起来后自动装并配置 ──
if [ -n "$DOMAIN" ] && [ "$PROXY_MODE" = "caddy" ]; then
  install_caddy
fi
# 等待上限：二进制 60 秒；Docker 首次启动要解压 3.66G 镜像、起数据库、跑迁移，
# 慢盘 / 低配机器上 60 秒不够——给 3 分钟，免得服务其实起来了却报「未就绪」。
_tries=30; _wait_desc="60s"
if [ "$MODE" = "docker" ]; then _tries=90; _wait_desc="3 分钟"; fi
for _n in $(seq 1 "$_tries"); do
  # 每轮失败打进度：装机现场「卡在哪一步」必须可见，也方便排障
  # curl 的退出码显式捕获：set -e 下 if 条件本应豁免，但显式 rc 让「为什么退出」
  # 直接可见（曾在此处出现无输出退出的怪象，靠这行定位）
  health_rc=0
  curl -fsS -o /dev/null "http://127.0.0.1:${PORT}/api/health" 2>/dev/null || health_rc=$?
  echo "[*] health check #${_n}: rc=${health_rc}"
  if [ "$health_rc" = "0" ]; then
    # macOS 的 hostname 没有 -I（退出码 1），set -o pipefail 会把它传染整条管道、
    # set -e 当场杀掉脚本——曾让 macOS 安装在 health 200 后静默退出
    HOST_IP="$(hostname -I 2>/dev/null | awk '{print $1}' || true)"
    if [ -n "$DOMAIN" ]; then
      # 域名部署：按反代方案分支打印访问入口与核对项。
      case "$PROXY_MODE" in
        caddy)
          ok "服务已就绪 → https://${DOMAIN}（Caddy 反代已配置：DNS 解析就绪后自动签发 TLS 证书，无需手工操作）"
          info "域名部署核对：①DNS 已把 ${DOMAIN} 解析到本机；②cookie Secure 已按 HTTPS 形态开启（勿用 http://IP:${PORT} 登录）；③公网部署建议先设 PANGU_INIT_TOKEN"
          ;;
        existing)
          ok "服务已就绪 → 域名入口 https://${DOMAIN}（自备反代指向 http://127.0.0.1:${PORT}，TLS 就绪后即为 HTTPS）"
          info "域名部署核对：①DNS 已把 ${DOMAIN} 解析到本机；②cookie Secure 已开启（反代 TLS 就绪前勿用 http://IP:${PORT} 登录）；③PANGU_TRUSTED_PROXIES=$TRUSTED_PROXY（反代不在本机时改成其 IP/网段）；④建议再设 PANGU_HSTS_MAX_AGE=31536000"
          ;;
        *)
          ok "服务已就绪 → http://${HOST_IP:-127.0.0.1}:${PORT}（暂未配反代：cookie Secure 已临时关，配好后把 PANGU_COOKIE_SECURE 改 1 并重启服务，即可用 https://${DOMAIN}）"
          info "域名后续核对：①DNS 把 ${DOMAIN} 解析到本机；②反代指向 http://127.0.0.1:${PORT}；③PANGU_TRUSTED_PROXIES=$TRUSTED_PROXY"
          ;;
      esac
    else
      ok "服务已就绪 → http://${HOST_IP:-127.0.0.1}:${PORT}（首次访问设置管理员密码）"
    fi
    info "下一步：①设置管理员密码 ②「LLM 配置」配通道 ③「系统→MCP」用预设接入 FOFA/Playwright ④建任务可套用内置 Playbook"
    if [ "$MODE" = "binary" ]; then
      if [ "$IS_MAC" = "1" ]; then
        tail -5 "$INSTALL_DIR/pangu.launchd.log" 2>/dev/null || true
        info "服务管理：launchctl kickstart -k gui/$(id -u)/com.pangu 重启 | launchctl bootout gui/$(id -u)/com.pangu 停止 | tail -f $INSTALL_DIR/pangu.launchd.log 看日志"
      else
        systemctl --no-pager status "$SERVICE" | head -5
      fi
    else
      ( cd "$INSTALL_DIR" && docker compose ps )
    fi
    # 服务确认健康之后才装备份定时器、出体检报告：装到一半失败的现场不需要这些。
    install_backup
    if [ "$IS_MAC" != "1" ]; then
      printf '\n── 装后体检 ──\n'
      host_report
    fi
    security_report
    info "随时可重跑 sudo ./install.sh --doctor 复查上面这些项（只读，不改任何东西）"
    exit 0
  fi
  info "等待服务就绪 (${_n}/${_tries})..."
  sleep 2
done
if [ "$MODE" = "binary" ]; then
  if [ "$IS_MAC" = "1" ]; then
    warn "${_wait_desc}内未就绪，查看日志: tail -n 50 $INSTALL_DIR/pangu.launchd.log"
  else
    warn "${_wait_desc}内未就绪，查看日志: journalctl -u $SERVICE -n 50"
  fi
else
  warn "${_wait_desc}内未就绪——自动诊断："
  ( cd "$INSTALL_DIR" && docker compose ps ) || true
  ( cd "$INSTALL_DIR" && docker compose logs --tail 30 ) 2>/dev/null || true
  warn "常见原因：①.env 的 POSTGRES_PASSWORD 与既有 postgres 数据卷不匹配（pangu 日志见 'password authentication failed'）——把初始化数据卷时的旧密码填回 .env 后 docker compose up -d；②镜像拉取不完整——docker compose pull 后重启；③端口被占用——改 .env 的 PANGU_UI_PORT；④内存不足被 OOM 杀——docker inspect pangu 查 OOMKilled，必要时升内存"
fi
exit 1
