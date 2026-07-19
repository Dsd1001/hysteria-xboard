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

// SubmitTraffic sends one durable, idempotent traffic batch to Xboard V2.
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
