package core

// 包内兼容别名：把拆包后的外部符号映射回原名字，保持原文件零改动。

import (
	"argoz/internal/config"
	"argoz/internal/logx"
	"argoz/internal/protocol"
	"argoz/internal/sys"
	"argoz/internal/xutil"
)

type (
	Node         = config.Node
	Settings     = config.Settings
	Config       = config.Config
	TunnelCfg    = config.TunnelCfg
	VlessRequest = protocol.VlessRequest
)

var (
	DefaultDataDir  = config.DefaultDataDir
	LoadConfig      = config.LoadConfig
	defaultSettings = config.DefaultSettings
)

var (
	LogInfo         = logx.LogInfo
	LogOK           = logx.LogOK
	LogWarn         = logx.LogWarn
	LogErr          = logx.LogErr
	LogDebug        = logx.LogDebug
	InitLog         = logx.InitLog
	CloseLog        = logx.CloseLog
	LogFilePath     = logx.LogFilePath
	SetConsoleLevel = logx.SetConsoleLevel
	SetLevelByName  = logx.SetLevelByName
)

var (
	randHex       = xutil.RandHex
	newUUID       = xutil.NewUUID
	randomPath    = xutil.RandomPath
	atomicWrite   = xutil.AtomicWrite
	humanBytes    = xutil.HumanBytes
	humanDuration = xutil.HumanDuration
	padRight      = xutil.PadRight
	padLeft       = xutil.PadLeft
	truncate      = xutil.Truncate
	tailLines     = xutil.TailLines
)

var (
	retryHint             = RetryHint
	readVlessAddr         = protocol.ReadVlessAddr
	parseUUID             = protocol.ParseUUID
	normalizeUUID         = protocol.NormalizeUUID
	vlessHandshake        = protocol.VlessHandshake
	vlessHandshakeRequest = protocol.VlessHandshakeRequest
	vlessResponse         = protocol.VlessResponse
	trojanHandshake       = protocol.TrojanHandshake
)

var (
	RemoveService     = sys.RemoveService
	RemoveShortcut    = sys.RemoveShortcut
	removeShortcut    = sys.RemoveShortcut
	detectServiceKind = sys.DetectServiceKind
	runServiceCommand = sys.RunServiceCommand
)

const (
	serviceNone    = sys.KindNone
	serviceSystemd = sys.KindSystemd
	serviceOpenRC  = sys.KindOpenRC
)

// uninstallPlan 兼容旧测试命名
type uninstallPlan = UninstallPlan
