package xboard

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type blockingUsersClient struct {
	calls         atomic.Int32
	firstStarted  chan struct{}
	secondStarted chan struct{}
	release       chan struct{}
}

func (c *blockingUsersClient) FetchUsers(context.Context, string) (*UsersResponse, error) {
	switch c.calls.Add(1) {
	case 1:
		close(c.firstStarted)
	case 2:
		close(c.secondStarted)
	}
	<-c.release
	return &UsersResponse{Users: []User{{ID: 1001, UUID: "uuid-a"}}}, nil
}

func TestUserSyncerSerializesConcurrentSyncs(t *testing.T) {
	client := &blockingUsersClient{
		firstStarted:  make(chan struct{}),
		secondStarted: make(chan struct{}),
		release:       make(chan struct{}),
	}
	syncer := NewUserSyncer(client, NewUserStore(), "", time.Hour)
	done := make(chan error, 2)
	go func() {
		_, err := syncer.Sync(context.Background())
		done <- err
	}()
	<-client.firstStarted
	go func() {
		_, err := syncer.Sync(context.Background())
		done <- err
	}()

	select {
	case <-client.secondStarted:
		t.Fatal("second Sync entered client before first Sync completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(client.release)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

type fakeUsersClient struct {
	response *UsersResponse
	err      error
	etag     string
	calls    int
}

func (c *fakeUsersClient) FetchUsers(_ context.Context, etag string) (*UsersResponse, error) {
	c.calls++
	c.etag = etag
	return c.response, c.err
}

func TestUserSyncerSyncInstallsAndCachesValidatedSnapshot(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cachePath := filepath.Join(t.TempDir(), "users.json")
	client := &fakeUsersClient{response: &UsersResponse{
		Users: []User{{ID: 1001, UUID: "uuid-a"}},
		ETag:  `"etag-a"`,
	}}
	store := NewUserStore()
	syncer := NewUserSyncer(client, store, cachePath, 6*time.Hour)
	syncer.now = func() time.Time { return now }

	result, err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !result.Updated || result.NotModified {
		t.Fatalf("Sync() result = %#v, want updated", result)
	}
	if id, ok := store.Lookup("uuid-a"); !ok || id != "1001" {
		t.Fatalf("store lookup = %q, %v, want 1001, true", id, ok)
	}
	cached, err := LoadUserCache(cachePath, now, 6*time.Hour)
	if err != nil {
		t.Fatalf("LoadUserCache() error = %v", err)
	}
	if cached.ETag != `"etag-a"` || !cached.FetchedAt.Equal(now) {
		t.Fatalf("cached snapshot = %#v, want current response metadata", cached)
	}
}

func TestUserSyncerFailurePreservesLastKnownGood(t *testing.T) {
	store := NewUserStore()
	fetchedAt := time.Unix(1_700_000_000, 0).UTC()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, `"good"`, fetchedAt)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	client := &fakeUsersClient{err: errors.New("panel unavailable")}
	syncer := NewUserSyncer(client, store, "", 6*time.Hour)

	if _, err := syncer.Sync(context.Background()); err == nil {
		t.Fatal("Sync() error = nil, want panel error")
	}
	if client.etag != `"good"` {
		t.Fatalf("FetchUsers() ETag = %q, want last-known-good ETag", client.etag)
	}
	if id, ok := store.Lookup("uuid-a"); !ok || id != "1001" {
		t.Fatalf("last-known-good changed after failure: %q, %v", id, ok)
	}
	if got := store.Snapshot().FetchedAt; !got.Equal(fetchedAt) {
		t.Fatalf("FetchedAt changed after failed sync: %v", got)
	}
}

func TestUserSyncerNotModifiedRefreshesSnapshotAge(t *testing.T) {
	oldTime := time.Unix(1_700_000_000, 0).UTC()
	now := oldTime.Add(5 * time.Hour)
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, `"old"`, oldTime)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	client := &fakeUsersClient{response: &UsersResponse{NotModified: true, ETag: `"new"`}}
	syncer := NewUserSyncer(client, store, "", 6*time.Hour)
	syncer.now = func() time.Time { return now }

	result, err := syncer.Sync(context.Background())
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if !result.NotModified || result.Updated {
		t.Fatalf("Sync() result = %#v, want not-modified", result)
	}
	snapshot := store.Snapshot()
	if !snapshot.FetchedAt.Equal(now) || snapshot.ETag != `"new"` {
		t.Fatalf("refreshed snapshot = %#v, want new time and ETag", snapshot)
	}
	if id, ok := store.Lookup("uuid-a"); !ok || id != "1001" {
		t.Fatalf("304 cleared users: %q, %v", id, ok)
	}
}

func TestUserSyncerRejectsNotModifiedWithoutLocalSnapshot(t *testing.T) {
	client := &fakeUsersClient{response: &UsersResponse{NotModified: true}}
	syncer := NewUserSyncer(client, NewUserStore(), "", time.Hour)

	if _, err := syncer.Sync(context.Background()); err == nil {
		t.Fatal("Sync() error = nil, want 304-without-snapshot error")
	}
}

func TestUserSyncerLoadsValidCacheForStartupFallback(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cachePath := filepath.Join(t.TempDir(), "users.json")
	if err := SaveUserCache(cachePath, UserSnapshot{
		Users:     []User{{ID: 1001, UUID: "uuid-a"}},
		ETag:      `"cached"`,
		FetchedAt: now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveUserCache() error = %v", err)
	}
	store := NewUserStore()
	syncer := NewUserSyncer(&fakeUsersClient{}, store, cachePath, 6*time.Hour)
	syncer.now = func() time.Time { return now }

	if err := syncer.LoadCache(); err != nil {
		t.Fatalf("LoadCache() error = %v", err)
	}
	if id, ok := store.Lookup("uuid-a"); !ok || id != "1001" {
		t.Fatalf("cache lookup = %q, %v, want 1001, true", id, ok)
	}
}
