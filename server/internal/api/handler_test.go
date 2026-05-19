package api

import (
	"testing"

	"github.com/stealthpte/server/internal/config"
)

func TestValidateTunnel(t *testing.T) {
	h := &Handler{cfg: &config.Config{PortMin: 10000, PortMax: 20000}}

	cases := []struct {
		proto  string
		port   int
		target string
		want   string
	}{
		{"tcp", 10080, "192.168.1.10:80", ""},
		{"udp", 10053, "192.168.1.1:53", ""},
		{"tcp", 10080, "[::1]:8080", ""},
		{"tcp", 10080, "localhost:3000", ""},
		{"bad", 10080, "192.168.1.1:80", "proto must be tcp or udp"},
		{"tcp", 9999, "192.168.1.1:80", "port out of allowed range"},
		{"tcp", 20001, "192.168.1.1:80", "port out of allowed range"},
		{"tcp", 10080, "", "target_addr required"},
		{"tcp", 10080, "   ", "target_addr required"},
		{"tcp", 10080, "192.168.1.1", "target_addr must be in host:port format (e.g. 192.168.1.10:80)"},
		{"tcp", 10080, ":80", "target_addr host must not be empty"},
		{"tcp", 10080, "192.168.1.1:0", "target_addr port must be a number between 1 and 65535"},
		{"tcp", 10080, "192.168.1.1:65536", "target_addr port must be a number between 1 and 65535"},
		{"tcp", 10080, "192.168.1.1:http", "target_addr port must be a number between 1 and 65535"},
	}

	for _, c := range cases {
		got := h.validateTunnel(c.proto, c.port, c.target)
		if got != c.want {
			t.Errorf("validateTunnel(%q, %d, %q) = %q, want %q", c.proto, c.port, c.target, got, c.want)
		}
	}
}
