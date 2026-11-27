package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestMCPBManifest keeps the desktop-extension manifest (ADR 156) in step
// with the server: every env var it sets is one the server reads, every
// ${user_config.x} it references is declared, and the credential fields
// are marked sensitive so the app keeps them in the system keychain.
func TestMCPBManifest(t *testing.T) {
	b, err := os.ReadFile("packaging/mcpb/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Version string `json:"version"`
		Server  struct {
			MCPConfig struct {
				Env       map[string]string `json:"env"`
				Overrides map[string]struct {
					Command string `json:"command"`
				} `json:"platform_overrides"`
			} `json:"mcp_config"`
		} `json:"server"`
		UserConfig map[string]struct {
			Sensitive bool `json:"sensitive"`
		} `json:"user_config"`
		Compatibility struct {
			Platforms []string `json:"platforms"`
		} `json:"compatibility"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m.Version != "@VERSION@" {
		t.Errorf("version = %q; the build script fills it from main.go", m.Version)
	}
	src, err := os.ReadFile("internal/config/config.go")
	if err != nil {
		t.Fatal(err)
	}
	ref := regexp.MustCompile(`\$\{user_config\.([a-z_]+)\}`)
	for k, v := range m.Server.MCPConfig.Env {
		if !strings.Contains(string(src), `"`+k+`"`) {
			t.Errorf("env %s is not read by internal/config", k)
		}
		for _, r := range ref.FindAllStringSubmatch(v, -1) {
			if _, ok := m.UserConfig[r[1]]; !ok {
				t.Errorf("env %s references undeclared user_config.%s", k, r[1])
			}
		}
	}
	for _, k := range []string{"slack_user_token", "extra_workspaces"} {
		if !m.UserConfig[k].Sensitive {
			t.Errorf("user_config.%s holds credentials and must be sensitive", k)
		}
	}
	if !strings.HasSuffix(m.Server.MCPConfig.Overrides["win32"].Command, ".exe") {
		t.Error("win32 override must run the .exe")
	}
	if strings.Join(m.Compatibility.Platforms, ",") != "darwin,win32" {
		t.Errorf("platforms = %v", m.Compatibility.Platforms)
	}
}
