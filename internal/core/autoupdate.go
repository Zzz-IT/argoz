package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"argoz/internal/config"
	"argoz/internal/xutil"
)

const (
	// autoUpdateInterval 成功检查后的下一次检查间隔。
	autoUpdateInterval = 24 * time.Hour
	// autoUpdateFirstRetry 第一次失败后的重试间隔，之后按 2 倍退避。
	autoUpdateFirstRetry = time.Hour
	// autoUpdateMaxRetry 退避上限，避免长期失败的机器被越推越远。
	autoUpdateMaxRetry = 6 * time.Hour
	// autoUpdateInitialDelay 启动后延迟首轮检查，避开启动阶段的资源竞争与重启风暴。
	autoUpdateInitialDelay = 10 * time.Minute
	// autoUpdateTick 调度粒度：每 30 分钟判断一次「到点没」。
	autoUpdateTick = 30 * time.Minute
	// autoUpdateConfirmDelay 升级后需稳定运行多久才确认新版本健康。
	autoUpdateConfirmDelay = 3 * time.Minute
	// autoUpdateMaxDefer 已安装但一直有流量时，最多等待多久再强制重启生效。
	autoUpdateMaxDefer = 7 * 24 * time.Hour
	// autoUpdateRollbackBoots 同一版本连续启动达到该次数仍未确认，判定升级失败并回滚。
	autoUpdateRollbackBoots = 2
)

// autoUpdateState 后台自更新的持久化状态。
// 跨进程重启保留：否则 supervisor 频繁拉起 daemon 会让「24 小时一次」永远轮不到，
// 也无法在升级失败后识别出「上次装上去的版本没活下来」。
type autoUpdateState struct {
	// NextCheckAt 下次允许检查的时间（Unix 秒）。0 表示从未检查过。
	NextCheckAt int64 `json:"next_check_at"`
	// FailStreak 连续失败次数，用于指数退避；成功检查后清零。
	FailStreak int `json:"fail_streak"`
	// LastError 最近一次失败原因，供状态展示。
	LastError string `json:"last_error"`
	// Pending 已安装但尚未确认健康的版本；非空表示有一次升级在验证中。
	Pending string `json:"pending"`
	// PendingSince 安装该版本的时间（Unix 秒）。
	PendingSince int64 `json:"pending_since"`
	// PendingBoots 该版本已启动的次数；达到阈值仍没确认就回滚。
	PendingBoots int `json:"pending_boots"`
}

func autoUpdateStatePath(dataDir string) string {
	return filepath.Join(dataDir, "update_state.json")
}

func loadAutoUpdateState(path string) autoUpdateState {
	var st autoUpdateState
	data, err := os.ReadFile(path)
	if err != nil {
		return autoUpdateState{}
	}
	_ = json.Unmarshal(data, &st)
	return st
}

func saveAutoUpdateState(path string, st autoUpdateState) {
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = xutil.AtomicWrite(path, data, 0o600)
}

// autoUpdateRetryDelay 连续失败后的重试间隔：1h → 2h → 4h → 6h 封顶。
func autoUpdateRetryDelay(streak int) time.Duration {
	if streak <= 1 {
		return autoUpdateFirstRetry
	}
	d := autoUpdateFirstRetry
	for i := 1; i < streak; i++ {
		d *= 2
		if d >= autoUpdateMaxRetry {
			return autoUpdateMaxRetry
		}
	}
	return d
}

// autoUpdateNextCheck 计算下一次检查时间；失败按退避，成功按固定周期。
func autoUpdateNextCheck(now time.Time, streak int) int64 {
	if streak > 0 {
		return now.Add(autoUpdateRetryDelay(streak)).Unix()
	}
	return now.Add(autoUpdateInterval).Unix()
}

func autoUpdateDue(st autoUpdateState, now time.Time) bool {
	return now.Unix() >= st.NextCheckAt
}

// pendingDecision 启动时对待验证升级的处理决定。
type pendingDecision int

const (
	// pendingNone 没有待验证版本。
	pendingNone pendingDecision = iota
	// pendingAwaitRestart 已安装新版本但还没重启过，等空闲时重启。
	pendingAwaitRestart
	// pendingCountBoot 正在运行待验证版本，记录一次启动。
	pendingCountBoot
	// pendingRollback 待验证版本反复起不来，需要回滚。
	pendingRollback
	// pendingStale 标记已失效（运行的是别的版本且不是等待重启阶段），直接清理。
	pendingStale
)

// decidePendingOnBoot 判断启动时如何处理待验证升级。
// running 是当前进程的版本号。
func decidePendingOnBoot(st autoUpdateState, running string) (pendingDecision, int) {
	if st.Pending == "" {
		return pendingNone, 0
	}
	sameVersion := normalizeVersion(st.Pending) == normalizeVersion(running)
	if sameVersion {
		boots := st.PendingBoots + 1
		if boots >= autoUpdateRollbackBoots {
			return pendingRollback, boots
		}
		return pendingCountBoot, boots
	}
	if st.PendingBoots == 0 {
		// 新版本已落盘但还没重启进它，属于正常的等待重启阶段。
		return pendingAwaitRestart, st.PendingBoots
	}
	// 曾经启动待验证版本，现在跑的却是别的版本：多半已被手工回滚或替换。
	return pendingStale, st.PendingBoots
}

// pendingConfirmed 判断待验证版本是否已稳定运行到可以确认。
func pendingConfirmed(st autoUpdateState, running string, uptime time.Duration) bool {
	if st.Pending == "" || st.PendingBoots == 0 {
		return false
	}
	if normalizeVersion(st.Pending) != normalizeVersion(running) {
		return false
	}
	return uptime >= autoUpdateConfirmDelay
}

// pendingDeferExpired 判断「已安装待重启」是否已超过最长等待。
func pendingDeferExpired(st autoUpdateState, now time.Time) bool {
	if st.Pending == "" || st.PendingSince <= 0 {
		return false
	}
	return now.Sub(time.Unix(st.PendingSince, 0)) >= autoUpdateMaxDefer
}

// cloudflaredAutoUpdateEnabled 决定本次拉起 cloudflared 时是否启用它自带的周期性自更新。
// 低内存模式（含运行期 OOM 降级）一律禁用：面板此时会隐藏开关，若只按配置取值，
// 用户既关不掉、cloudflared 又会自行下载二进制，正好顶爆小内存机器。
func cloudflaredAutoUpdateEnabled(s Settings) bool {
	return s.AutoUpdateCloudflared && !LowMemoryActive()
}

// autoUpdateRepo 后台自更新固定使用官方仓库。
// ARGOZ_REPO 是环境可改写的变量，手动更新时由管理员负责；
// 无人值守执行时不应允许把更新源指向任意仓库。
func autoUpdateRepo() string { return defaultUpdateRepo }

// startAutoUpdate 启动后台自更新循环。仅 daemon 模式运行；
// 面板进程是短生命周期客户端，不在其中做任何更新。
func (e *Engine) startAutoUpdate(ctx context.Context) {
	if !e.daemon {
		return
	}
	e.autoUpdateOnce.Do(func() {
		go e.autoUpdateLoop(ctx)
	})
}

func (e *Engine) autoUpdateLoop(ctx context.Context) {
	// 启动即处理升级验证：崩溃回滚要尽快发生，不能等首轮检查的延迟。
	e.checkPendingOnBoot(ctx)

	select {
	case <-ctx.Done():
		return
	case <-time.After(autoUpdateInitialDelay):
	}
	e.autoUpdateRound(ctx)

	t := time.NewTicker(autoUpdateTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.autoUpdateRound(ctx)
		}
	}
}

// checkPendingOnBoot 处理上次升级留下的待验证标记。
func (e *Engine) checkPendingOnBoot(ctx context.Context) {
	statePath := autoUpdateStatePath(e.dataDir)
	st := loadAutoUpdateState(statePath)
	if st.Pending == "" {
		return
	}

	decision, boots := decidePendingOnBoot(st, config.Version)
	switch decision {
	case pendingNone, pendingAwaitRestart:
		if decision == pendingAwaitRestart {
			// 已经装好等着重启，交给后续轮次在空闲时执行。
			return
		}
		return
	case pendingCountBoot:
		st.PendingBoots = boots
		saveAutoUpdateState(statePath, st)
	case pendingStale:
		LogInfo("自动更新：待验证版本 %s 与当前版本 %s 不符，清理升级标记", st.Pending, config.Version)
		clearPending(&st)
		saveAutoUpdateState(statePath, st)
	case pendingRollback:
		LogErr("自动更新：版本 %s 连续 %d 次启动后仍未确认健康，回滚到上一版本", st.Pending, boots)
		if _, err := rollbackComponent(ctx, e.dataDir, "self"); err != nil {
			LogErr("自动更新：回滚失败，保留当前二进制以免陷入循环：%v", err)
			clearPending(&st)
			saveAutoUpdateState(statePath, st)
			return
		}
		clearPending(&st)
		saveAutoUpdateState(statePath, st)
		if _, err := e.restartAfterSelfUpdate(UpdateResult{Replaced: true}); err != nil {
			LogWarn("自动更新：回滚后重启失败，将在下次服务重启时生效：%v", err)
		}
	}
}

func clearPending(st *autoUpdateState) {
	st.Pending = ""
	st.PendingSince = 0
	st.PendingBoots = 0
}

// autoUpdateRound 执行一轮调度。
// cloudflared 不在这里处理：开启开关后由它自带的周期性自更新负责。
func (e *Engine) autoUpdateRound(ctx context.Context) {
	statePath := autoUpdateStatePath(e.dataDir)
	st := loadAutoUpdateState(statePath)
	now := time.Now()
	changed := false

	// 升级验证与低内存无关，必须先于低内存门禁处理。
	if e.confirmOrDeferPending(ctx, &st, now) {
		changed = true
	}

	if LowMemoryActive() || !e.Settings().AutoUpdateSelf {
		if changed {
			saveAutoUpdateState(statePath, st)
		}
		return
	}

	if autoUpdateDue(st, now) {
		e.autoUpdateSelf(ctx, &st, now)
		changed = true
	}
	if changed {
		saveAutoUpdateState(statePath, st)
	}
}

// confirmOrDeferPending 处理待验证升级：确认健康、清理失效标记、或在空闲时重启生效。
func (e *Engine) confirmOrDeferPending(ctx context.Context, st *autoUpdateState, now time.Time) bool {
	if st.Pending == "" {
		return false
	}

	if pendingConfirmed(*st, config.Version, time.Since(procStart)) {
		LogInfo("自动更新：%s 已稳定运行，确认升级完成", st.Pending)
		clearPending(st)
		return true
	}

	// 已安装但还没重启进新版本。
	if normalizeVersion(st.Pending) != normalizeVersion(config.Version) && st.PendingBoots == 0 {
		busy := e.busySessions()
		if busy > 0 && !pendingDeferExpired(*st, now) {
			return false
		}
		if busy > 0 {
			LogWarn("自动更新：%s 已安装但等待空闲超过 %s，强制重启生效（会中断 %d 条会话）",
				st.Pending, autoUpdateMaxDefer, busy)
		}
		LogInfo("自动更新：重启到已安装的新版本 %s", st.Pending)
		if _, err := e.restartAfterSelfUpdate(UpdateResult{Replaced: true}); err != nil {
			LogWarn("自动更新：重启新版本失败，将在下次服务重启时生效：%v", err)
			clearPending(st)
			return true
		}
		return true
	}
	return false
}

// busySessions 返回当前仍在转发的会话数（WS 连接 + UDP 会话）。
// 只统计 WS 连接会把 UDP 转发中的机器误判为空闲，重启即掉流量。
func (e *Engine) busySessions() int {
	e.mu.RLock()
	rts := make([]*nodeRuntime, 0, len(e.rt))
	for _, v := range e.rt {
		rts = append(rts, v)
	}
	e.mu.RUnlock()

	total := 0
	for _, rt := range rts {
		total += int(rt.conns.Load()) + int(rt.udpConns.Load())
	}
	return total
}

// autoUpdateSelf 检查并安装 argoz 新版本。
// 有新版本就先下载安装（不依赖空闲），重启时机交给 confirmOrDeferPending：
// 这样忙站点也能把版本装好，只在真正空闲时重启，不至于永远停在旧版本。
func (e *Engine) autoUpdateSelf(ctx context.Context, st *autoUpdateState, now time.Time) {
	fail := func(msg string) {
		st.FailStreak++
		st.LastError = msg
		st.NextCheckAt = autoUpdateNextCheck(now, st.FailStreak)
		LogWarn("自动更新：%s（连续失败 %d 次，%s 后重试）", msg, st.FailStreak, autoUpdateRetryDelay(st.FailStreak))
	}
	done := func() {
		st.FailStreak = 0
		st.LastError = ""
		st.NextCheckAt = autoUpdateNextCheck(now, 0)
	}

	// 自更新只发布了 Linux amd64/arm64 资产，其他平台是永久不支持，
	// 直接按「已处理」返回，避免退进失败重试循环里刷日志。
	if _, err := updateAssetName(); err != nil {
		st.LastError = err.Error()
		st.NextCheckAt = autoUpdateNextCheck(now, 0)
		LogInfo("自动更新：%v，跳过本体自更新", err)
		return
	}
	// 开发构建不参与无人值守升级，否则本地 go build 的产物会被 release 二进制覆盖。
	if config.Channel != config.ChannelRelease {
		st.NextCheckAt = autoUpdateNextCheck(now, 0)
		return
	}

	mgr := artifactManagerFor(e.dataDir)
	checkCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	info, err := checkSelfUpdate(checkCtx, mgr, false, autoUpdateRepo())
	cancel()
	if err != nil {
		fail("检查版本失败：" + err.Error())
		return
	}
	if !info.HasUpdate {
		done()
		return
	}

	applyCtx, cancelApply := context.WithTimeout(ctx, 10*time.Minute)
	_, res, err := applySelfUpdate(applyCtx, mgr, e.dataDir, false, false, autoUpdateRepo())
	cancelApply()
	if err != nil {
		fail("安装失败：" + err.Error())
		return
	}
	st.FailStreak = 0
	st.LastError = ""
	st.NextCheckAt = autoUpdateNextCheck(now, 0)

	if !res.Replaced {
		return
	}
	// 记为待验证：重启后由 checkPendingOnBoot 计数，稳定运行才确认，反复起不来则回滚。
	st.Pending = info.Latest
	st.PendingSince = now.Unix()
	st.PendingBoots = 0
	LogInfo("自动更新：已安装 argoz %s，将在空闲时重启生效", info.Latest)

	if busy := e.busySessions(); busy > 0 {
		LogInfo("自动更新：当前有 %d 条会话在转发，稍后空闲时再重启", busy)
		return
	}
	if _, err := e.restartAfterSelfUpdate(res); err != nil {
		LogWarn("自动更新：重启新版本失败，将在下次服务重启时生效：%v", err)
	}
}

// AutoUpdateStatus 汇总后台自更新的策略与状态，供 `argoz update --status` 展示。
func AutoUpdateStatus(dataDir string) ([]string, error) {
	cfg, err := LoadConfig(dataDir)
	if err != nil {
		return nil, err
	}
	s := cfg.Settings
	low := s.EffectiveLowMemory(CurrentMemInfo().EffectiveLimitMB())
	st := loadAutoUpdateState(autoUpdateStatePath(dataDir))

	lowText := "否"
	if low {
		lowText = "是（自更新固定手动）"
	}
	selfText := autoUpdateOnOff(s.AutoUpdateSelf && !low)
	if s.AutoUpdateSelf && !low && config.Channel != config.ChannelRelease {
		selfText = "开启（当前为 " + config.Channel + " 构建，不参与后台更新）"
	}
	lines := []string{
		fmt.Sprintf("当前版本    %s（通道 %s）", config.Version, config.Channel),
		fmt.Sprintf("低内存模式  %s", lowText),
		fmt.Sprintf("本体自更新  %s", selfText),
		fmt.Sprintf("CF 自更新   %s", autoUpdateOnOff(s.AutoUpdateCloudflared && !low)),
	}
	if st.NextCheckAt > 0 {
		lines = append(lines, fmt.Sprintf("下次检查    %s", time.Unix(st.NextCheckAt, 0).Format("2006-01-02 15:04:05")))
	} else {
		lines = append(lines, "下次检查    尚未计划（daemon 启动约 10 分钟后首检）")
	}
	if st.FailStreak > 0 {
		lines = append(lines, fmt.Sprintf("连续失败    %d 次，最近：%s", st.FailStreak, st.LastError))
	}
	if st.Pending != "" {
		lines = append(lines, fmt.Sprintf("待验证版本  %s（已启动 %d 次，稳定 %s 后确认）",
			st.Pending, st.PendingBoots, autoUpdateConfirmDelay))
	}
	return lines, nil
}

func autoUpdateOnOff(on bool) string {
	if on {
		return "开启"
	}
	return "关闭（手动）"
}

// AutoUpdateStatus 是 Engine 的 RPC 入口：返回后台自更新策略与状态。
func (e *Engine) AutoUpdateStatus() ([]string, error) {
	return AutoUpdateStatus(e.dataDir)
}
