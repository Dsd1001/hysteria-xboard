package xboard

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type runtimePanelClient struct {
	users    *UsersResponse
	reported chan *TrafficBatch
}

func (c *runtimePanelClient) FetchUsers(context.Context, string) (*UsersResponse, error) {
	return c.users, nil
}

func (c *runtimePanelClient) SubmitTraffic(_ context.Context, batch *TrafficBatch) (TrafficAck, error) {
	select {
	case c.reported <- batch:
	default:
	}
	return TrafficAck{BatchID: batch.ID, Status: TrafficBatchAccepted}, nil
}

func TestServiceStartRunsIndependentSyncPersistenceAndReporterLoops(t *testing.T) {
	panel := &runtimePanelClient{
		users:    &UsersResponse{Users: []User{{ID: 1001, UUID: "uuid-a"}}},
		reported: make(chan *TrafficBatch, 1),
	}
	service, err := NewService(ServiceConfig{
		Panel:            panel,
		NodeID:           "401",
		UserCachePath:    filepath.Join(t.TempDir(), "users.json"),
		SpoolPath:        filepath.Join(t.TempDir(), "traffic.db"),
		MaxUserStale:     time.Hour,
		UserPullInterval: 10 * time.Millisecond,
		FlushInterval:    5 * time.Millisecond,
		BatchInterval:    10 * time.Millisecond,
		ReportInterval:   5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := service.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := service.Start(ctx); err == nil {
		t.Fatal("second Start() error = nil, want already-started error")
	}
	if !service.TrafficLogger().LogTraffic("1001", 123, 456) {
		t.Fatal("LogTraffic() = false, want true")
	}

	select {
	case batch := <-panel.reported:
		if got := batch.Traffic["1001"]; got.Upload != 123 || got.Download != 456 {
			t.Fatalf("reported traffic = %#v, want 123/456", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for background traffic report")
	}

	cancel()
	if err := service.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case _, ok := <-service.Errors():
		if ok {
			t.Fatal("Errors channel still open after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Errors channel did not close")
	}
}
