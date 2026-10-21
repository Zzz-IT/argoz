package sys

import (
	"strings"
	"testing"
)

func TestRenderSystemdUnit(t *testing.T) {
	unit := renderSystemdUnit("/opt/argoz/bin/argoz", "/srv/argoz-data")
	for _, want := range []string{
		"ExecStart=/opt/argoz/bin/argoz -d -dir /srv/argoz-data",
		"ReadWritePaths=/srv/argoz-data",
		"Restart=always",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("systemd unit missing %q:\n%s", want, unit)
		}
	}
}

func TestRenderOpenRC(t *testing.T) {
	script := renderOpenRC("/opt/argoz/bin/argoz", "/srv/argoz-data")
	for _, want := range []string{
		`command="/opt/argoz/bin/argoz"`,
		`command_args="-d -dir /srv/argoz-data"`,
		"supervise-daemon",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("openrc script missing %q:\n%s", want, script)
		}
	}
}
