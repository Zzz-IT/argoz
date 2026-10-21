# argoz

**Argo（Cloudflare Tunnel）专用的 WebSocket 节点转发器。** 单二进制、纯静态、不依赖 sing-box。

[![CI](https://github.com/Zzz-IT/argoz/actions/workflows/ci.yml/badge.svg)](https://github.com/Zzz-IT/argoz/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Zzz-IT/argoz)](https://github.com/Zzz-IT/argoz/releases/latest)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

```
客户端 ──TLS+WS──► Cloudflare Edge ──tunnel──► cloudflared ──HTTP──► argoz(127.0.0.1:20001) ──► 目标
```

## 一键安装

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/Zzz-IT/argoz/main/install.sh)
```

没有 curl 的机器：

```bash
bash <(wget -qO- https://raw.githubusercontent.com/Zzz-IT/argoz/main/install.sh)
```

装完直接执行 `argoz` 进入面板。安装脚本会：识别发行版与架构 → 补齐 Alpine 证书 → 校验产物是 ELF → 安装二进制 → 建数据目录 → 预装 cloudflared → 装 systemd/OpenRC 服务并设为开机自启。

## 界面

```
╭──────────────────────────────────────────────────────────────────────────────────────────╮
│ argoz v1.0.0              ● running   节点 3 · 运行 2   连接 15   ↑1.0GB ↓8.0GB   up 2h00m │
╰──────────────────────────────────────────────────────────────────────────────────────────╯
╭──────────────────────────────────────────────────────────────────────────────────────────╮
│   #   名称              协议      端口 域名                         状态       连接         │
│     1 Argo-VLESS-1      vless    20001 xxx-yyy.trycloudflare.com   ● up         12         │
│     2 Argo-Trojan-1     trojan   20002 argo.example.com            ● up          3         │
│ ▌   3 Argo-VLESS-2      vless    20003 (未分配)                    ◐ 降级        0         │
│   隧道不存在 (HTTP 1033)  [1h08m 后重试]                                                 │
╰──────────────────────────────────────────────────────────────────────────────────────────╯
 j/k 选择   enter 详情   n 新建   r 重启隧道   d 删除   s 设置   l 日志   u 卸载   q 退出
```

| 键 | 列表页 | 详情页 |
|---|---|---|
| `j/k` `↑/↓` | 移动选择 | 滚动内容 |
| `enter` | 进入详情 | — |
| `n` / `s` / `l` | 新建 / 设置 / 日志 | — |
| `r` / `d` | 重启隧道 / 删除 | 重启隧道 / 删除 |
| `c` `y` `w` | — | 复制链接 / 复制 YAML / 写入文件 |
| `e` / `esc` | — | 编辑 / 返回 |
| `u` | 卸载（需输入 DELETE） | — |
| `q` | 退出 | 返回列表 |

## 特性

- **节点即隧道**：新建只需回答名称/协议/隧道模式，端口、WS 路径、UUID 全自动
- **VLESS / Trojan over WS**：mihomo、xray、v2rayN、sing-box 客户端直接可用
- **临时隧道**（trycloudflare 自动域名，漂移自动回写）与**固定隧道**（Token + 自有域名）
- **链接与 Clash 同源生成**，OSC 52 直达剪贴板，也可导出 `nodes.txt`
- **热生效**：增删改节点不重启进程；端口被占用自动顺延
- **5 级保活 + 分层重试**：2s→4s→8s→3min×20→60min，±20% 抖动，稳定 10 分钟清零
- **端到端探针**：有流量时 60s、空闲 3min 探测域名，连续失败重启隧道
- **日志**：4 级过滤、5MB 轮转、TUI 静音、面板内可查
- **卸载**：面板 `u` / 菜单 `9` / `install.sh --uninstall`

## 使用

```bash
argoz              # 全屏 TUI 面板
argoz --classic    # 经典行式菜单（老终端 / 偏好纯文本）
argoz -d           # 无菜单守护（systemd / OpenRC 用）
argoz --version
argoz -dir /path   # 指定数据目录
```

无 TTY（管道、脚本、CI）时自动降级为行式菜单，不会卡死。

## 保活与自愈（5 级）

```
L0 连接级    30s WS ping + 5m 空闲超时         → 断单条会话
L1 监听级    15s 本地探针，连续 3 次失败         → 重建 listener（端口可漂移）
L2 进程级    cloudflared 退出                   → 2s/4s/8s → 3min×20 → 60min 分层重试
L3 端到端    60s(有流量) / 3min(空闲) HTTPS 探针 → 连续 2 次失败重启隧道
L4 进程自愈  5min 采样 goroutine/fd             → 泄漏则退出交 systemd/OpenRC 拉起
L5 外部守护  systemd Restart=always / OpenRC supervise-daemon
```

- **永久性错误**（1033 / tunnel not found / Token 无效）第 2 次起直接转 60 分钟重试
- **每小时全量巡检**：所有节点重跑 L1 + L3，纠正状态机漂移

## 日志

| 项 | 说明 |
|---|---|
| 文件 | `<数据目录>/argoz.log`（0600） |
| 轮转 | 超过 5MB 切为 `argoz.log.1` |
| 等级 | `error` / `warn` / `info` / `debug`，设置页实时生效 |
| 终端 | 守护模式全量；TUI 模式静音（只写文件）；`--classic` 仅 warn/error |

## cloudflared 安装时机

1. **安装脚本**：检测到缺失即预装（`--no-cloudflared` 可跳过）
2. **进程启动预热**：后台异步下载，不阻塞节点启动
3. **节点启动兜底**：前两步失败时再试；失败结果缓存 30 秒，避免多节点重复拉取

## 卸载

```bash
bash install.sh --uninstall          # 保留配置与日志
bash install.sh --uninstall --purge  # 连配置、日志、cloudflared 一起删
```

面板按 `u` 或菜单选 `9`，同样需要输入 `DELETE` 确认。

## 目录

| 路径 | 说明 |
|---|---|
| `/usr/local/bin/argoz` | 二进制 |
| `/usr/local/bin/cloudflared` | 见上方安装时机 |
| `/usr/local/etc/argoz/config.json` | 配置（0600） |
| `/usr/local/etc/argoz/argoz.log` | 运行日志 |
| `/usr/local/etc/argoz/nodes.txt` | 导出的链接与 YAML |

## 设置项

| 项 | 默认 | 说明 |
|---|---|---|
| 并发上限 | 128 | 每节点活跃 WS 会话数，超出 503（每会话约 41KB） |
| 保活间隔 | 30s | WS ping 周期 |
| 空闲超时 | 5m | 双向无流量强制下线 |
| 监听与端口段 | 127.0.0.1 / 20000-20100 | 端口仅本地，可安全漂移 |
| ECH 注入 | 开 | 链接/YAML 的 SNI 写成 cloudflare-ech.com |
| 日志级别 | info | error / warn / info / debug |

## 兼容性

| 系统 | 服务托管 |
|---|---|
| Alpine 3.x（musl） | OpenRC `supervise-daemon` |
| Debian 10+ / Ubuntu 18.04+ | systemd |
| CentOS / RHEL / Rocky 8+ | systemd |
| 无 systemd / OpenRC | 手动 nohup 或 crontab @reboot |

二进制以 `CGO_ENABLED=0` 静态编译；构建需 **Go 1.24+**。

## 从源码构建

```bash
git clone https://github.com/Zzz-IT/argoz.git
cd argoz
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o argoz .
```

## 开发

```bash
go test ./... -race     # 35 个用例：协议端到端、退避时序、探针、日志、TUI、卸载
go vet ./...
```

CI 见 [.github/workflows/ci.yml](.github/workflows/ci.yml)；打 `v*` 标签自动构建并发布 Release（linux/amd64、linux/arm64 + 校验和）。

## 说明

- Argo 隧道**只转发 TCP**，不支持 UDP（Hysteria2 / TUIC 不在范围内）
- 本地监听是纯 HTTP，TLS 由 Cloudflare 边缘终结，**不需要任何证书**
- 固定隧道需在 Cloudflare 面板把路由指向 `http://localhost:<节点端口>`

## License

[MIT](LICENSE) · 变更记录见 [CHANGELOG.md](CHANGELOG.md)
