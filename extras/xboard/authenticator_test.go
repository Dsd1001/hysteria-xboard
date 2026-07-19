package xboard

import (
	"net"
	"testing"
	"time"
)

func TestAuthenticatorUsesLocalSnapshotAndReturnsStableUserID(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "", now)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	auth := NewAuthenticator(store, 6*time.Hour)
	auth.now = func() time.Time { return now.Add(time.Hour) }

	ok, id := auth.Authenticate(&net.UDPAddr{}, "uuid-a", 123)
	if !ok || id != "1001" {
		t.Fatalf("Authenticate(valid) = %v, %q, want true, 1001", ok, id)
	}
	ok, id = auth.Authenticate(&net.UDPAddr{}, "unknown", 123)
	if ok || id != "" {
		t.Fatalf("Authenticate(unknown) = %v, %q, want false, empty", ok, id)
	}
}

func TestAuthenticatorDeniesNewConnectionsWhenSnapshotIsStale(t *testing.T) {
	fetchedAt := time.Unix(1_700_000_000, 0)
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "", fetchedAt)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	auth := NewAuthenticator(store, 6*time.Hour)
	auth.now = func() time.Time { return fetchedAt.Add(6*time.Hour + time.Nanosecond) }

	ok, id := auth.Authenticate(&net.UDPAddr{}, "uuid-a", 0)
	if ok || id != "" {
		t.Fatalf("Authenticate(stale) = %v, %q, want false, empty", ok, id)
	}
}

func TestAuthenticatorAllowsConfiguredUnlimitedCacheAge(t *testing.T) {
	fetchedAt := time.Unix(1_700_000_000, 0)
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "", fetchedAt)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	auth := NewAuthenticator(store, 0)
	auth.now = func() time.Time { return fetchedAt.Add(365 * 24 * time.Hour) }

	ok, id := auth.Authenticate(nil, "uuid-a", 0)
	if !ok || id != "1001" {
		t.Fatalf("Authenticate(unlimited cache age) = %v, %q, want true, 1001", ok, id)
	}
}

func TestAuthenticatorWithoutSnapshotFailsClosed(t *testing.T) {
	auth := NewAuthenticator(NewUserStore(), time.Hour)
	ok, id := auth.Authenticate(nil, "uuid-a", 0)
	if ok || id != "" {
		t.Fatalf("Authenticate(empty) = %v, %q, want false, empty", ok, id)
	}
}
