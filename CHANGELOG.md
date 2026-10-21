# Changelog

本项目遵循 [Semantic Versioning](https://semver.org/lang/zh-CN/) 与 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/) 格式。

## [1.3.1] - 2026-10-22

### 修复

- **升级后残留旧 daemon**：`argoz service install` 由 `start` 改为 `restart`（systemd / OpenRC 同步）。此前服务已在运行时 `start` 是空操作，新面板会连到旧版本 daemon，检查更新报 `未知命令: updateCheck`。
- **版本一致性提示**：面板启动时读取 daemon 版本，与自身不一致时在主菜单红字提示并给出重启命令。

## [1.3.0] - 2026-10-22

### 新增

- **快捷命令 `az`**：安装时自动创建 `/usr/local/bin/az -> argoz` 符号链接，输入 `az` 即唤出面板；`install.sh` 与 `argoz service install` 都会补齐，卸载时一并删除（同名非链接文件不覆盖）。
- **内置服务托管**：`argoz service install | status | uninstall`，自动适配 systemd 与 OpenRC；`install.sh` 改为调用该命令，避免两套模板漂移。
- **自更新**：`argoz update` 从 GitHub Release 下载对应架构二进制，校验 SHA256 与 ELF 后原子替换，并尝试重启已安装服务；`argoz update --check` 只检查版本。
- **Alpine 运行时自检**：daemon 启动时检查 `ca-certificates` / `tzdata` / `gcompat`，root 下尝试 `apk add`，非 root 或失败只告警。
- **孤儿 cloudflared 清理**：Linux 下给 cloudflared 与 supervisor→daemon 子进程设置 `Pdeathsig`，daemon 启动时按节点端口 / Token 精确清理遗留进程，不使用宽泛 `pkill -f`。
- **运行状态页（`6`）**：采样 1 秒计算每节点实时上下行速率。
- **检查更新页（`5`）**：查询 GitHub Releases、展示更新说明、一键下载校验替换并重启服务。

### 变更（界面）

- 状态与标签全面中文化：固定隧道 / 临时隧道、运行中 / 未运行 / 启动中 / 降级 / 错误、30 秒 / 5 分钟等；**配置里仍存 `fixed`/`temp`/`vless`**，不影响兼容性。
- 节点列表：按终端宽度自适应（≥96 列显示域名，≥76 列显示协议），15 行分页，`/` 搜索，`r` 刷新。
- 节点详情：超长分享链接主动折行缩进；内容超过一屏时用 `n/p` 翻屏。
- 日志：分页 + 级别过滤（`e` 错误 / `w` 警告 / `i` 信息 / `d` 调试 / `a` 全部 / `g` 最新）。
- 设置页底部显示守护进程进程号、运行时长、内存占用。
- 主菜单改为两行状态（服务 + 守护进程），增加降级告警行与新版本提示行。

### 变更（代码结构）

- 单体 63 个文件拆成 8 个包：`internal/{config,core,rpc,panel,protocol,sys,logx,xutil}`，`main` 仅保留入口（约 190 行）。
- 依赖单向：`panel → rpc → core → 基础包`，无循环。
- 测试随包迁移（config / core / logx / rpc / sys）。
- 版本变量移到 `config.Version`，发布流水线 ldflags 同步更新为 `-X argoz/internal/config.Version`。

### 修复

- 自更新替换二进制后对目标目录执行 fsync，降低掉电丢目录项的风险。
- 非 Alpine 系统缺少 CA 证书时输出明确告警。
- 非 root 运行时，孤儿 cloudflared 清理只处理同用户进程并记录调试日志。
- 降低 daemon 被 `kill -9` 后重复启动 cloudflared、重复隧道的风险。

## [1.2.0] - 2026-10-22

### 变更

- **改为双进程模型**：\`argoz\` 变成纯客户端面板，\`argoz -d\` 是常驻 daemon（跑节点与保活），两者通过本地 socket（unix socket，失败退化本地 TCP）通信。
  - 面板退出不再影响服务；面板崩溃也不会带走节点
  - 面板启动时若没有 daemon，会自动在后台拉起一个
  - 多个面板可以同时连接，写操作由 daemon 串行执行
- 新增 \`argoz --supervise\`：自带看护的父进程，daemon 崩溃后按 1s→30s 退避重启，适合没有 systemd/OpenRC 的容器。
- 单实例锁拆成两把：\`argoz.lock\`（daemon）与 \`argoz-supervisor.lock\`（supervisor），互不干扰。

### 说明

- systemd / OpenRC 仍然直接托管 \`argoz -d\`，不需要 supervisor

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

[Unreleased]: https://github.com/Zzz-IT/argoz/compare/v1.3.1...HEAD
[1.3.1]: https://github.com/Zzz-IT/argoz/releases/tag/v1.3.1
[1.3.0]: https://github.com/Zzz-IT/argoz/releases/tag/v1.3.0
[1.2.0]: https://github.com/Zzz-IT/argoz/releases/tag/v1.2.0
[1.1.2]: https://github.com/Zzz-IT/argoz/releases/tag/v1.1.2
[1.1.1]: https://github.com/Zzz-IT/argoz/releases/tag/v1.1.1
[1.1.0]: https://github.com/Zzz-IT/argoz/releases/tag/v1.1.0
[1.0.1]: https://github.com/Zzz-IT/argoz/releases/tag/v1.0.1
[1.0.0]: https://github.com/Zzz-IT/argoz/releases/tag/v1.0.0
