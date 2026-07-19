package xboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
)

const maxTrafficResponseBodySize = 1 << 20

type trafficReportRequest struct {
	NodeID  string               `json:"node_id"`
	BatchID string               `json:"batch_id"`
	Traffic map[string][2]uint64 `json:"traffic"`
}

// SubmitTraffic sends one durable local traffic batch to Xboard. Ledger mode
// uses the idempotent V2 extension; legacy mode uses the unmodified UniProxy
// push API and synthesizes a local ACK only after {"data":true}.
func (c *Client) SubmitTraffic(ctx context.Context, batch *TrafficBatch) (TrafficAck, error) {
	if batch == nil || batch.ID == "" {
		return TrafficAck{}, fmt.Errorf("Xboard traffic batch ID is required")
	}
	if batch.NodeID != c.nodeID {
		return TrafficAck{}, fmt.Errorf("Xboard traffic batch belongs to a different node")
	}

	traffic := make(map[string][2]uint64, len(batch.Traffic))
	for id, delta := range batch.Traffic {
		if id == "" {
			return TrafficAck{}, fmt.Errorf("empty Xboard user ID in traffic batch")
		}
		if delta.Upload > math.MaxInt64 || delta.Download > math.MaxInt64 {
			return TrafficAck{}, fmt.Errorf("Xboard traffic delta exceeds PHP integer range")
		}
		traffic[id] = [2]uint64{delta.Upload, delta.Download}
	}
	if c.apiMode == APIModeLegacy {
		return c.submitLegacyTraffic(ctx, batch, traffic)
	}
	return c.submitLedgerTraffic(ctx, batch, traffic)
}

func (c *Client) submitLedgerTraffic(ctx context.Context, batch *TrafficBatch, traffic map[string][2]uint64) (TrafficAck, error) {
	payload, err := json.Marshal(trafficReportRequest{
		NodeID:  c.nodeID,
		BatchID: batch.ID,
		Traffic: traffic,
	})
	if err != nil {
		return TrafficAck{}, fmt.Errorf("encode Xboard traffic report")
	}

	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "/api/v2/server/report"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return TrafficAck{}, fmt.Errorf("create Xboard traffic report request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Idempotency-Key", batch.ID)

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return TrafficAck{}, ctx.Err()
		}
		return TrafficAck{}, fmt.Errorf("Xboard traffic report request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TrafficAck{}, fmt.Errorf("unexpected Xboard traffic API status: %d", resp.StatusCode)
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxTrafficResponseBodySize+1))
	if err != nil {
		return TrafficAck{}, fmt.Errorf("read Xboard traffic report response")
	}
	if len(responseBody) > maxTrafficResponseBodySize {
		return TrafficAck{}, fmt.Errorf("Xboard traffic report response exceeds maximum size")
	}
	var wrapper struct {
		Data *struct {
			BatchID string `json:"batch_id"`
			Status  string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &wrapper); err != nil {
		return TrafficAck{}, fmt.Errorf("decode Xboard traffic report response")
	}
	if wrapper.Data == nil || wrapper.Data.BatchID == "" || wrapper.Data.Status == "" {
		return TrafficAck{}, fmt.Errorf("invalid Xboard traffic report response")
	}
	return TrafficAck{BatchID: wrapper.Data.BatchID, Status: wrapper.Data.Status}, nil
}

func (c *Client) submitLegacyTraffic(ctx context.Context, batch *TrafficBatch, traffic map[string][2]uint64) (TrafficAck, error) {
	payload, err := json.Marshal(traffic)
	if err != nil {
		return TrafficAck{}, fmt.Errorf("encode Xboard legacy traffic report")
	}
	endpoint := c.baseURL.ResolveReference(&url.URL{Path: "/api/v1/server/UniProxy/push"})
	query := url.Values{}
	query.Set("token", c.token)
	query.Set("node_id", c.nodeID)
	query.Set("node_type", "hysteria")
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return TrafficAck{}, fmt.Errorf("create Xboard legacy traffic report request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return TrafficAck{}, ctx.Err()
		}
		return TrafficAck{}, fmt.Errorf("Xboard legacy traffic report request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return TrafficAck{}, fmt.Errorf("unexpected Xboard legacy traffic API status: %d", resp.StatusCode)
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxTrafficResponseBodySize+1))
	if err != nil {
		return TrafficAck{}, fmt.Errorf("read Xboard legacy traffic report response")
	}
	if len(responseBody) > maxTrafficResponseBodySize {
		return TrafficAck{}, fmt.Errorf("Xboard legacy traffic report response exceeds maximum size")
	}
	var wrapper struct {
		Data *bool `json:"data"`
	}
	if err := json.Unmarshal(responseBody, &wrapper); err != nil {
		return TrafficAck{}, fmt.Errorf("decode Xboard legacy traffic report response")
	}
	if wrapper.Data == nil || !*wrapper.Data {
		return TrafficAck{}, fmt.Errorf("invalid Xboard legacy traffic report response")
	}
	return TrafficAck{BatchID: batch.ID, Status: TrafficBatchAccepted}, nil
}
