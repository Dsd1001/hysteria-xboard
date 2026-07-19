package xboard

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientSubmitTrafficUsesV2IdempotentReportContract(t *testing.T) {
	const token = "secret +&?%/中文"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/server/report" {
			t.Errorf("request = %s %s, want POST /api/v2/server/report", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("token") != "" {
			t.Error("token was placed in report URL query")
		}
		if got := r.Header.Get("Idempotency-Key"); got != "hy-0001" {
			t.Errorf("Idempotency-Key = %q, want hy-0001", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("Authorization = %q, want bearer token", got)
		}
		var body struct {
			Token   string               `json:"token"`
			NodeID  string               `json:"node_id"`
			BatchID string               `json:"batch_id"`
			Traffic map[string][2]uint64 `json:"traffic"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Token != "" || body.NodeID != "42" || body.BatchID != "hy-0001" {
			t.Errorf("report body metadata = %#v", body)
		}
		if got := body.Traffic["1001"]; got != [2]uint64{123, 456} {
			t.Errorf("traffic[1001] = %v, want [123 456]", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"batch_id":"hy-0001","status":"accepted"}}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Token: token, NodeID: "42", APIMode: APIModeLedger, AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	batch := &TrafficBatch{
		ID:        "hy-0001",
		NodeID:    "42",
		CreatedAt: time.Now(),
		Traffic: map[string]TrafficDelta{
			"1001": {Upload: 123, Download: 456},
		},
	}

	ack, err := client.SubmitTraffic(context.Background(), batch)
	if err != nil {
		t.Fatalf("SubmitTraffic() error = %v", err)
	}
	if ack.BatchID != batch.ID || ack.Status != TrafficBatchAccepted {
		t.Fatalf("SubmitTraffic() ACK = %#v, want accepted batch", ack)
	}
}

func TestClientSubmitTrafficRejectsInvalidOrAmbiguousResponses(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "non-200", statusCode: http.StatusBadGateway, body: `{"message":"failed"}`},
		{name: "malformed JSON", statusCode: http.StatusOK, body: `{`},
		{name: "missing data", statusCode: http.StatusOK, body: `{"data":true}`},
		{name: "missing batch ID", statusCode: http.StatusOK, body: `{"data":{"status":"accepted"}}`},
		{name: "missing status", statusCode: http.StatusOK, body: `{"data":{"batch_id":"hy-0001"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.statusCode)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			const token = "must-not-leak"
			client, err := NewClient(Config{BaseURL: server.URL, Token: token, NodeID: "42", APIMode: APIModeLedger, AllowHTTP: true})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.SubmitTraffic(context.Background(), &TrafficBatch{
				ID: "hy-0001", NodeID: "42", Traffic: map[string]TrafficDelta{"1": {Upload: 1}},
			})
			if err == nil {
				t.Fatal("SubmitTraffic() error = nil, want response error")
			}
			if strings.Contains(err.Error(), token) {
				t.Fatalf("SubmitTraffic() error leaked token: %v", err)
			}
		})
	}
}

func TestClientSubmitTrafficRejectsWrongNodeOrPHPIntegerOverflow(t *testing.T) {
	client, err := NewClient(Config{BaseURL: "https://xboard.example", Token: "token", NodeID: "42", APIMode: APIModeLedger})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.SubmitTraffic(context.Background(), &TrafficBatch{
		ID: "hy-0001", NodeID: "43", Traffic: map[string]TrafficDelta{"1": {Upload: 1}},
	}); err == nil {
		t.Fatal("wrong-node SubmitTraffic() error = nil")
	}
	if _, err := client.SubmitTraffic(context.Background(), &TrafficBatch{
		ID: "hy-0001", NodeID: "42", Traffic: map[string]TrafficDelta{"1": {Upload: math.MaxUint64}},
	}); err == nil {
		t.Fatal("overflow SubmitTraffic() error = nil")
	}
}

func TestClientSubmitTrafficDefaultsToUnmodifiedUniProxyContract(t *testing.T) {
	const token = "token +&?%/中文"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/server/UniProxy/push" {
			t.Errorf("request = %s %s, want legacy UniProxy push endpoint", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("token") != token || r.URL.Query().Get("node_id") != "42" || r.URL.Query().Get("node_type") != "hysteria" {
			t.Errorf("legacy query = %q", r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		var body map[string][2]uint64
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode legacy request: %v", err)
		}
		if got := body["1001"]; got != [2]uint64{123, 456} {
			t.Errorf("traffic[1001] = %v, want [123 456]", got)
		}
		_, _ = w.Write([]byte(`{"data":true}`))
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Token: token, NodeID: "42", AllowHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	batch := &TrafficBatch{
		ID: "hy-0001", NodeID: "42",
		Traffic: map[string]TrafficDelta{"1001": {Upload: 123, Download: 456}},
	}
	ack, err := client.SubmitTraffic(context.Background(), batch)
	if err != nil {
		t.Fatalf("SubmitTraffic() error = %v", err)
	}
	if ack.BatchID != batch.ID || ack.Status != TrafficBatchAccepted {
		t.Fatalf("SubmitTraffic() ACK = %#v", ack)
	}
}

func TestClientSubmitLegacyTrafficRequiresDataTrue(t *testing.T) {
	for _, body := range []string{`{}`, `{"data":false}`, `{"data":null}`, `{`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			client, err := NewClient(Config{BaseURL: server.URL, Token: "token", NodeID: "42", AllowHTTP: true})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.SubmitTraffic(context.Background(), &TrafficBatch{
				ID: "hy-0001", NodeID: "42", Traffic: map[string]TrafficDelta{"1": {Upload: 1}},
			})
			if err == nil {
				t.Fatal("SubmitTraffic() error = nil, want invalid legacy response error")
			}
		})
	}
}
