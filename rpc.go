package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ---------- 协议 ----------

type rpcRequest struct {
	Cmd  string          `json:"cmd"`
	Data json.RawMessage `json:"data,omitempty"`
}

type rpcResponse struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// NodePatch 面板可修改的字段（空值表示不改）
type NodePatch struct {
	Name       string `json:"name,omitempty"`
	Port       int    `json:"port,omitempty"`
	Path       string `json:"path,omitempty"`
	Credential string `json:"credential,omitempty"`
	Token      string `json:"token,omitempty"`
	Domain     string `json:"domain,omitempty"`
}

type idReq struct {
	ID string `json:"id"`
}

type updateReq struct {
	ID    string    `json:"id"`
	Patch NodePatch `json:"patch"`
}

type checkPortReq struct {
	Port    int    `json:"port"`
	Exclude string `json:"exclude"`
}

type credReq struct {
	Cred string `json:"cred"`
}

type tailReq struct {
	N int `json:"n"`
}

type nodeResp struct {
	Node Node `json:"node"`
	OK   bool `json:"ok"`
}

type totalsResp struct {
	Nodes int   `json:"nodes"`
	Conns int   `json:"conns"`
	Up    int64 `json:"up"`
	Down  int64 `json:"down"`
}

// EngineAPI 面板依赖的全部能力；本地 *Engine 与远程 *rpcClient 都实现它
type EngineAPI interface {
	Statuses() []NodeStatus
	Nodes() []Node
	Node(id string) (Node, bool)
	Totals() (nodes, conns int, up, down int64)
	Create(spec NodeSpec) (Node, error)
	Update(id string, mutate func(*Node) error) error
	Delete(id string) error
	RestartTunnel(id string) error
	Settings() Settings
	SaveSettings(Settings) error
	CheckPortFree(port int, excludeID string) error
	FindByCredential(cred string) string
	Uninstall(plan uninstallPlan) ([]string, error)
	DataDir() string
}

// ApplyPatch 把补丁应用到节点上（供 RPC 调用）
func (e *Engine) ApplyPatch(id string, p NodePatch) error {
	return e.Update(id, func(n *Node) error {
		if p.Name != "" {
			n.Name = p.Name
		}
		if p.Port != 0 {
			n.Port = p.Port
		}
		if p.Path != "" {
			n.Path = p.Path
		}
		if p.Token != "" {
			n.Tunnel.Token = p.Token
		}
		if p.Domain != "" {
			n.Tunnel.Domain = p.Domain
		}
		if p.Credential != "" {
			if n.Protocol == "trojan" {
				n.Password = p.Credential
			} else {
				n.UUID = p.Credential
			}
		}
		return nil
	})
}

// ---------- 控制通道 ----------

func sockPath(dir string) string { return filepath.Join(dir, "argoz.sock") }
func addrPath(dir string) string { return filepath.Join(dir, "argoz.addr") }

// listenControl 优先用 unix socket，失败则退化为本地 TCP
func listenControl(dir string) (net.Listener, string, error) {
	p := sockPath(dir)
	_ = os.Remove(p)
	if ln, err := net.Listen("unix", p); err == nil {
		_ = os.Chmod(p, 0o600)
		return ln, "unix:" + p, nil
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	_ = os.WriteFile(addrPath(dir), []byte(ln.Addr().String()), 0o600)
	return ln, "tcp:" + ln.Addr().String(), nil
}

func dialControl(dir string) (net.Conn, error) {
	if _, err := os.Stat(sockPath(dir)); err == nil {
		return net.DialTimeout("unix", sockPath(dir), 2*time.Second)
	}
	if b, err := os.ReadFile(addrPath(dir)); err == nil {
		return net.DialTimeout("tcp", strings.TrimSpace(string(b)), 2*time.Second)
	}
	return nil, fmt.Errorf("未找到运行中的 argoz 实例")
}

// ---------- 服务端 ----------

type rpcServer struct {
	eng  *Engine
	ln   net.Listener
	done chan struct{}
	once sync.Once
}

func startRPC(e *Engine, dir string) (*rpcServer, error) {
	ln, _, err := listenControl(dir)
	if err != nil {
		return nil, err
	}
	s := &rpcServer{eng: e, ln: ln, done: make(chan struct{})}
	go s.serve()
	return s, nil
}

func (s *rpcServer) Shutdown() {
	s.once.Do(func() { close(s.done) })
	_ = s.ln.Close()
	_ = os.Remove(sockPath(s.eng.dataDir))
}

func (s *rpcServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			return
		}
		go s.handle(conn)
	}
}

func (s *rpcServer) handle(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(bufio.NewReader(conn))
	enc := json.NewEncoder(conn)

	for {
		var req rpcRequest
		if err := dec.Decode(&req); err != nil {
			return
		}
		resp := s.dispatch(req)
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func (s *rpcServer) ok(v any) rpcResponse {
	b, err := json.Marshal(v)
	if err != nil {
		return rpcResponse{OK: false, Error: err.Error()}
	}
	return rpcResponse{OK: true, Data: b}
}

func (s *rpcServer) fail(err error) rpcResponse {
	return rpcResponse{OK: false, Error: err.Error()}
}

func (s *rpcServer) dispatch(req rpcRequest) rpcResponse {
	switch req.Cmd {
	case "statuses":
		return s.ok(s.eng.Statuses())

	case "nodes":
		return s.ok(s.eng.Nodes())

	case "node":
		var p idReq
		if err := json.Unmarshal(req.Data, &p); err != nil {
			return s.fail(err)
		}
		n, okFound := s.eng.Node(p.ID)
		return s.ok(nodeResp{Node: n, OK: okFound})

	case "totals":
		n, c, u, d := s.eng.Totals()
		return s.ok(totalsResp{n, c, u, d})

	case "create":
		var spec NodeSpec
		if err := json.Unmarshal(req.Data, &spec); err != nil {
			return s.fail(err)
		}
		n, err := s.eng.Create(spec)
		if err != nil {
			return s.fail(err)
		}
		return s.ok(n)

	case "update":
		var p updateReq
		if err := json.Unmarshal(req.Data, &p); err != nil {
			return s.fail(err)
		}
		if err := s.eng.ApplyPatch(p.ID, p.Patch); err != nil {
			return s.fail(err)
		}
		return s.ok(true)

	case "delete":
		var p idReq
		if err := json.Unmarshal(req.Data, &p); err != nil {
			return s.fail(err)
		}
		if err := s.eng.Delete(p.ID); err != nil {
			return s.fail(err)
		}
		return s.ok(true)

	case "restart":
		var p idReq
		if err := json.Unmarshal(req.Data, &p); err != nil {
			return s.fail(err)
		}
		if err := s.eng.RestartTunnel(p.ID); err != nil {
			return s.fail(err)
		}
		return s.ok(true)

	case "settings":
		return s.ok(s.eng.Settings())

	case "setSettings":
		var st Settings
		if err := json.Unmarshal(req.Data, &st); err != nil {
			return s.fail(err)
		}
		if err := s.eng.SaveSettings(st); err != nil {
			return s.fail(err)
		}
		return s.ok(true)

	case "checkPort":
		var p checkPortReq
		if err := json.Unmarshal(req.Data, &p); err != nil {
			return s.fail(err)
		}
		if err := s.eng.CheckPortFree(p.Port, p.Exclude); err != nil {
			return s.fail(err)
		}
		return s.ok(true)

	case "findCred":
		var p credReq
		if err := json.Unmarshal(req.Data, &p); err != nil {
			return s.fail(err)
		}
		return s.ok(s.eng.FindByCredential(p.Cred))

	case "tailLog":
		var p tailReq
		if err := json.Unmarshal(req.Data, &p); err != nil {
			return s.fail(err)
		}
		lines, err := tailLines(LogFilePath(), p.N)
		if err != nil {
			return s.fail(err)
		}
		return s.ok(lines)

	case "uninstall":
		var plan uninstallPlan
		if err := json.Unmarshal(req.Data, &plan); err != nil {
			return s.fail(err)
		}
		lines, err := s.eng.Uninstall(plan)
		if err != nil {
			return rpcResponse{OK: false, Error: err.Error()}
		}
		go func() {
			time.Sleep(200 * time.Millisecond)
			s.Shutdown()
		}()
		return s.ok(lines)
	}
	return s.fail(fmt.Errorf("未知命令: %s", req.Cmd))
}
