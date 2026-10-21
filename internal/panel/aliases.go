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
