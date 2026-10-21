#!/usr/bin/env bash
# argoz 安装脚本
#   bash install.sh                  # 装 argoz + cloudflared + 服务托管
#   bash install.sh --no-cloudflared # 跳过 cloudflared 预装（运行时再自动下载）
set -euo pipefail

RED='\033[1;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; CYAN='\033[0;36m'; WHITE='\033[1;37m'; NC='\033[0m'
info() { echo -e "${CYAN}[信息]${NC} $*"; }
ok()   { echo -e "${GREEN}[成功]${NC} $*"; }
warn() { echo -e "${YELLOW}[注意]${NC} $*"; }
err()  { echo -e "${RED}[错误]${NC} $*" >&2; }

WITH_CF=1
UNINSTALL=0
PURGE=0
for arg in "$@"; do
  case "$arg" in
    --no-cloudflared) WITH_CF=0 ;;
    --uninstall)      UNINSTALL=1 ;;
    --purge)          PURGE=1 ;;
    -h|--help) echo "用法: bash install.sh [--no-cloudflared] [--uninstall [--purge]]"; exit 0 ;;
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
    rm -rf "${ARGOZ_DIR:-/usr/local/etc/argoz}" && ok "已删除配置与日志目录"
    rm -f /usr/local/bin/cloudflared && ok "已删除 cloudflared"
  else
    warn "配置与日志保留在 ${ARGOZ_DIR:-/usr/local/etc/argoz}（加 --purge 可一并删除）"
  fi
  ok "卸载完成"
  exit 0
fi

# ---------- 1. 发行版识别 ----------
DISTRO="unknown"; DISTRO_VER=""
if [[ -r /etc/os-release ]]; then
  # shellcheck disable=SC1091
  . /etc/os-release
  DISTRO="${ID:-unknown}"; DISTRO_VER="${VERSION_ID:-}"
fi
info "发行版: ${DISTRO} ${DISTRO_VER}  内核: $(uname -r)"

# ---------- 2. 架构识别 ----------
case "$(uname -m)" in
  x86_64|amd64)  GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) err "不支持的架构: $(uname -m)（支持 x86_64 / aarch64）"; exit 1 ;;
esac
info "架构: ${GOARCH}"

# ---------- 3. 依赖与证书 ----------
DOWNLOADER=""
if command -v curl >/dev/null 2>&1; then DOWNLOADER="curl"
elif command -v wget >/dev/null 2>&1; then DOWNLOADER="wget"
else err "需要 curl 或 wget 之一"; exit 1; fi

fetch() { # fetch <url> <dest>
  if [[ "$DOWNLOADER" == "curl" ]]; then
    curl -fL --retry 3 --connect-timeout 15 -o "$2" "$1"
  else
    wget -q --timeout=60 -O "$2" "$1"
  fi
}

# Alpine 极简系统往往缺根证书，Go 的 HTTPS 探针与下载都会失败
if command -v apk >/dev/null 2>&1; then
  info "检测到 Alpine (apk)，补齐 ca-certificates / tzdata"
  apk add --no-cache ca-certificates tzdata >/dev/null 2>&1 || warn "apk 安装证书失败，HTTPS 可能不可用"
  update-ca-certificates >/dev/null 2>&1 || true
fi

REPO="${ARGOZ_REPO:-Zzz-IT/argoz}"
DATA_DIR="${ARGOZ_DIR:-/usr/local/etc/argoz}"
BIN_PATH="/usr/local/bin/argoz"
CF_PATH="/usr/local/bin/cloudflared"

# ---------- 4. 安装 argoz ----------
info "下载 argoz-${GOARCH} ..."
TMP="$(mktemp)"
fetch "https://github.com/${REPO}/releases/latest/download/argoz-${GOARCH}" "$TMP"

SIZE=$(wc -c <"$TMP")
if [[ "$SIZE" -lt 1048576 ]]; then
  err "下载文件只有 ${SIZE} 字节，疑似失败页"; rm -f "$TMP"; exit 1
fi
if ! head -c 4 "$TMP" | od -An -tx1 | grep -qi "7f 45 4c 46"; then
  err "下载内容不是 ELF 可执行文件（可能是 404 页）"; rm -f "$TMP"; exit 1
fi
install -m 0755 "$TMP" "$BIN_PATH"
rm -f "$TMP"
ok "argoz 已安装: $BIN_PATH ($("$BIN_PATH" --version))"
ln -sf "$BIN_PATH" "$(dirname "$BIN_PATH")/az" 2>/dev/null && ok "快捷命令已创建: az（直接输入 az 唤出面板）"

# ---------- 5. 数据目录 ----------
mkdir -p "$DATA_DIR"
chmod 750 "$DATA_DIR"
ok "数据目录: $DATA_DIR"

# ---------- 6. cloudflared（可选预装）----------
if [[ "$WITH_CF" -eq 1 ]]; then
  if [[ -x "$CF_PATH" ]]; then
    ok "cloudflared 已存在: $("$CF_PATH" --version 2>/dev/null | head -n1)"
  else
    info "预装 cloudflared (linux-${GOARCH}) ..."
    TMP2="$(mktemp)"
    if fetch "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-${GOARCH}" "$TMP2" \
       && [[ $(wc -c <"$TMP2") -gt 1048576 ]]; then
      install -m 0755 "$TMP2" "$CF_PATH"
      ok "cloudflared 已安装: $("$CF_PATH" --version 2>/dev/null | head -n1)"
    else
      warn "cloudflared 预装失败，运行时将由 argoz 自动重试下载"
    fi
    rm -f "$TMP2"
  fi
else
  warn "已跳过 cloudflared 预装（运行时首次启动节点时会自动下载）"
fi

# ---------- 7. 服务托管 ----------
info "安装 systemd / OpenRC 服务 ..."
if "$BIN_PATH" service install -bin "$BIN_PATH" -dir "$DATA_DIR"; then
  ok "服务已安装并启动"
  echo -e "     ${WHITE}argoz service status${NC} 查看状态   ${WHITE}argoz service uninstall${NC} 卸载服务"
else
  warn "服务安装失败，可稍后手动执行："
  echo -e "     ${WHITE}$BIN_PATH service install -bin $BIN_PATH -dir $DATA_DIR${NC}"
  echo -e "     无 init 系统可手动运行: ${WHITE}nohup argoz --supervise >/var/log/argoz.log 2>&1 &${NC}"
fi

# ---------- 8. 完成 ----------
echo
ok "安装完成。执行 ${WHITE}argoz${NC} 进入面板。"
echo -e "     配置: ${DATA_DIR}/config.json   日志: ${DATA_DIR}/argoz.log"
