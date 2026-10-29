package panel

import (
	"argoz/internal/config"
	"argoz/internal/core"
	"argoz/internal/logx"
	"argoz/internal/protocol"
	"argoz/internal/xutil"
)

type (
	Node          = core.Node
	Settings      = core.Settings
	NodeStatus    = core.NodeStatus
	NodeSpec      = core.NodeSpec
	UninstallPlan = core.UninstallPlan
	Config        = config.Config

	// rpc 新接口使用 core 中的导出类型；这里做面板内别名，
	// 避免依赖 rpc 包里的未导出兼容别名。
	DaemonInfo     = core.DaemonInfo
	UpdateInfo     = core.UpdateInfo
	UpdateResult   = core.UpdateResult
	DoctorReport   = core.DoctorReport
	DoctorCheck    = core.DoctorCheck
	ArtifactStatus = core.ArtifactStatus
)

var (
	humanBytes    = xutil.HumanBytes
	humanDuration = xutil.HumanDuration
	padRight      = xutil.PadRight
	padLeft       = xutil.PadLeft
	truncate      = xutil.Truncate
	dispWidth     = xutil.DispWidth
	tailLines     = xutil.TailLines

	LogInfo  = logx.LogInfo
	LogOK    = logx.LogOK
	LogWarn  = logx.LogWarn
	LogErr   = logx.LogErr
	LogDebug = logx.LogDebug

	LogFilePath = logx.LogFilePath

	retryHint    = core.RetryHint
	extractToken = core.ExtractToken

	randHex       = xutil.RandHex
	newUUID       = xutil.NewUUID
	parseUUID     = protocol.ParseUUID
	normalizeUUID = protocol.NormalizeUUID
)
