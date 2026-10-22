package config

import (
	"fmt"
	"net/url"
	"strings"
)

const echSuffix = "cloudflare-ech.com"

// sniFor 根据 ECH 开关决定 SNI
func sniFor(domain string, ech bool) string {
	if ech {
		return echSuffix
	}
	return domain
}

// URI 生成分享链接（未分配域名时返回空串）
func (n Node) URI(s Settings) string {
	if strings.TrimSpace(n.Tunnel.Domain) == "" {
		return ""
	}
	domain := n.Tunnel.Domain
	sni := sniFor(domain, s.ECH)
	escPath := url.QueryEscape(n.Path)
	name := url.QueryEscape(n.Name)

	switch n.Protocol {
	case "trojan":
		return fmt.Sprintf("trojan://%s@%s:443?security=tls&type=ws&host=%s&path=%s&sni=%s&fp=chrome#%s",
			url.QueryEscape(n.Password), domain, domain, escPath, sni, name)
	default:
		return fmt.Sprintf("vless://%s@%s:443?encryption=none&security=tls&type=ws&host=%s&path=%s&sni=%s&fp=chrome#%s",
			n.UUID, domain, domain, escPath, sni, name)
	}
}

// YAML 生成 clash / mihomo 片段（与 URI 同源）
func (n Node) YAML(s Settings) string {
	if strings.TrimSpace(n.Tunnel.Domain) == "" {
		return ""
	}
	domain := n.Tunnel.Domain

	var b strings.Builder
	b.WriteString("- name: " + n.Name + "\n")

	udp := "false"
	if s.UDPEnabled && n.Protocol == "vless" {
		udp = "true"
	}

	if n.Protocol == "trojan" {
		b.WriteString("  type: trojan\n")
		b.WriteString("  server: " + domain + "\n")
		b.WriteString("  port: 443\n")
		b.WriteString("  password: " + n.Password + "\n")
		b.WriteString("  udp: " + udp + "\n")
		b.WriteString("  network: ws\n")
		b.WriteString("  sni: " + domain + "\n")
		b.WriteString("  client-fingerprint: chrome\n")
		b.WriteString("  skip-cert-verify: false\n")
	} else {
		b.WriteString("  type: vless\n")
		b.WriteString("  server: " + domain + "\n")
		b.WriteString("  port: 443\n")
		b.WriteString("  uuid: " + n.UUID + "\n")
		b.WriteString("  tls: true\n")
		b.WriteString("  udp: " + udp + "\n")
		b.WriteString("  network: ws\n")
		b.WriteString("  servername: " + domain + "\n")
		b.WriteString("  client-fingerprint: chrome\n")
		b.WriteString("  skip-cert-verify: false\n")
	}

	b.WriteString("  ws-opts:\n")
	b.WriteString("    path: " + n.Path + "\n")
	b.WriteString("    headers:\n")
	b.WriteString("      Host: " + domain + "\n")

	if s.ECH {
		b.WriteString("  ech-opts:\n")
		b.WriteString("    enable: true\n")
		b.WriteString("    query-server-name: " + echSuffix + "\n")
	}

	return b.String()
}
