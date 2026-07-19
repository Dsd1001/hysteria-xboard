package xboard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestUserCacheRoundTripUsesPrivatePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "users.json")
	snapshot := UserSnapshot{
		Users: []User{
			{ID: 1001, UUID: "uuid-a", SpeedLimit: 10, DeviceLimit: 2},
		},
		ETag:      `"etag-a"`,
		FetchedAt: time.Unix(1_700_000_000, 123_000_000).UTC(),
	}

	if err := SaveUserCache(path, snapshot); err != nil {
		t.Fatalf("SaveUserCache() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(cache) error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("cache permissions = %o, want 600", got)
	}

	loaded, err := LoadUserCache(path, snapshot.FetchedAt.Add(time.Hour), 6*time.Hour)
	if err != nil {
		t.Fatalf("LoadUserCache() error = %v", err)
	}
	if loaded.ETag != snapshot.ETag || !loaded.FetchedAt.Equal(snapshot.FetchedAt) {
		t.Fatalf("loaded metadata = %#v, want %#v", loaded, snapshot)
	}
	if len(loaded.Users) != 1 || loaded.Users[0] != snapshot.Users[0] {
		t.Fatalf("loaded users = %#v, want %#v", loaded.Users, snapshot.Users)
	}
}

func TestLoadUserCacheRejectsStaleSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	fetchedAt := time.Unix(1_700_000_000, 0).UTC()
	if err := SaveUserCache(path, UserSnapshot{
		Users:     []User{{ID: 1001, UUID: "uuid-a"}},
		FetchedAt: fetchedAt,
	}); err != nil {
		t.Fatalf("SaveUserCache() error = %v", err)
	}

	_, err := LoadUserCache(path, fetchedAt.Add(7*time.Hour), 6*time.Hour)
	if !errors.Is(err, ErrUserCacheStale) {
		t.Fatalf("LoadUserCache() error = %v, want ErrUserCacheStale", err)
	}
}

func TestLoadUserCacheRejectsMalformedOrInvalidData(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "malformed JSON", body: `{`},
		{name: "unknown version", body: `{"version":2,"fetched_at":"2023-11-14T00:00:00Z","users":[]}`},
		{name: "missing fetched time", body: `{"version":1,"users":[]}`},
		{name: "invalid user", body: `{"version":1,"fetched_at":"2023-11-14T00:00:00Z","users":[{"id":0,"uuid":"bad"}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "users.json")
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			if _, err := LoadUserCache(path, time.Unix(1_700_000_000, 0), 0); err == nil {
				t.Fatal("LoadUserCache() error = nil, want validation error")
			}
		})
	}
}

func TestSaveUserCacheRejectsInvalidSnapshotWithoutReplacingGoodFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	good := UserSnapshot{
		Users:     []User{{ID: 1001, UUID: "uuid-a"}},
		ETag:      `"good"`,
		FetchedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	if err := SaveUserCache(path, good); err != nil {
		t.Fatalf("initial SaveUserCache() error = %v", err)
	}

	bad := UserSnapshot{
		Users:     []User{{ID: 1002, UUID: ""}},
		ETag:      `"bad"`,
		FetchedAt: good.FetchedAt,
	}
	if err := SaveUserCache(path, bad); err == nil {
		t.Fatal("invalid SaveUserCache() error = nil, want validation error")
	}
	loaded, err := LoadUserCache(path, good.FetchedAt, 0)
	if err != nil {
		t.Fatalf("LoadUserCache() after failed save error = %v", err)
	}
	if loaded.ETag != `"good"` || len(loaded.Users) != 1 || loaded.Users[0].ID != 1001 {
		t.Fatalf("good cache was replaced: %#v", loaded)
	}
}
