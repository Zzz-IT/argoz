# Changelog

本项目遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/) 与 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 格式。

## [Unreleased]

## [1.1.2] - 2026-10-22

### 修复

- **多实例互相抢端口**：新增单实例锁 `<数据目录>/argoz.lock`。已有实例在运行时，第二个 `argoz` 会直接退出并提示 `已有 argoz 实例在运行（pid X）`；进程崩溃留下的陈旧锁会自动接管。
  此前反复手动启动会堆出多个进程，它们互相抢占端口并把"顺延后的端口"写回配置，导致端口自己乱跳、8080 永远改不动。
- **端口段允许写单个端口**：`8080-8080` 现在合法（此前要求起始必须小于结束）。
- **端口占用提示更明确**：附带排查命令 `ss -lntp | grep :PORT`。

## [1.1.1] - 2026-10-22

### 修复

- **VLESS 地址类型编号错误**：VLESS 是 `1=IPv4 / 2=域名 / 3=IPv6`，此前误用了 SOCKS5 的编号（`3=域名 / 4=IPv6`），导致所有以域名为目标的 VLESS 连接握手失败，日志报 `handshake: 不支持的地址类型: 2`。
- **固定隧道误报 up**：改为必须收到 cloudflared 的 `Registered tunnel connection`（临时隧道抓到域名亦可）才置为 up；45 秒未注册自动杀掉重试。
- **编辑端口静默顺延**：手改端口冲突时现在明确报错并回滚，不再偷偷换成别的端口。

### 变更

- 默认端口段从 `20000-20100` 放开为 **`1-65535`**，自动分配从 1024 起（避开特权端口），旧配置自动迁移一次。
- 节点详情新增 **`7 自检`**：本地监听 / DNS / 端到端 HTTPS（显示状态码、CF-Ray、响应片段），并提示 CF 路由与 ECH 注意事项。

## [1.1.0] - 2026-10-22

### 变更

- **移除全屏 TUI**：删除 bubbletea / lipgloss 及全部间接依赖，界面回到纯文本行式菜单，**重新变成零第三方依赖**。
- 二进制 8.47MB → 约 7.5MB；常驻内存减少 2~5MB；CI 不再需要下载依赖。
- `--classic` 保留为兼容参数（现在默认就是行式菜单）。

### 新增

- 新建节点支持**手填端口 / WS 路径 / UUID（或密码）**，留空则自动生成，创建前有确认页。
- 节点列表新增「状态 / 连接」列、错误信息与重试倒计时，按 `r` 刷新。
- 详情页显示重启次数与下次重试时间。

### 修复

- 隧道错误提示改为优先提取真正的错误行，不再显示 usage 末尾的 `--help, -h`。

## [1.0.1] - 2026-10-22

### 修复

- **cloudflared 启动参数顺序错误**：`--no-autoupdate` 是全局 flag，必须位于子命令之前。此前写成 `tunnel --no-autoupdate ...`，cloudflared 会以 `flag provided but not defined` 退出，导致**所有隧道都无法建立**、节点长期停留在降级状态。
- **隧道错误提示不可读**：以前取 cloudflared 输出的最后一行，常显示 usage 末尾的 `--help, -h`；现在优先提取真正的错误行（Incorrect Usage / failed / error 等）。
- **TUI 窄窗口错位**：面板宽度少算了边框与内边距，右侧被截断、表格与快捷键条折行。现在按终端宽度自适应：依次收起「域名 → 协议 → 端口」列，页头与页脚按剩余宽度裁剪。
- **节点信息行跑出面板**：错误/重试提示现按面板宽度截断。

## [1.0.0] - 2026-10-22

首个正式版本：把 Argo（Cloudflare Tunnel）与 WebSocket 代理节点合成一个单二进制工具。

### 新增

- **全屏 TUI 面板**（bubbletea + lipgloss）：卡片式布局、状态色、每秒刷新、vim 键位、8 FPS 限帧
- **经典行式菜单**（`--classic`），并在无 TTY（管道/重定向）时自动降级
- **节点即隧道**：新建节点只需回答名称/协议/隧道模式，端口、WS 路径、UUID 全自动生成
- **协议**：VLESS over WS、Trojan over WS，兼容 mihomo / xray / v2rayN / sing-box 客户端
- **隧道模式**：临时隧道（trycloudflare 自动域名，域名漂移自动回写）与固定隧道（Token + 自有域名）
- **链接与 Clash 同源生成**：URI 与 YAML 由同一数据结构渲染，杜绝两处不一致
- **复制能力**：OSC 52 直达系统剪贴板，另可导出到 `nodes.txt`
- **节点编辑**：名称 / 端口 / WS 路径 / UUID（或密码），热生效不重启进程
- **端口自愈**：启动时端口被占用自动顺延并回写配置
- **5 级保活**：连接级 ping、监听级探针、进程级监督、端到端 HTTPS 探针、进程自愈
- **分层重试**：2s → 4s → 8s → 3min×20 → 60min，±20% 抖动，稳定运行 10 分钟清零
- **永久错误快通道**：1033 / tunnel not found / Token 无效第二次即转 60 分钟重试
- **每小时全量巡检**：所有节点重跑 L1 + L3，纠正状态机漂移
- **日志系统**：4 级过滤、5MB 轮转、TUI 模式静音，面板内可查看
- **卸载**：面板 `u`、菜单 `9`、`install.sh --uninstall [--purge]` 三种入口，需输入 DELETE 确认
- **部署**：systemd 与 OpenRC 双支持，开机自启，`GOMEMLIMIT`/`GOGC`/`MemoryMax` 内存护栏
- **cloudflared 三处兜底**：安装脚本预装 / 进程启动预热 / 节点启动兜底（失败缓存 30s）

### 说明

- Argo 隧道仅转发 TCP，因此不支持 UDP 协议（Hysteria2 / TUIC 不在范围内）
- 本地监听为纯 HTTP，TLS 由 Cloudflare 边缘终结，无需任何证书
- 面向 Linux（x86_64 / aarch64），Windows / macOS 仅用于开发调试

[Unreleased]: https://github.com/Zzz-IT/argoz/compare/v1.1.2...HEAD
[1.1.2]: https://github.com/Zzz-IT/argoz/releases/tag/v1.1.2
[1.1.1]: https://github.com/Zzz-IT/argoz/releases/tag/v1.1.1
[1.1.0]: https://github.com/Zzz-IT/argoz/releases/tag/v1.1.0
[1.0.1]: https://github.com/Zzz-IT/argoz/releases/tag/v1.0.1
[1.0.0]: https://github.com/Zzz-IT/argoz/releases/tag/v1.0.0
