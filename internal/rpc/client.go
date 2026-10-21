package rpc

import (
	"argoz/internal/core"
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

// rpcClient 面板侧客户端：所有操作都转发给常驻 daemon
type rpcClient struct {
	mu            sync.Mutex
	conn          net.Conn
	enc           *json.Encoder
	dec           *json.Decoder
	dir           string
	daemonVersion string
	daemonProto   int
}

func dialRPC(dir string) (*rpcClient, error) {
	conn, err := dialControl(dir)
	if err != nil {
		return nil, err
	}
	c := &rpcClient{
		conn: conn,
		enc:  json.NewEncoder(conn),
		dec:  json.NewDecoder(bufio.NewReader(conn)),
		dir:  dir,
	}
	var hello helloResp
	if err := c.call("hello", nil, &hello); err == nil {
		c.daemonProto = hello.Proto
		c.daemonVersion = hello.Version
	}
	return c, nil
}

func (c *rpcClient) Close() {
	if c != nil && c.conn != nil {
		_ = c.conn.Close()
	}
}

func (c *rpcClient) call(cmd string, in any, out any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var raw json.RawMessage
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		raw = b
	}

	req := rpcRequest{Cmd: cmd, Data: raw}
	if err := c.enc.Encode(req); err != nil {
		if rerr := c.reconnect(); rerr != nil {
			return fmt.Errorf("与 daemon 的连接已断开: %w", err)
		}
		if err := c.enc.Encode(req); err != nil {
			return err
		}
	}

	var resp rpcResponse
	if err := c.dec.Decode(&resp); err != nil {
		return fmt.Errorf("读取 daemon 响应失败: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	if out != nil && len(resp.Data) > 0 {
		return json.Unmarshal(resp.Data, out)
	}
	return nil
}

func (c *rpcClient) reconnect() error {
	conn, err := dialControl(c.dir)
	if err != nil {
		return err
	}
	c.conn = conn
	c.enc = json.NewEncoder(conn)
	c.dec = json.NewDecoder(bufio.NewReader(conn))
	return nil
}

// ---------- API 实现 ----------

func (c *rpcClient) Statuses() []NodeStatus {
	var v []NodeStatus
	_ = c.call("statuses", nil, &v)
	return v
}

func (c *rpcClient) Nodes() []Node {
	var v []Node
	_ = c.call("nodes", nil, &v)
	return v
}

func (c *rpcClient) Node(id string) (Node, bool) {
	var r nodeResp
	if err := c.call("node", idReq{ID: id}, &r); err != nil {
		return Node{}, false
	}
	return r.Node, r.OK
}

func (c *rpcClient) Totals() (int, int, int64, int64) {
	var r totalsResp
	if err := c.call("totals", nil, &r); err != nil {
		return 0, 0, 0, 0
	}
	return r.Nodes, r.Conns, r.Up, r.Down
}

func (c *rpcClient) Create(spec NodeSpec) (Node, error) {
	var n Node
	if err := c.call("create", spec, &n); err != nil {
		return Node{}, err
	}
	return n, nil
}

// Update 在本地副本上执行 mutate，算出差异后发给 daemon
func (c *rpcClient) Update(id string, mutate func(*Node) error) error {
	cur, ok := c.Node(id)
	if !ok {
		return fmt.Errorf("节点不存在")
	}
	next := cur
	if err := mutate(&next); err != nil {
		return err
	}

	p := NodePatch{}
	if next.Name != cur.Name {
		p.Name = next.Name
	}
	if next.Port != cur.Port {
		p.Port = next.Port
	}
	if next.Path != cur.Path {
		p.Path = next.Path
	}
	if next.Credential() != cur.Credential() {
		p.Credential = next.Credential()
	}
	if next.Tunnel.Token != cur.Tunnel.Token {
		p.Token = next.Tunnel.Token
	}
	if next.Tunnel.Domain != cur.Tunnel.Domain {
		p.Domain = next.Tunnel.Domain
	}
	return c.call("update", updateReq{ID: id, Patch: p}, nil)
}

// ApplyPatch 直接下发补丁
func (c *rpcClient) ApplyPatch(id string, p NodePatch) error {
	return c.call("update", updateReq{ID: id, Patch: p}, nil)
}

func (c *rpcClient) Delete(id string) error {
	return c.call("delete", idReq{ID: id}, nil)
}

func (c *rpcClient) RestartTunnel(id string) error {
	return c.call("restart", idReq{ID: id}, nil)
}

func (c *rpcClient) Settings() Settings {
	var s Settings
	_ = c.call("settings", nil, &s)
	return s
}

func (c *rpcClient) SaveSettings(s Settings) error {
	return c.call("setSettings", s, nil)
}

func (c *rpcClient) CheckPortFree(port int, excludeID string) error {
	return c.call("checkPort", checkPortReq{Port: port, Exclude: excludeID}, nil)
}

func (c *rpcClient) FindByCredential(cred string) string {
	var name string
	_ = c.call("findCred", credReq{Cred: cred}, &name)
	return name
}

func (c *rpcClient) Uninstall(plan core.UninstallPlan) ([]string, error) {
	var lines []string
	if err := c.call("uninstall", plan, &lines); err != nil {
		return nil, err
	}
	return lines, nil
}

// ---------- 面板启动时确保 daemon 在跑 ----------

// ensureDaemon 连接已有 daemon；没有就后台拉起一个
func EnsureDaemon(dir string) (*rpcClient, error) {
	if c, err := dialRPC(dir); err == nil {
		return c, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("定位自身程序失败: %w", err)
	}

	cmd := exec.Command(exe, "-d", "-dir", dir)
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = DetachAttr()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动后台 daemon 失败: %w", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		if c, err := dialRPC(dir); err == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("daemon 启动超时，请查看日志 %s", LogFilePath())
}

// DataDir 返回 daemon 的数据目录
func (c *rpcClient) DataDir() string { return c.dir }

func (c *rpcClient) Info() daemonInfo {
	var info daemonInfo
	_ = c.call("info", nil, &info)
	return info
}

func (c *rpcClient) UpdateCheck() (updateInfo, error) {
	var info updateInfo
	if err := c.call("updateCheck", nil, &info); err != nil {
		return updateInfo{}, err
	}
	return info, nil
}

func (c *rpcClient) UpdateApply() (updateResult, error) {
	var res updateResult
	if err := c.call("updateApply", nil, &res); err != nil {
		return updateResult{}, err
	}
	return res, nil
}

// DaemonVersion 返回 daemon 报告的版本（老版本 daemon 可能为空）
func (c *rpcClient) DaemonVersion() string { return c.daemonVersion }

// DaemonProto 返回 daemon 的协议版本（0 表示老版本不支持握手）
func (c *rpcClient) DaemonProto() int { return c.daemonProto }
