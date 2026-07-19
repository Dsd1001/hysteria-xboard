package xboard

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fakePanelClient struct {
	usersResponse *UsersResponse
	usersErr      error
	trafficAck    TrafficAck
	trafficErr    error
	trafficBatch  *TrafficBatch
}

func (c *fakePanelClient) FetchUsers(context.Context, string) (*UsersResponse, error) {
	return c.usersResponse, c.usersErr
}

func (c *fakePanelClient) SubmitTraffic(_ context.Context, batch *TrafficBatch) (TrafficAck, error) {
	c.trafficBatch = batch
	if c.trafficAck.BatchID == "" && batch != nil {
		c.trafficAck.BatchID = batch.ID
	}
	return c.trafficAck, c.trafficErr
}

func TestServiceInitializeUsesRemoteSnapshot(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	panel := &fakePanelClient{usersResponse: &UsersResponse{
		Users: []User{{ID: 1001, UUID: "uuid-a"}},
		ETag:  `"remote"`,
	}}
	service := newTestService(t, panel, now)
	defer service.Close()

	result, err := service.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if result.UsingCache || result.SyncError != nil {
		t.Fatalf("Initialize() result = %#v, want healthy remote snapshot", result)
	}
	ok, id := service.Authenticator().Authenticate(nil, "uuid-a", 0)
	if !ok || id != "1001" {
		t.Fatalf("Authenticator() = %v, %q, want true, 1001", ok, id)
	}
}

func TestServiceInitializeFallsBackToValidCache(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cachePath := filepath.Join(t.TempDir(), "users.json")
	if err := SaveUserCache(cachePath, UserSnapshot{
		Users:     []User{{ID: 1001, UUID: "uuid-a"}},
		FetchedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	panelErr := errors.New("panel unavailable")
	panel := &fakePanelClient{usersErr: panelErr}
	service, err := NewService(ServiceConfig{
		Panel:         panel,
		NodeID:        "401",
		UserCachePath: cachePath,
		SpoolPath:     filepath.Join(t.TempDir(), "traffic.db"),
		MaxUserStale:  6 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	service.syncer.now = service.now
	service.authenticator.now = service.now
	defer service.Close()

	result, err := service.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize(cache fallback) error = %v", err)
	}
	if !result.UsingCache || !errors.Is(result.SyncError, panelErr) {
		t.Fatalf("Initialize() result = %#v, want degraded cache fallback", result)
	}
	if ok, id := service.Authenticator().Authenticate(nil, "uuid-a", 0); !ok || id != "1001" {
		t.Fatalf("cached authenticator = %v, %q, want true, 1001", ok, id)
	}
}

func TestServiceInitializeFailsWithoutRemoteOrCache(t *testing.T) {
	panel := &fakePanelClient{usersErr: errors.New("panel unavailable")}
	service, err := NewService(ServiceConfig{
		Panel:         panel,
		NodeID:        "401",
		UserCachePath: filepath.Join(t.TempDir(), "missing.json"),
		SpoolPath:     filepath.Join(t.TempDir(), "traffic.db"),
		MaxUserStale:  time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if _, err := service.Initialize(context.Background()); err == nil {
		t.Fatal("Initialize() error = nil, want startup failure")
	}
}

func TestServiceFlushesCollectorToDurableBatchAndReports(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	panel := &fakePanelClient{
		usersResponse: &UsersResponse{Users: []User{{ID: 1001, UUID: "uuid-a"}}},
		trafficAck:    TrafficAck{Status: TrafficBatchAccepted},
	}
	service := newTestService(t, panel, now)
	defer service.Close()
	if _, err := service.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !service.TrafficLogger().LogTraffic("1001", 123, 456) {
		t.Fatal("LogTraffic() = false, want true")
	}
	if err := service.FlushTraffic(); err != nil {
		t.Fatalf("FlushTraffic() error = %v", err)
	}
	batch, err := service.CreateTrafficBatch(now)
	if err != nil || batch == nil {
		t.Fatalf("CreateTrafficBatch() = %#v, %v", batch, err)
	}
	reported, err := service.ReportOldest(context.Background())
	if err != nil || !reported {
		t.Fatalf("ReportOldest() = %v, %v, want true, nil", reported, err)
	}
	if panel.trafficBatch == nil || panel.trafficBatch.Traffic["1001"] != (TrafficDelta{Upload: 123, Download: 456}) {
		t.Fatalf("reported batch = %#v", panel.trafficBatch)
	}
}

func newTestService(t *testing.T, panel PanelClient, now time.Time) *Service {
	t.Helper()
	service, err := NewService(ServiceConfig{
		Panel:         panel,
		NodeID:        "401",
		UserCachePath: filepath.Join(t.TempDir(), "users.json"),
		SpoolPath:     filepath.Join(t.TempDir(), "traffic.db"),
		MaxUserStale:  6 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	service.syncer.now = service.now
	service.authenticator.now = service.now
	return service
}
