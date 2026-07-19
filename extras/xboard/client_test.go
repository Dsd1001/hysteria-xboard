package xboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestFetchUsersDoesNotLeakTokenInRequestError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.NotFoundHandler())
	baseURL := server.URL
	server.Close()

	const token = "secret-token-must-not-leak"
	client, err := NewClient(Config{
		BaseURL:   baseURL,
		Token:     token,
		NodeID:    "1",
		AllowHTTP: true,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.FetchUsers(context.Background(), "")
	if err == nil {
		t.Fatal("FetchUsers() error = nil, want request error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("FetchUsers() error leaked token: %q", err)
	}
}

func TestFetchUsersDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var redirectedRequests atomic.Int32
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedRequests.Add(1)
		_, _ = w.Write([]byte(`{"users":[{"id":1,"uuid":"user-a"}]}`))
	}))
	defer redirectTarget.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL, http.StatusFound)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:   server.URL,
		Token:     "secret-token-must-not-leak",
		NodeID:    "1",
		AllowHTTP: true,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.FetchUsers(context.Background(), "")
	if err == nil {
		t.Fatal("FetchUsers() error = nil, want redirect rejection")
	}
	if got := redirectedRequests.Load(); got != 0 {
		t.Errorf("redirect target received %d requests, want 0", got)
	}
}

func TestFetchUsersRejectsOversizedResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"users":[],"padding":"` + strings.Repeat("x", 4<<20) + `"}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:   server.URL,
		Token:     "token",
		NodeID:    "1",
		AllowHTTP: true,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if _, err := client.FetchUsers(context.Background(), ""); err == nil {
		t.Fatal("FetchUsers() error = nil, want response size error")
	}
}

func TestFetchUsersRejectsNonSuccessStatus(t *testing.T) {
	t.Parallel()

	const token = "secret-token-must-not-leak"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"users":[{"id":1,"uuid":"user-a"}]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:   server.URL,
		Token:     token,
		NodeID:    "1",
		AllowHTTP: true,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	_, err = client.FetchUsers(context.Background(), "")
	if err == nil {
		t.Fatal("FetchUsers() error = nil, want status error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("FetchUsers() error leaked token: %q", err)
	}
}

func TestFetchUsersReturnsNotModifiedForMatchingETag(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != `"previous"` {
			t.Errorf("If-None-Match = %q, want %q", got, `"previous"`)
		}
		w.Header().Set("ETag", `"current"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:   server.URL,
		Token:     "token",
		NodeID:    "1",
		AllowHTTP: true,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	response, err := client.FetchUsers(context.Background(), `"previous"`)
	if err != nil {
		t.Fatalf("FetchUsers() error = %v", err)
	}
	if !response.NotModified {
		t.Error("NotModified = false, want true")
	}
	if response.ETag != `"current"` {
		t.Errorf("ETag = %q, want %q", response.ETag, `"current"`)
	}
	if response.Users != nil {
		t.Errorf("Users = %#v, want nil", response.Users)
	}
}

func TestFetchUsersRejectsInvalidUsersResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		{name: "missing users", body: `{}`},
		{name: "null users", body: `{"users":null}`},
		{name: "non-positive ID", body: `{"users":[{"id":0,"uuid":"user-a"}]}`},
		{name: "empty UUID", body: `{"users":[{"id":1,"uuid":""}]}`},
		{name: "duplicate ID", body: `{"users":[{"id":1,"uuid":"user-a"},{"id":1,"uuid":"user-b"}]}`},
		{name: "duplicate UUID", body: `{"users":[{"id":1,"uuid":"user-a"},{"id":2,"uuid":"user-a"}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			client, err := NewClient(Config{
				BaseURL:   server.URL,
				Token:     "secret-token-must-not-leak",
				NodeID:    "1",
				AllowHTTP: true,
			})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			_, err = client.FetchUsers(context.Background(), "")
			if err == nil {
				t.Fatal("FetchUsers() error = nil, want response validation error")
			}
			if strings.Contains(err.Error(), "secret-token-must-not-leak") {
				t.Errorf("FetchUsers() error leaked token: %q", err)
			}
		})
	}
}

func TestNewClientDoesNotLeakTokenInURLParseError(t *testing.T) {
	t.Parallel()

	const token = "secret-token-must-not-leak"
	_, err := NewClient(Config{
		BaseURL: "https://xboard.example/%zz?token=" + token,
		Token:   token,
		NodeID:  "1",
	})
	if err == nil {
		t.Fatal("NewClient() error = nil, want URL parse error")
	}
	if strings.Contains(err.Error(), token) {
		t.Errorf("NewClient() error leaked token: %q", err)
	}
}

func TestNewClientConfiguresRequestTimeout(t *testing.T) {
	t.Parallel()

	client, err := NewClient(Config{
		BaseURL: "https://xboard.example",
		Token:   "token",
		NodeID:  "1",
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if client.http.Timeout <= 0 {
		t.Errorf("HTTP timeout = %v, want positive timeout", client.http.Timeout)
	}
}

func TestNewClientRejectsUnsupportedScheme(t *testing.T) {
	t.Parallel()

	if _, err := NewClient(Config{
		BaseURL:   "ftp://xboard.example",
		Token:     "token",
		NodeID:    "1",
		AllowHTTP: true,
	}); err == nil {
		t.Fatal("NewClient() error = nil, want unsupported scheme error")
	}
}

func TestNewClientRejectsMissingRequiredInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config Config
	}{
		{
			name:   "empty token",
			config: Config{BaseURL: "https://xboard.example", NodeID: "1"},
		},
		{
			name:   "empty node ID",
			config: Config{BaseURL: "https://xboard.example", Token: "token"},
		},
		{
			name:   "missing scheme and host",
			config: Config{Token: "token", NodeID: "1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewClient(test.config); err == nil {
				t.Fatal("NewClient() error = nil, want invalid configuration error")
			}
		})
	}
}

func TestNewClientRejectsAmbiguousOrCredentialedBaseURL(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{
		"https://user:pass@xboard.example",
		"https://xboard.example/subpath",
		"https://xboard.example?query=value",
		"https://xboard.example#fragment",
	} {
		if _, err := NewClient(Config{BaseURL: baseURL, Token: "token", NodeID: "1"}); err == nil {
			t.Fatalf("NewClient(%q) error = nil, want unambiguous root URL error", baseURL)
		}
	}
}

func TestNewClientAcceptsStringNodeCode(t *testing.T) {
	t.Parallel()

	client, err := NewClient(Config{
		BaseURL: "https://xboard.example",
		Token:   "token",
		NodeID:  "hy-node-a",
	})
	if err != nil {
		t.Fatalf("NewClient(string node code) error = %v", err)
	}
	if client.nodeID != "hy-node-a" {
		t.Fatalf("client.nodeID = %q, want hy-node-a", client.nodeID)
	}
}

func TestNewClientRejectsHTTPUnlessExplicitlyAllowed(t *testing.T) {
	t.Parallel()

	const token = "secret-token-must-not-leak"
	_, err := NewClient(Config{
		BaseURL: "http://xboard.example",
		Token:   token,
		NodeID:  "1",
	})
	if err == nil {
		t.Fatal("NewClient() error = nil, want error for HTTP base URL")
	}
	if got := err.Error(); strings.Contains(got, token) {
		t.Errorf("NewClient() error leaked token: %q", got)
	}

	client, err := NewClient(Config{
		BaseURL:   "http://xboard.example",
		Token:     token,
		NodeID:    "1",
		AllowHTTP: true,
	})
	if err != nil {
		t.Fatalf("NewClient(AllowHTTP: true) error = %v", err)
	}
	if client == nil {
		t.Fatal("NewClient(AllowHTTP: true) = nil, want client")
	}
}

func TestFetchUsersRequestsV2EndpointAndReturnsUsers(t *testing.T) {
	t.Parallel()

	const token = "token +&?%/中文"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want %q", r.Method, http.MethodGet)
		}
		if r.URL.Path != "/api/v2/server/user" {
			t.Errorf("path = %q, want /api/v2/server/user", r.URL.Path)
		}
		if got := r.URL.Query().Get("token"); got != "" {
			t.Errorf("token query = %q, want omitted", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		if got := r.URL.Query().Get("node_id"); got != "42" {
			t.Errorf("node_id query = %q, want 42", got)
		}
		if got := r.Header.Get("If-None-Match"); got != `"previous"` {
			t.Errorf("If-None-Match = %q, want %q", got, `"previous"`)
		}

		w.Header().Set("ETag", `"current"`)
		_, _ = w.Write([]byte(`{"users":[{"id":1001,"uuid":"user-a","speed_limit":0,"device_limit":0,"future_field":true}]}`))
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BaseURL:   server.URL,
		Token:     token,
		NodeID:    "42",
		AllowHTTP: true,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	response, err := client.FetchUsers(context.Background(), `"previous"`)
	if err != nil {
		t.Fatalf("FetchUsers() error = %v", err)
	}
	if response.NotModified {
		t.Fatal("NotModified = true, want false")
	}
	if response.ETag != `"current"` {
		t.Errorf("ETag = %q, want %q", response.ETag, `"current"`)
	}
	if len(response.Users) != 1 {
		t.Fatalf("len(Users) = %d, want 1", len(response.Users))
	}
	if got := response.Users[0]; got.ID != 1001 || got.UUID != "user-a" || got.SpeedLimit != 0 || got.DeviceLimit != 0 {
		t.Errorf("Users[0] = %#v, want valid Xboard user", got)
	}
}
