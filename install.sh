#!/usr/bin/env bash
# argoz 安装脚本（幂等）
#   bash install.sh                  # 装 argoz；cloudflared 缺失/损坏时自动安装
#   bash install.sh --force          # 强制重新下载/修复 argoz 与 cloudflared
#   bash install.sh --no-cloudflared # 跳过 cloudflared 预装
#   bash install.sh --uninstall [--purge]
set -euo pipefail

RED='\033[1;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; CYAN='\033[0;36m'; WHITE='\033[1;37m'; NC='\033[0m'
info() { echo -e "${CYAN}[信息]${NC} $*"; }
ok()   { echo -e "${GREEN}[成功]${NC} $*"; }
warn() { echo -e "${YELLOW}[注意]${NC} $*"; }
err()  { echo -e "${RED}[错误]${NC} $*" >&2; }

WITH_CF=1
UNINSTALL=0
PURGE=0
FORCE=0
for arg in "$@"; do
  case "$arg" in
    --no-cloudflared) WITH_CF=0 ;;
    --uninstall)      UNINSTALL=1 ;;
    --purge)          PURGE=1 ;;
    --force|-f)       FORCE=1 ;;
    -h|--help)
      echo "用法: bash install.sh [--force] [--no-cloudflared] [--uninstall [--purge]]"
      exit 0
      ;;
    *) err "未知参数: $arg"; exit 1 ;;
  esac
done

# ---------- 0. 权限 ----------
if [[ ${EUID:-$(id -u)} -ne 0 ]]; then
  err "请使用 root 运行（或 sudo bash install.sh）"; exit 1
fi

# ---------- 卸载 ----------
if [[ "$UNINSTALL" -eq 1 ]]; then
  info "开始卸载 argoz ..."
  if command -v systemctl >/dev/null 2>&1 && [[ -f /etc/systemd/system/argoz.service ]]; then
    systemctl disable --now argoz >/dev/null 2>&1 || true
    rm -f /etc/systemd/system/argoz.service
    systemctl daemon-reload
    ok "已移除 systemd 服务"
  fi
  if command -v rc-update >/dev/null 2>&1 && [[ -f /etc/init.d/argoz ]]; then
    rc-service argoz stop >/dev/null 2>&1 || true
    rc-update del argoz default >/dev/null 2>&1 || true
    rm -f /etc/init.d/argoz
    ok "已移除 OpenRC 服务"
  fi
  rm -f /usr/local/bin/argoz && ok "已删除 /usr/local/bin/argoz"
  rm -f /usr/local/bin/az && ok "已删除快捷命令 az"

  if [[ "$PURGE" -eq 1 ]]; then
    rm -rf "${ARGOZ_DIR:-/usr/local/etc/argoz}" && ok "已删除配置、日志、缓存与备份目录"
    rm -f /usr/local/bin/cloudflared && ok "已删除 cloudflared"
  else
    warn "配置、日志与验证缓存保留在 ${ARGOZ_DIR:-/usr/local/etc/argoz}（加 --purge 可一并删除）"
  fi
  ok "卸载完成"
  exit 0
fi

# ---------- 1. 发行版与架构 ----------
DISTRO="unknown"; DISTRO_VER=""
if [[ -r /etc/os-release ]]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  DISTRO="${ID:-unknown}"; DISTRO_VER="${VERSION_ID:-}"
fi
info "发行版: ${DISTRO} ${DISTRO_VER}  内核: $(uname -r)"

case "$(uname -m)" in
  x86_64|amd64)  GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) err "不支持的架构: $(uname -m)（支持 x86_64 / aarch64）"; exit 1 ;;
esac
info "架构: ${GOARCH}"

REPO="${ARGOZ_REPO:-Zzz-IT/argoz}"
DATA_DIR="${ARGOZ_DIR:-/usr/local/etc/argoz}"
BIN_PATH="/usr/local/bin/argoz"
CF_PATH="/usr/local/bin/cloudflared"
DOWNLOAD_TIMEOUT="${ARGOZ_DOWNLOAD_TIMEOUT:-300}"

TMP_DIR="$(mktemp -d)"
cleanup() { rm -rf "$TMP_DIR"; }
trap cleanup EXIT

# ---------- 2. 基础工具（只在真正需要下载时使用）----------
ensure_downloader() {
  if command -v curl >/dev/null 2>&1; then
    DOWNLOADER="curl"
  elif command -v wget >/dev/null 2>&1; then
    DOWNLOADER="wget"
  else
    err "需要 curl 或 wget 之一才能下载 argoz"; return 1
  fi
}

run_timeout() {
  local seconds="$1"; shift
  if command -v timeout >/dev/null 2>&1; then
    timeout "$seconds" "$@"
    return $?
  fi
  # 极简 BusyBox 环境没有 timeout 时的纯 Bash 看门狗。
  "$@" &
  local cmd_pid=$!
  (
    sleep "$seconds"
    if kill -0 "$cmd_pid" 2>/dev/null; then
      kill -TERM "$cmd_pid" 2>/dev/null || true
      sleep 2
      kill -KILL "$cmd_pid" 2>/dev/null || true
    fi
  ) &
  local watchdog_pid=$!
  local rc=0
  wait "$cmd_pid" || rc=$?
  kill "$watchdog_pid" 2>/dev/null || true
  wait "$watchdog_pid" 2>/dev/null || true
  return "$rc"
}

fetch() { # fetch <url> <dest>
  ensure_downloader || return 1
  if [[ "$DOWNLOADER" == "curl" ]]; then
    run_timeout "$DOWNLOAD_TIMEOUT" curl -fL --retry 2 --connect-timeout 15 --max-time "$DOWNLOAD_TIMEOUT" -o "$2" "$1"
  else
    run_timeout "$DOWNLOAD_TIMEOUT" wget -q --timeout=60 --tries=2 -O "$2" "$1"
  fi
}

sha256_of() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$file" | awk '{print $NF}'
  else
    return 1
  fi
}

checksum_for() { # checksum_for <asset> <checksums-file>
  awk -v want="$1" '{
    name=$2; sub(/^\*/, "", name);
    if (name == want) { print tolower($1); exit }
  }' "$2"
}

is_elf() {
  [[ -f "$1" ]] && head -c 4 "$1" | od -An -tx1 | grep -qi "7f 45 4c 46"
}

binary_ok() { # binary_ok <path> [label]
  local path="$1"
  [[ -f "$path" ]] || return 1
  if [[ ! -x "$path" ]]; then
    chmod 0755 "$path" 2>/dev/null || return 1
  fi
  [[ $(wc -c <"$path") -ge 1048576 ]] || return 1
  is_elf "$path" || return 1
  local out
  out="$(run_timeout 12 "$path" --version 2>&1)" || return 1
  [[ -n "${out//[[:space:]]/}" ]]
}

# Alpine 极简系统往往缺根证书，Go 的 HTTPS 下载会失败。
if command -v apk >/dev/null 2>&1; then
  if [[ ! -f /etc/ssl/certs/ca-certificates.crt || ! -d /usr/share/zoneinfo ]]; then
    info "Alpine 缺少证书/时区数据，尝试补齐"
    apk add --no-cache ca-certificates tzdata >/dev/null 2>&1 || warn "apk 安装证书/时区失败，HTTPS 可能不可用"
    update-ca-certificates >/dev/null 2>&1 || true
  fi
fi

# ---------- 3. 安装 argoz（健康则完全不下载）----------
if [[ "$FORCE" -eq 0 ]] && binary_ok "$BIN_PATH"; then
  ok "argoz 已安装且可执行，跳过下载: $("$BIN_PATH" --version 2>/dev/null | head -n1)"
else
  info "下载 argoz-${GOARCH} ..."
  TMP_BIN="$TMP_DIR/argoz-${GOARCH}"
  if ! fetch "https://github.com/${REPO}/releases/latest/download/argoz-${GOARCH}" "$TMP_BIN"; then
    err "下载 argoz 失败（已超时或网络错误）；旧版本保持不变"
    exit 1
  fi

  SIZE=$(wc -c <"$TMP_BIN")
  if [[ "$SIZE" -lt 1048576 ]]; then
    err "下载文件只有 ${SIZE} 字节，疑似失败页；旧版本保持不变"; exit 1
  fi
  if ! is_elf "$TMP_BIN"; then
    err "下载内容不是 ELF 可执行文件（可能是 404 页）；旧版本保持不变"; exit 1
  fi

  # 校验和可用时强制核对；不可用时仍会执行 ELF 与 --version 双重验证。
  CHECKSUMS="$TMP_DIR/checksums.txt"
  if fetch "https://github.com/${REPO}/releases/latest/download/checksums.txt" "$CHECKSUMS" >/dev/null 2>&1; then
    WANT_SUM="$(checksum_for "argoz-${GOARCH}" "$CHECKSUMS" || true)"
    if [[ -n "$WANT_SUM" ]]; then
      GOT_SUM="$(sha256_of "$TMP_BIN" || true)"
      if [[ -z "$GOT_SUM" || "${GOT_SUM,,}" != "${WANT_SUM,,}" ]]; then
        err "argoz SHA256 校验失败；旧版本保持不变"; exit 1
      fi
      ok "SHA256 校验通过"
    else
      warn "checksums.txt 中没有 argoz-${GOARCH}，继续使用 ELF/--version 校验"
    fi
  else
    warn "未获取到 checksums.txt，继续使用 ELF/--version 校验"
  fi

  chmod 0755 "$TMP_BIN"
  NEW_VER="$(run_timeout 15 "$TMP_BIN" --version 2>&1)" || {
    err "新下载的 argoz 无法执行 --version；旧版本保持不变"; exit 1
  }
  if [[ -z "${NEW_VER//[[:space:]]/}" ]]; then
    err "新下载的 argoz --version 输出为空；旧版本保持不变"; exit 1
  fi

  # 先在目标目录暂存并校验，最后一次 rename 覆盖；任何失败都不会破坏旧文件。
  STAGE="${BIN_PATH}.new.$$"
  if ! install -m 0755 "$TMP_BIN" "$STAGE"; then
    rm -f "$STAGE"; err "暂存新版本失败；旧版本保持不变"; exit 1
  fi
  if ! mv -f "$STAGE" "$BIN_PATH"; then
    rm -f "$STAGE"; err "替换 argoz 失败；旧版本保持不变"; exit 1
  fi
  ok "argoz 已安装: $BIN_PATH (${NEW_VER})"
fi

mkdir -p /usr/local/bin
ln -sf "$BIN_PATH" /usr/local/bin/az 2>/dev/null && ok "快捷命令已创建: az（直接输入 az 唤出面板）"

# ---------- 4. 数据目录 ----------
mkdir -p "$DATA_DIR"
chmod 750 "$DATA_DIR"
ok "数据目录: $DATA_DIR"

# ---------- 5. cloudflared（统一调用 argoz artifact CLI）----------
if [[ "$WITH_CF" -eq 1 ]]; then
  EXISTING_CF=""
  if command -v cloudflared >/dev/null 2>&1; then
    EXISTING_CF="$(command -v cloudflared)"
  elif [[ -x "$CF_PATH" ]]; then
    EXISTING_CF="$CF_PATH"
  fi

  if [[ "$FORCE" -eq 0 ]] && [[ -n "$EXISTING_CF" ]] && binary_ok "$EXISTING_CF"; then
    ok "cloudflared 已可用，跳过下载: $("$EXISTING_CF" --version 2>/dev/null | head -n1)"
  else
    info "通过 argoz 统一安装/修复 cloudflared (linux-${GOARCH}) ..."
    # 仅在本机健康检查失败或用户 --force 时走到这里；强制重装以修复损坏文件。
    CF_ARGS=(update --component cloudflared --force)
    if run_timeout 360 "$BIN_PATH" "${CF_ARGS[@]}"; then
      CF_NEW="$(command -v cloudflared 2>/dev/null || true)"
      [[ -n "$CF_NEW" ]] || CF_NEW="$CF_PATH"
      if binary_ok "$CF_NEW"; then
        ok "cloudflared 已安装: $("$CF_NEW" --version 2>/dev/null | head -n1)"
      else
        warn "cloudflared 命令返回成功，但本地复验未通过；可稍后执行 argoz update --component cloudflared"
      fi
    else
      warn "cloudflared 安装失败；旧文件未被覆盖，运行时仍可重试或执行 argoz update --component cloudflared"
    fi
  fi
else
  warn "已跳过 cloudflared 预装（运行时首次启动节点时会自动下载）"
fi

# ---------- 6. 服务托管 ----------
info "安装 systemd / OpenRC 服务 ..."
if "$BIN_PATH" service install -bin "$BIN_PATH" -dir "$DATA_DIR"; then
  ok "服务已安装并启动"
  echo -e "     ${WHITE}argoz service status${NC} 查看状态   ${WHITE}argoz service uninstall${NC} 卸载服务"
else
  warn "服务安装失败，可稍后手动执行："
  echo -e "     ${WHITE}$BIN_PATH service install -bin $BIN_PATH -dir $DATA_DIR${NC}"
  echo -e "     无 init 系统可手动运行: ${WHITE}nohup argoz --supervise >/var/log/argoz.log 2>&1 &${NC}"
fi

# ---------- 7. 完成 ----------
echo
ok "安装完成。执行 ${WHITE}argoz${NC} 进入面板。"
echo -e "     配置: ${DATA_DIR}/config.json   日志: ${DATA_DIR}/argoz.log   缓存: ${DATA_DIR}/cache"
