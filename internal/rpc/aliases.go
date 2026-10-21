package rpc

// 包内兼容别名：保持原 main 包代码的可读名字

import (
	"argoz/internal/core"
	"argoz/internal/logx"
	"argoz/internal/xutil"
)

type (
	Node         = core.Node
	Settings     = core.Settings
	NodeStatus   = core.NodeStatus
	NodeSpec     = core.NodeSpec
	daemonInfo   = core.DaemonInfo
	updateInfo   = core.UpdateInfo
	updateResult = core.UpdateResult
	NodePatch    = core.NodePatch
)

var (
	tailLines   = xutil.TailLines
	LogFilePath = logx.LogFilePath
	LogInfo     = logx.LogInfo
	LogWarn     = logx.LogWarn
	LogErr      = logx.LogErr
)
