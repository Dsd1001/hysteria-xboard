package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apernet/hysteria/core/v2/server"
	"github.com/spf13/viper"
)

func TestXboardConfigDecodesFromServerYAML(t *testing.T) {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(`
auth:
  type: xboard
xboard:
  baseURL: https://panel.example
  tokenFile: /run/secrets/xboard_token
  nodeID: "401"
  apiMode: legacy
  timeout: 8s
  users:
    cacheFile: /var/lib/hysteria/xboard-users.json
    pullInterval: 30s
    maxStale: 6h
  traffic:
    spoolFile: /var/lib/hysteria/xboard-traffic.db
    flushInterval: 1s
    batchInterval: 1m
    reportInterval: 5s
`)); err != nil {
		t.Fatal(err)
	}
	var config serverConfig
	if err := v.Unmarshal(&config); err != nil {
		t.Fatal(err)
	}
	if config.Auth.Type != "xboard" || config.Xboard.NodeID != "401" || config.Xboard.APIMode != "legacy" || config.Xboard.Timeout != 8*time.Second {
		t.Fatalf("decoded Xboard config = %#v", config.Xboard)
	}
	if config.Xboard.Users.PullInterval != 30*time.Second || config.Xboard.Users.MaxStale != 6*time.Hour {
		t.Fatalf("decoded Xboard users config = %#v", config.Xboard.Users)
	}
}

func TestPrepareXboardInitializesLocalAuthenticatorAndTrafficLogger(t *testing.T) {
	const token = "server-token"
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/server/UniProxy/user" {
			t.Fatalf("panel path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("token") != token || r.URL.Query().Get("node_id") != "hy-node-a" || r.URL.Query().Get("node_type") != "hysteria" || r.Header.Get("Authorization") != "" {
			t.Fatalf("panel authentication request is wrong")
		}
		_, _ = w.Write([]byte(`{"users":[{"id":1001,"uuid":"uuid-a"}]}`))
	}))
	defer panel.Close()
	dir := t.TempDir()
	config := serverConfig{
		Auth: serverConfigAuth{Type: "xboard"},
		Xboard: serverConfigXboard{
			BaseURL:   panel.URL,
			Token:     token,
			NodeID:    "hy-node-a",
			AllowHTTP: true,
			Users: serverConfigXboardUsers{
				CacheFile:    filepath.Join(dir, "users.json"),
				MaxStale:     6 * time.Hour,
				PullInterval: time.Minute,
			},
			Traffic: serverConfigXboardTraffic{
				SpoolFile:      filepath.Join(dir, "traffic.db"),
				FlushInterval:  time.Second,
				BatchInterval:  time.Minute,
				ReportInterval: 5 * time.Second,
			},
		},
	}

	result, err := config.prepareXboard(context.Background())
	if err != nil {
		t.Fatalf("prepareXboard() error = %v", err)
	}
	defer config.closeXboard()
	if result.UsingCache || result.SyncError != nil {
		t.Fatalf("prepareXboard() result = %#v, want healthy remote sync", result)
	}

	hyConfig := &server.Config{}
	if err := config.fillAuthenticator(hyConfig); err != nil {
		t.Fatalf("fillAuthenticator() error = %v", err)
	}
	if ok, id := hyConfig.Authenticator.Authenticate(nil, "uuid-a", 0); !ok || id != "1001" {
		t.Fatalf("Xboard authenticator = %v, %q, want true, 1001", ok, id)
	}
	if err := config.fillTrafficLogger(hyConfig); err != nil {
		t.Fatalf("fillTrafficLogger() error = %v", err)
	}
	if hyConfig.TrafficLogger == nil || !hyConfig.TrafficLogger.LogTraffic("1001", 123, 456) {
		t.Fatal("Xboard traffic logger was not installed")
	}
}

func TestPrepareXboardReadsTokenFileWithoutPuttingItInConfig(t *testing.T) {
	const token = "file-token"
	panel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != token || r.Header.Get("Authorization") != "" {
			t.Fatalf("panel request did not use token file value with legacy authentication")
		}
		_, _ = w.Write([]byte(`{"users":[]}`))
	}))
	defer panel.Close()
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := serverConfig{
		Auth: serverConfigAuth{Type: "xboard"},
		Xboard: serverConfigXboard{
			BaseURL:   panel.URL,
			TokenFile: tokenFile,
			NodeID:    "401",
			AllowHTTP: true,
			Users:     serverConfigXboardUsers{CacheFile: filepath.Join(dir, "users.json")},
			Traffic:   serverConfigXboardTraffic{SpoolFile: filepath.Join(dir, "traffic.db")},
		},
	}
	if _, err := config.prepareXboard(context.Background()); err != nil {
		t.Fatalf("prepareXboard(token file) error = %v", err)
	}
	defer config.closeXboard()
	if config.Xboard.Token != "" {
		t.Fatal("token file value was copied into parsed config")
	}
}

func TestPrepareXboardRejectsMissingRequiredConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		config serverConfigXboard
	}{
		{name: "missing base URL", config: serverConfigXboard{Token: "t", NodeID: "1"}},
		{name: "missing token", config: serverConfigXboard{BaseURL: "https://panel.example", NodeID: "1"}},
		{name: "both token sources", config: serverConfigXboard{BaseURL: "https://panel.example", NodeID: "1", Token: "t", TokenFile: "/tmp/t"}},
		{name: "missing node", config: serverConfigXboard{BaseURL: "https://panel.example", Token: "t"}},
		{name: "missing cache", config: serverConfigXboard{BaseURL: "https://panel.example", Token: "t", NodeID: "1", Traffic: serverConfigXboardTraffic{SpoolFile: "/tmp/t.db"}}},
		{name: "missing spool", config: serverConfigXboard{BaseURL: "https://panel.example", Token: "t", NodeID: "1", Users: serverConfigXboardUsers{CacheFile: "/tmp/u.json"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := serverConfig{Auth: serverConfigAuth{Type: "xboard"}, Xboard: test.config}
			if _, err := config.prepareXboard(context.Background()); err == nil {
				t.Fatal("prepareXboard() error = nil, want configuration error")
			}
		})
	}
}

func TestFillXboardAuthenticatorRequiresPreparedRuntime(t *testing.T) {
	config := serverConfig{Auth: serverConfigAuth{Type: "xboard"}}
	if err := config.fillAuthenticator(&server.Config{}); err == nil {
		t.Fatal("fillAuthenticator() error = nil, want uninitialized Xboard error")
	}
}
