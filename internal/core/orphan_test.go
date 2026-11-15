package core

import "testing"

func TestOrphanMatchesNode(t *testing.T) {
	temp := Node{Port: 20001, Tunnel: TunnelCfg{Mode: "temp"}}
	fixed := Node{Tunnel: TunnelCfg{Mode: "fixed", Token: "secret-token"}}

	cases := []struct {
		name string
		args []string
		node Node
		want bool
	}{
		{"temp exact", []string{"cloudflared", "--no-autoupdate", "tunnel", "--url", "http://127.0.0.1:20001"}, temp, true},
		{"temp other port", []string{"cloudflared", "--url", "http://127.0.0.1:20002"}, temp, false},
		{"temp equals", []string{"cloudflared", "--url=http://127.0.0.1:20001"}, temp, true},
		{"fixed exact", []string{"cloudflared", "--no-autoupdate", "tunnel", "run", "--token", "secret-token"}, fixed, true},
		{"fixed other token", []string{"cloudflared", "tunnel", "run", "--token", "other"}, fixed, false},
		{"not cloudflared", []string{"/usr/bin/other", "--url", "http://127.0.0.1:20001"}, temp, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := orphanMatchesNode(tc.args, tc.node); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// 开启 cloudflared 自带自更新后不再传 --no-autoupdate，
// pidfile 路径仍要能认出这些进程，否则孤儿会漏清理。
func TestIsOurCloudflared(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"旧版带 no-autoupdate", []string{"cloudflared", "--no-autoupdate", "tunnel", "--url", "http://127.0.0.1:20001"}, true},
		{"开启自更新 temp", []string{"cloudflared", "tunnel", "--url", "http://127.0.0.1:20001"}, true},
		{"开启自更新 fixed", []string{"cloudflared", "tunnel", "run", "--token", "t"}, true},
		{"cloudflared 但非隧道", []string{"cloudflared", "update"}, false},
		{"非 cloudflared", []string{"/usr/bin/other", "tunnel", "--url", "http://127.0.0.1:20001"}, false},
		{"空参数", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOurCloudflared(tc.args); got != tc.want {
				t.Fatalf("isOurCloudflared(%v)=%v，期望 %v", tc.args, got, tc.want)
			}
		})
	}
}
