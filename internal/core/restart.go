package core

import "errors"

// RestartTunnel 重启指定节点的隧道
func (e *Engine) RestartTunnel(id string) error {
	e.mu.Lock()
	rt := e.rt[id]
	e.mu.Unlock()
	if rt == nil {
		return errors.New("节点未运行")
	}
	if rt.tun != nil {
		rt.tun.Stop()
	}
	t := NewTunnel(e, id)
	rt.mu.Lock()
	rt.tun = t
	rt.mu.Unlock()
	t.Start()
	return nil
}
