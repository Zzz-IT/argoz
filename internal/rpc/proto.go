package rpc

// ProtoVersion 控制通道协议版本；daemon 与面板必须一致，否则视为版本不匹配
const ProtoVersion = 1

type helloResp struct {
	Proto   int    `json:"proto"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
}
