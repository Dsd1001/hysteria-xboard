package xboard

import (
	"testing"
	"time"
)

func TestTrafficCollectorAccountsActiveUsersAndDrainsDeltas(t *testing.T) {
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "", time.Now())
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	collector := NewTrafficCollector(store)

	if ok := collector.LogTraffic("1001", 123, 456); !ok {
		t.Fatal("LogTraffic(active) = false, want true")
	}
	if ok := collector.LogTraffic("1001", 7, 8); !ok {
		t.Fatal("LogTraffic(active second delta) = false, want true")
	}

	deltas := collector.Drain()
	if got := deltas["1001"]; got.Upload != 130 || got.Download != 464 {
		t.Fatalf("drained traffic = %#v, want upload=130 download=464", got)
	}
	if second := collector.Drain(); len(second) != 0 {
		t.Fatalf("second Drain() = %#v, want empty", second)
	}
}

func TestTrafficCollectorRejectsEveryDeltaForRemovedUser(t *testing.T) {
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "", time.Now())
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	collector := NewTrafficCollector(store)
	if !collector.LogTraffic("1001", 10, 20) {
		t.Fatal("initial LogTraffic() = false, want true")
	}
	_, err = store.Replace([]User{}, "", time.Now())
	if err != nil {
		t.Fatalf("remove user Replace() error = %v", err)
	}

	for i := 0; i < 3; i++ {
		if collector.LogTraffic("1001", 100, 200) {
			t.Fatalf("LogTraffic(removed) call %d = true, want false", i+1)
		}
	}
	got := collector.Drain()["1001"]
	if got.Upload != 10 || got.Download != 20 {
		t.Fatalf("removed-user traffic was accounted: %#v", got)
	}
}

func TestTrafficCollectorMergeRestoresFailedFlush(t *testing.T) {
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "", time.Now())
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	collector := NewTrafficCollector(store)
	collector.LogTraffic("1001", 10, 20)
	drained := collector.Drain()
	collector.LogTraffic("1001", 1, 2)

	collector.Merge(drained)
	got := collector.Drain()["1001"]
	if got.Upload != 11 || got.Download != 22 {
		t.Fatalf("merged traffic = %#v, want upload=11 download=22", got)
	}
}

func TestTrafficCollectorTracksOnlineConnections(t *testing.T) {
	store := NewUserStore()
	_, err := store.Replace([]User{{ID: 1001, UUID: "uuid-a"}}, "", time.Now())
	if err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	collector := NewTrafficCollector(store)

	collector.LogOnlineState("1001", true)
	collector.LogOnlineState("1001", true)
	collector.LogOnlineState("unknown", true)
	collector.LogOnlineState("1001", false)

	online := collector.OnlineSnapshot()
	if online["1001"] != 1 {
		t.Fatalf("online[1001] = %d, want 1", online["1001"])
	}
	if _, ok := online["unknown"]; ok {
		t.Fatal("inactive user was added to online snapshot")
	}

	collector.LogOnlineState("1001", false)
	collector.LogOnlineState("1001", false)
	if online := collector.OnlineSnapshot(); len(online) != 0 {
		t.Fatalf("online after disconnects = %#v, want empty", online)
	}
}
