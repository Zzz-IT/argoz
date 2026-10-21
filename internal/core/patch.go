package core

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
