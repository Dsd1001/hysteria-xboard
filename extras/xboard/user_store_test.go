package xboard

import (
	"sync"
	"testing"
	"time"
)

func TestUserStoreLookupAuthReturnsMetadataFromSameSnapshot(t *testing.T) {
	store := NewUserStore()
	fetchedAt := time.Unix(1_700_000_000, 0)
	if _, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "etag-a", fetchedAt); err != nil {
		t.Fatal(err)
	}

	id, gotFetchedAt, ok := store.LookupAuth("uuid-a")
	if !ok || id != "1001" || !gotFetchedAt.Equal(fetchedAt) {
		t.Fatalf("LookupAuth() = %q, %v, %v", id, gotFetchedAt, ok)
	}
}

func TestUserStoreReplacesSnapshotAndAuthenticatesLocally(t *testing.T) {
	store := NewUserStore()
	if _, ok := store.Lookup("missing"); ok {
		t.Fatal("empty store authenticated unknown UUID")
	}

	fetchedAt := time.Unix(1_700_000_000, 0)
	revoked, err := store.Replace([]User{
		{ID: 1001, UUID: "uuid-a"},
		{ID: 1002, UUID: "uuid-b"},
	}, `"etag-a"`, fetchedAt)
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	if len(revoked) != 0 {
		t.Fatalf("Replace() revoked = %v, want empty", revoked)
	}

	if id, ok := store.Lookup("uuid-a"); !ok || id != "1001" {
		t.Fatalf("Lookup(uuid-a) = %q, %v, want 1001, true", id, ok)
	}
	if !store.IsActiveID("1002") {
		t.Fatal("IsActiveID(1002) = false, want true")
	}

	snapshot := store.Snapshot()
	if snapshot.ETag != `"etag-a"` || !snapshot.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("Snapshot metadata = %#v, want supplied ETag and timestamp", snapshot)
	}
	if len(snapshot.Users) != 2 {
		t.Fatalf("len(Snapshot.Users) = %d, want 2", len(snapshot.Users))
	}

	// Returned snapshots must not mutate the live authentication index.
	snapshot.Users[0].UUID = "mutated"
	if id, ok := store.Lookup("uuid-a"); !ok || id != "1001" {
		t.Fatalf("snapshot mutation changed live lookup: %q, %v", id, ok)
	}
}

func TestUserStoreReplaceReportsEveryRemovedUserID(t *testing.T) {
	store := NewUserStore()
	_, err := store.Replace([]User{
		{ID: 1001, UUID: "uuid-a"},
		{ID: 1002, UUID: "uuid-b"},
		{ID: 1003, UUID: "uuid-c"},
	}, "", time.Now())
	if err != nil {
		t.Fatalf("initial Replace() error = %v", err)
	}

	revoked, err := store.Replace([]User{
		{ID: 1002, UUID: "uuid-b-new"},
	}, "", time.Now())
	if err != nil {
		t.Fatalf("second Replace() error = %v", err)
	}
	if got, want := revoked, []string{"1001", "1003"}; !equalStrings(got, want) {
		t.Fatalf("revoked = %v, want %v", got, want)
	}
	if _, ok := store.Lookup("uuid-a"); ok {
		t.Fatal("removed UUID still authenticates")
	}
	if _, ok := store.Lookup("uuid-b"); ok {
		t.Fatal("replaced UUID still authenticates")
	}
	if id, ok := store.Lookup("uuid-b-new"); !ok || id != "1002" {
		t.Fatalf("new UUID lookup = %q, %v, want 1002, true", id, ok)
	}
	if store.IsActiveID("1001") || !store.IsActiveID("1002") {
		t.Fatal("active ID index was not atomically replaced")
	}
}

func TestUserStoreRejectsInvalidReplacementWithoutChangingLastKnownGood(t *testing.T) {
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, `"good"`, time.Now())
	if err != nil {
		t.Fatalf("initial Replace() error = %v", err)
	}

	_, err = store.Replace([]User{{ID: 1002, UUID: ""}}, `"bad"`, time.Now())
	if err == nil {
		t.Fatal("invalid Replace() error = nil, want validation error")
	}
	if id, ok := store.Lookup("uuid-a"); !ok || id != "1001" {
		t.Fatalf("last-known-good changed after invalid replacement: %q, %v", id, ok)
	}
	if got := store.Snapshot().ETag; got != `"good"` {
		t.Fatalf("ETag after invalid replacement = %q, want good ETag", got)
	}
}

func TestUserStoreConcurrentLookupAndReplace(t *testing.T) {
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "", time.Now())
	if err != nil {
		t.Fatalf("initial Replace() error = %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1_000; j++ {
				store.Lookup("uuid-a")
				store.IsActiveID("1001")
				_ = store.Snapshot()
			}
		}()
	}
	for i := 0; i < 100; i++ {
		_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "", time.Now())
		if err != nil {
			t.Fatalf("Replace() error = %v", err)
		}
	}
	wg.Wait()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
