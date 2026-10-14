package handlers

import (
	"strings"
	"testing"

	"wx_channel/internal/config"
)

func TestBuildInjectedScriptsUsesRuntimeAPIPort(t *testing.T) {
	cfg := &config.Config{Port: 2023, APIPort: 2022, SecretToken: "runtime-token"}
	h := NewScriptHandler(
		cfg,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		[]byte("window.__wx_api_client = {};"), nil, "test",
	)

	got := h.buildInjectedScripts("/web/pages/home")
	configMarker := `window.__WX_CHANNEL_API_CONFIG__={"apiPort":2022,"token":"runtime-token"}`
	if !strings.Contains(got, configMarker) {
		t.Fatalf("injected scripts do not contain runtime API config %q", configMarker)
	}
	if strings.Index(got, configMarker) > strings.Index(got, "window.__wx_api_client") {
		t.Fatal("runtime API config must be injected before api_client.js")
	}
}

func TestBuildInjectedScriptsFallsBackToProxyPort(t *testing.T) {
	cfg := &config.Config{Port: 2025}
	h := NewScriptHandler(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "test")

	got := h.getAPIClientConfigScript()
	if !strings.Contains(got, `{"apiPort":2026}`) {
		t.Fatalf("expected derived API port in injected config, got %s", got)
	}
}
