package cli

import (
	"strings"
	"testing"
)

func TestServerAllowedHostRequiresToken(t *testing.T) {
	h := newHarness(t)

	// 1. Non-loopback --allowed-host without token => ExitUsage
	code, _, errOut := h.run("server", "--allowed-host", "my-tunnel.ngrok-free.app")
	if code != ExitUsage {
		t.Fatalf("expected code %d, got %d; stderr: %s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "without an authentication token") {
		t.Fatalf("expected token refusal error, got: %s", errOut)
	}

	// 2. Non-loopback bind host without token => ExitUsage
	code, _, errOut = h.run("server", "--host", "0.0.0.0")
	if code != ExitUsage {
		t.Fatalf("expected code %d, got %d; stderr: %s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "without an authentication token") {
		t.Fatalf("expected token refusal error, got: %s", errOut)
	}

	// 3. Non-loopback config allowed_hosts without token => ExitUsage
	h.cfg.AllowedHosts = []string{"tunnel.example.com"}
	if err := h.cfg.SaveFile(h.cfgF); err != nil {
		t.Fatalf("SaveFile: %v", err)
	}
	code, _, errOut = h.run("server")
	if code != ExitUsage {
		t.Fatalf("expected code %d, got %d; stderr: %s", ExitUsage, code, errOut)
	}
	if !strings.Contains(errOut, "without an authentication token") {
		t.Fatalf("expected token refusal error, got: %s", errOut)
	}
}
