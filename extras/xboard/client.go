// Package xboard implements Xboard server user and traffic APIs.
package xboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout      = 10 * time.Second
	maxResponseBodySize = 4 << 20
	APIModeLegacy       = "legacy"
	APIModeLedger       = "ledger"
)

// Config configures a Client.
type Config struct {
	BaseURL   string
	Token     string
	NodeID    string
	APIMode   string
	AllowHTTP bool
	Timeout   time.Duration
}

// User is an Xboard user returned by the V2 API.
type User struct {
	ID          int64  `json:"id"`
	UUID        string `json:"uuid"`
	SpeedLimit  int64  `json:"speed_limit"`
	DeviceLimit int64  `json:"device_limit"`
}

// UsersResponse is the result of fetching users.
type UsersResponse struct {
	Users       []User
	ETag        string
	NotModified bool
}

// Client fetches users and submits traffic to an Xboard server API.
type Client struct {
	baseURL *url.URL
	token   string
	nodeID  string
	apiMode string
	http    *http.Client
}

// NewClient constructs a client for the supplied Xboard server API.
func NewClient(config Config) (*Client, error) {
	if config.Token == "" {
		return nil, fmt.Errorf("Xboard token is required")
	}
	if config.NodeID == "" || strings.TrimSpace(config.NodeID) != config.NodeID {
		return nil, fmt.Errorf("Xboard node ID is required and must not contain surrounding whitespace")
	}
	apiMode := strings.ToLower(strings.TrimSpace(config.APIMode))
	if apiMode == "" {
		apiMode = APIModeLegacy
	}
	if apiMode != APIModeLegacy && apiMode != APIModeLedger {
		return nil, fmt.Errorf("unsupported Xboard API mode")
	}

	baseURL, err := url.Parse(config.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Xboard base URL")
	}
	if baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("Xboard base URL must include a scheme and host")
	}
	if baseURL.User != nil || baseURL.Opaque != "" || baseURL.RawQuery != "" || baseURL.Fragment != "" || (baseURL.Path != "" && baseURL.Path != "/") {
		return nil, fmt.Errorf("Xboard base URL must be an uncredentialed origin URL")
	}
	if baseURL.Scheme != "https" && !(baseURL.Scheme == "http" && config.AllowHTTP) {
		return nil, fmt.Errorf("Xboard base URL must use HTTPS")
	}

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	return &Client{
		baseURL: baseURL,
		token:   config.Token,
		nodeID:  config.NodeID,
		apiMode: apiMode,
		http: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// FetchUsers fetches the current Xboard users. It sends etag in If-None-Match
// when etag is non-empty.
func (c *Client) FetchUsers(ctx context.Context, etag string) (*UsersResponse, error) {
	path := "/api/v1/server/UniProxy/user"
	if c.apiMode == APIModeLedger {
		path = "/api/v2/server/user"
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: path})
	query := url.Values{}
	query.Set("node_id", c.nodeID)
	if c.apiMode == APIModeLegacy {
		// Unmodified Xboard requires these query parameters. HTTPS remains
		// mandatory by default, and errors never include the request URL.
		query.Set("token", c.token)
		query.Set("node_type", "hysteria")
	}
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Xboard users request")
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if c.apiMode == APIModeLedger {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("Xboard users request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return &UsersResponse{ETag: resp.Header.Get("ETag"), NotModified: true}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected Xboard API status: %d", resp.StatusCode)
	}

	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodySize+1))
	if err != nil {
		return nil, fmt.Errorf("read Xboard users response")
	}
	if len(responseBody) > maxResponseBodySize {
		return nil, fmt.Errorf("Xboard users response exceeds maximum size")
	}

	var body struct {
		Users json.RawMessage `json:"users"`
	}
	if err := json.Unmarshal(responseBody, &body); err != nil {
		return nil, err
	}
	if len(body.Users) == 0 || string(body.Users) == "null" {
		return nil, fmt.Errorf("invalid Xboard users response")
	}

	var users []User
	if err := json.Unmarshal(body.Users, &users); err != nil {
		return nil, err
	}
	if err := validateUsers(users); err != nil {
		return nil, err
	}
	return &UsersResponse{Users: users, ETag: resp.Header.Get("ETag")}, nil
}

func validateUsers(users []User) error {
	ids := make(map[int64]struct{}, len(users))
	uuids := make(map[string]struct{}, len(users))
	for _, user := range users {
		if user.ID <= 0 || strings.TrimSpace(user.UUID) == "" {
			return fmt.Errorf("invalid Xboard user")
		}
		if _, ok := ids[user.ID]; ok {
			return fmt.Errorf("duplicate Xboard user ID")
		}
		if _, ok := uuids[user.UUID]; ok {
			return fmt.Errorf("duplicate Xboard user UUID")
		}
		ids[user.ID] = struct{}{}
		uuids[user.UUID] = struct{}{}
	}
	return nil
}
