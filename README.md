# argoz

**Argo（Cloudflare Tunnel）专用的 WebSocket 节点转发器。** 单二进制、纯静态、纯标准库，不依赖 sing-box。

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

装完执行 `argoz` 进面板。脚本会：识别发行版与架构 → 补齐 Alpine 证书 → 校验产物是 ELF → 安装二进制 → 建数据目录 → 预装 cloudflared → 调用 `argoz service install` 装 systemd/OpenRC 服务并启动。

## 界面

```
  argoz v1.1.0    ● running   节点 3 · 运行 2   连接 15   ↑1.2GB ↓8.7GB

  1 节点管理      2 新建节点      3 设置      4 日志      9 卸载      0 退出

  >
```

节点列表：

```
  节点列表
  ───────────────────────────────────────────────────────────────
  #   名称              协议     端口    域名                        状态    连接
  1   Argo-VLESS-1      vless    20001   xxx-yyy.trycloudflare.com   ● up     12
  2   Argo-Trojan-1     trojan   20002   argo.example.com            ● up      3
  3   Argo-VLESS-2      vless    20003   (未分配)                     ◐ 降级    0
  ───────────────────────────────────────────────────────────────
  节点 #3：隧道不存在 (HTTP 1033)   [1h08m 后重试]   [已重启 3 次]

  输入编号进入详情   r 刷新   0 返回
```

## 新建节点

六个问题，全部可以直接回车走自动：

```
  名称 (默认 Argo-VLESS-4):
  协议 [1]vless  [2]trojan          (默认 1):
  端口 (回车自动分配):
  WS 路径 (回车随机生成):
  UUID (回车自动生成):
  Argo 隧道 [1]临时  [2]固定         (默认 1):
```

| 字段 | 手填规则 | 留空 |
|---|---|---|
| 端口 | 1-65535，未被其他节点占用且能绑定 | 从端口段内自动分配 |
| WS 路径 | 以 `/` 开头、不含空白 | 随机 12 位十六进制 |
| UUID | 36 位或 32 位十六进制（自动补横线） | 自动生成 v4 |
| 密码（trojan） | 8-64 位、不含空白 | 16 字节随机 |

填完显示确认页，回车即创建；创建后自动等待域名并进入详情。

### 节点自检

详情页按 `7`，逐层打印：

```
  ✓ 本地监听  127.0.0.1:20002     可连接
  ✓ DNS       laxgo.zhzz.us.ci   → 104.21.x.x, 172.67.x.x
  ✓ 端到端    HTTP 404  38ms  server=cloudflare
      CF-Ray: 8c1f2a3b4d5e6f70
      提示：1033/530 多为 CF 面板路由未指向 http://localhost:20002
```

## 单实例保护

启动时在数据目录写 `argoz.lock`（内含 pid）。已有实例在跑时，第二个进程会直接退出：

```
已有 argoz 实例在运行（pid 163），请先停止它：kill 163
```

进程被 kill -9 留下的陈旧锁，会在下次启动时按 pid 存活状态自动接管，不会卡住。

## 特性

- **纯文本菜单**：零第三方依赖，只有标准库
- **节点即隧道**：新建/编辑一条命令搞定，端口、路径、UUID 都可手填或自动
- **VLESS / Trojan over WS**：mihomo、xray、v2rayN、sing-box 客户端直接可用
- **临时隧道**（trycloudflare 自动域名，漂移自动回写）与**固定隧道**（Token + 自有域名）
- **链接与 Clash 同源生成**，OSC 52 直达剪贴板，也可导出 `nodes.txt`
- **热生效**：增删改节点不重启进程；端口被占用自动顺延
- **5 级保活 + 分层重试**：2s→4s→8s→3min×20→60min，±20% 抖动，稳定 10 分钟清零
- **端到端探针**：有流量时 60s、空闲 3min 探测域名，连续失败重启隧道
- **日志**：4 级过滤、5MB 轮转、面板内可查
- **UDP（仅 mihomo VLESS）**：设置页可选开启，按 mihomo 规则选择性转发；默认关闭
- **统一资源管理**：argoz / cloudflared 共用缓存、锁、SHA256/ELF 校验、原子替换与失败冷却，安装脚本重复执行不重复下载
- **事务化升级**：下载超时、断点、备份、替换后验证、失败回滚；本体与 cloudflared 分开升级/重装
- **自检与修复**：只读诊断 daemon/RPC/二进制/权限/依赖/内存/磁盘/端口，用户确认后执行有限安全修复
- **低内存模式**：总内存 ≤96MB 自动采用较低并发、UDP 配额和 GC 限制，并做可用内存准入
- **内置服务托管**：`argoz service install/status/uninstall` 自动适配 systemd / OpenRC
- **自更新**：`argoz update` 下载、SHA256 校验、原子替换并重启服务；`--check` 只查版本
- **Alpine 自愈**：daemon 启动时检查并尝试补齐 `ca-certificates` / `tzdata` / `gcompat`
- **孤儿清理**：cloudflared 设置 `Pdeathsig`，daemon 启动时按节点端口/Token 精确清理遗留进程
- **卸载**：菜单 `9` / `install.sh --uninstall`

## 快捷命令

安装后会自动创建符号链接 `az`（与 `argoz` 同目录）：

```bash
az          # 唤出面板，等同于 argoz
az --version
```

## 代码结构

\`\`\`
argoz/
├── main.go                入口：flag 分发、daemon / panel / supervisor
├── supervise.go           自守护父进程
├── internal/
│   ├── artifact/          二进制缓存、断点下载、校验、原子替换、备份/回滚
│   ├── config/            Node / Settings / Config、URI 与 YAML 生成
│   ├── core/              engine、relay(WS+HTTP)、tunnel、health、lock、update、uninstall、doctor 接口
│   ├── doctor/            只读自检（daemon/RPC/二进制/依赖/内存/磁盘/端口）
│   ├── rpc/               本地控制通道（服务端 + 客户端）
│   ├── panel/             全部终端页面（列表/详情/设置/日志/状态/更新/自检）
│   ├── protocol/          VLESS / Trojan 握手
│   ├── sys/               systemd/OpenRC 模板与安装、环境自检、az 快捷命令
│   ├── logx/              分级日志 + 轮转
│   └── xutil/             随机/UUID/原子写/格式化/终端宽度
\`\`\`

依赖方向单向：`panel → rpc → core → {config, tunnel, relay, protocol, sys, logx, xutil}`，没有循环。

## 进程模型

\`\`\`
argoz -d            daemon：跑节点 + 5 级保活 + 本地控制通道
argoz               panel ：只做客户端，连 daemon 读写配置，退出不影响服务
argoz --supervise   supervisor：拉起并看护 daemon，容器没有 init 时用
\`\`\`

- 面板启动时若没有 daemon，会自动后台拉起一个
- 有 systemd / OpenRC 时由系统托管 \`argoz -d\`，不需要 supervisor
- 同一数据目录只允许一个 daemon（\`argoz.lock\`），重复启动会被拒绝

## 面板

主菜单：

\`\`\`
  argoz v1.2.0    ● 运行中   节点 3/3   连接 15   ↑1.2GB ↓8.7GB
  守护进程   进程号 563 · 已运行 3 小时 12 分 · 内存 12.4MB

  1 节点管理    2 新建节点    3 设置    4 日志    5 检查更新    6 运行状态    9 卸载    0 退出
\`\`\`

- **节点列表**：宽度自适应、分页（15 行/页）、\`/\` 搜索、\`r\` 刷新
- **节点详情**：链接自动折行、\`n/p\` 翻屏，含复制链接 / 复制 YAML / 写入文件 / 编辑 / 重启 / 删除 / 自检
- **日志**：\`n/p\` 翻页，\`e/w/i/d/a\` 级别过滤，\`g\` 跳到最新
- **检查更新**：查询 GitHub Releases 并一键升级（下载 → SHA256 校验 → ELF 校验 → 原子替换 → 重启服务）
- **运行状态**：采样 1 秒计算每节点实时上下行速率
- 终端宽度可用环境变量覆盖：\`ARGOZ_COLS=70 ARGOZ_LINES=30 argoz\`

## 使用

```bash
argoz              # 面板（自动连接/拉起后台 daemon）
argoz -d           # daemon（systemd / OpenRC 托管）
argoz --supervise  # 自守护（容器无 init）
argoz service install    # 安装并启动 systemd/OpenRC 服务
argoz service status     # 查看服务状态
argoz service uninstall  # 只移除服务，保留二进制与数据
argoz update --check   # 检查新版本
argoz update           # 下载、校验、原子替换并重启服务
argoz --version
argoz -dir /path       # 指定数据目录
```

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
| 终端 | 守护模式输出全部；菜单模式只输出 warn/error |

## cloudflared 安装时机

1. **安装脚本**：检测到缺失即预装（`--no-cloudflared` 可跳过）
2. **进程启动预热**：后台异步下载，不阻塞节点启动
3. **节点启动兜底**：前两步失败时再试；失败结果缓存 30 秒

## 卸载

```bash
bash install.sh --uninstall          # 保留配置与日志
bash install.sh --uninstall --purge  # 连配置、日志、cloudflared 一起删
```

面板菜单选 `9`，需输入 `DELETE` 确认。

## 目录

| 路径 | 说明 |
|---|---|
| `/usr/local/bin/argoz` | 二进制 |
| `/usr/local/bin/cloudflared` | 见上方安装时机 |
| `/usr/local/etc/argoz/config.json` | 配置（0600） |
| `/usr/local/etc/argoz/argoz.log` | 运行日志 |
| `/usr/local/etc/argoz/nodes.txt` | 导出的链接与 YAML |
| `/usr/local/etc/argoz/run/<nodeID>.pid` | cloudflared 进程 PID（用于孤儿清理） |

## 设置项

| 项 | 默认 | 说明 |
|---|---|---|
| 并发上限 | 128 | 每节点活跃 WS 会话数，超出 503（每会话约 41KB） |
| 保活间隔 | 30s | WS ping 周期 |
| 空闲超时 | 5m | 双向无流量强制下线 |
| 监听与端口段 | 127.0.0.1 / 1-65535 | 自动分配范围（从 1024 起）；可写单端口如 `8080-8080` |
| ECH 注入 | 开 | 链接/YAML 的 SNI 写成 cloudflare-ech.com |
| UDP 转发 | 关 | 仅 mihomo VLESS 标准 UDP；关闭时 UDP 直接拒绝 |
| UDP 空闲 | 60s | UDP 会话空闲回收时间，建议 < Cloudflare 100s |
| UDP 会话上限 | 32 | 每节点 UDP 会话数，最大 128；低内存模式运行期上限 8 |
| 内存模式 | auto | auto / low / normal；总内存 ≤96MB 自动按低内存基线运行 |
| 日志级别 | info | error / warn / info / debug |

## UDP（仅 mihomo VLESS）

默认关闭。设置页打开 `7 UDP 转发` 后，argoz 只接受 mihomo 的标准 VLESS UDP（`cmd=0x02`），不支持 XUDP/mux；请勿在 mihomo 中开启 XUDP，若版本支持 `packet-encoding` 请使用 `packet`。

每条 UDP 流使用独立的 VLESS/WS 连接，仍走同一条 Argo 隧道；这样某条流丢包不会阻塞其他流。

mihomo 里建议把 UDP 单独放一个 outbound，只给需要的 UDP 规则用：

```yaml
proxies:
  - name: argo-udp
    type: vless
    server: xxx.trycloudflare.com
    port: 443
    uuid: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
    network: ws
    tls: true
    udp: true
    # packet-encoding: packet  # 如果你的 mihomo 版本支持，显式使用标准 packet
    ws-opts:
      path: /你的路径
      headers:
        Host: xxx.trycloudflare.com
```

规则建议：DNS 用 DoH/DoT，`UDP 443` 直连或拒绝让浏览器回退 TCP，只有确实需要代理的 UDP 应用才走 `argo-udp`；其他 UDP 默认直连。这样 TCP 网页和视频不受 UDP 影响。

## 运维、自检与本体维护

首页现在提供两个独立入口：

- **6 自检与修复**：只读检查 daemon/RPC、argoz/cloudflared 二进制权限与 `--version`、配置、Alpine 依赖、内存、磁盘、节点端口；用户选中并二次确认后才执行 `chmod`、`locks`、`env`、`config`、`cloudflared` 等有限修复。永久性二进制错误不会被无限重试，而是停在可操作状态。
- **7 本体维护**：argoz 与 cloudflared 分别提供检查更新、升级、强制重装、查看路径/版本/缓存。下载、SHA256/ELF 校验、备份、原子替换和失败回滚都由 daemon 统一处理，面板不会重复下载。

安装和升级建议：

```bash
bash install.sh          # 幂等：已有健康 argoz/cloudflared 时跳过下载
bash install.sh --force  # 仅需要强制重装时使用
argoz update --check
argoz update --component self
argoz update --component cloudflared
argoz update --offline
argoz update --rollback
```

## 低内存模式

`MemoryMode=auto` 在总内存 ≤96MB 时自动启用低内存基线：

- 默认并发上限 24、UDP 会话上限 8，UDP 仍默认关闭；
- 运行期设置 Go `GOGC=50` / `GOMEMLIMIT=40MiB`，cloudflared 子进程继承更低的 Go 内存目标；
- 可用内存低于 16MB 时拒绝新 WS 会话（503），优先保护已有连接；
- 不后台预下载资源；安装脚本和面板只在缺失/损坏或用户确认时下载。

可用 `auto / low / normal` 手动覆盖；64MB 机器建议 `low`，并只运行 1 个节点。

## 兼容性

| 系统 | 服务托管 |
|---|---|
| Alpine 3.x（musl） | OpenRC `supervise-daemon` |
| Debian 10+ / Ubuntu 18.04+ | systemd |
| CentOS / RHEL / Rocky 8+ | systemd |
| 无 systemd / OpenRC | 手动 nohup 或 crontab @reboot |

二进制以 `CGO_ENABLED=0` 静态编译；构建需 **Go 1.22+**，无任何第三方依赖。

## 从源码构建

```bash
git clone https://github.com/Zzz-IT/argoz.git
cd argoz
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o argoz .
```

## 开发

```bash
go test ./... -race     # 60+ 个用例：协议端到端、WS 帧边界、服务/更新/孤儿清理、退避时序、探针、日志、创建校验、卸载
go vet ./...
```

## 说明

- Argo 隧道本身**只转发 TCP**；argoz 的 UDP 支持是把 mihomo 的标准 VLESS UDP 封装在 TCP/WS 中（非原生 UDP，Hysteria2 / TUIC 不在范围内）
- 本地监听是纯 HTTP，TLS 由 Cloudflare 边缘终结，**不需要任何证书**
- 固定隧道需在 Cloudflare 面板把路由指向 `http://localhost:<节点端口>`

## License

[MIT](LICENSE) · 变更记录见 [CHANGELOG.md](CHANGELOG.md)
