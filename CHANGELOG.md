# Changelog

本项目遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/) 与 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 格式。

## [Unreleased]

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

[Unreleased]: https://github.com/Zzz-IT/argoz/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/Zzz-IT/argoz/releases/tag/v1.0.0
