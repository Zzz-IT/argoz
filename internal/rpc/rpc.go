package rpc

import (
	"argoz/internal/core"
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

type totalsResp struct {
	Nodes int   `json:"nodes"`
	Conns int   `json:"conns"`
	Up    int64 `json:"up"`
	Down  int64 `json:"down"`
}

// API 面板依赖的全部能力；本地 *Engine 与远程 *rpcClient 都实现它
type API interface {
	Statuses() []NodeStatus
	Nodes() []Node
	Node(id string) (Node, bool)
	Totals() (nodes, conns int, up, down int64)
	Create(spec NodeSpec) (Node, error)
	Update(id string, mutate func(*Node) error) error
	ApplyPatch(id string, p NodePatch) error
	Delete(id string) error
	RestartTunnel(id string) error
	Settings() Settings
	SaveSettings(Settings) error
	CheckPortFree(port int, excludeID string) error
	FindByCredential(cred string) string
	Uninstall(plan core.UninstallPlan) ([]string, error)
	DataDir() string
	Info() daemonInfo
	UpdateCheck() (updateInfo, error)
	UpdateApply() (updateResult, error)
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
	eng  API
	ln   net.Listener
	done chan struct{}
	once sync.Once
}

func Start(e API, dir string) (*rpcServer, error) {
	ln, _, err := listenControl(dir)
	if err != nil {
		return nil, err
	}
	s := &rpcServer{eng: e, ln: ln, done: make(chan struct{})}
	go s.serve()
	return s, nil
}

func (s *rpcServer) Done() <-chan struct{} { return s.done }

func (s *rpcServer) Shutdown() {
	s.once.Do(func() { close(s.done) })
	_ = s.ln.Close()
	_ = os.Remove(sockPath(s.eng.DataDir()))
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

	case "info":
		return s.ok(s.eng.Info())

	case "updateCheck":
		info, err := s.eng.UpdateCheck()
		if err != nil {
			return s.fail(err)
		}
		return s.ok(info)

	case "updateApply":
		res, err := s.eng.UpdateApply()
		if err != nil {
			return s.fail(err)
		}
		return s.ok(res)

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
		var plan core.UninstallPlan
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
