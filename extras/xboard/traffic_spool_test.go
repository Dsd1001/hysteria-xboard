package xboard

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestTrafficSpoolPersistsPendingAndUnacknowledgedBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "traffic.db")
	spool, err := OpenTrafficSpool(path, "401")
	if err != nil {
		t.Fatalf("OpenTrafficSpool() error = %v", err)
	}
	if err := spool.AddPending(map[string]TrafficDelta{
		"1001": {Upload: 10, Download: 20},
	}); err != nil {
		t.Fatalf("AddPending(first) error = %v", err)
	}
	if err := spool.AddPending(map[string]TrafficDelta{
		"1001": {Upload: 1, Download: 2},
		"1002": {Upload: 3, Download: 4},
	}); err != nil {
		t.Fatalf("AddPending(second) error = %v", err)
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	batch, err := spool.CreateBatch(now)
	if err != nil {
		t.Fatalf("CreateBatch() error = %v", err)
	}
	if batch == nil || batch.ID == "" || batch.NodeID != "401" || !batch.CreatedAt.Equal(now) {
		t.Fatalf("CreateBatch() = %#v, want identified batch", batch)
	}
	if got := batch.Traffic["1001"]; got.Upload != 11 || got.Download != 22 {
		t.Fatalf("batch traffic[1001] = %#v, want 11/22", got)
	}
	if got := batch.Traffic["1002"]; got.Upload != 3 || got.Download != 4 {
		t.Fatalf("batch traffic[1002] = %#v, want 3/4", got)
	}
	if empty, err := spool.CreateBatch(now.Add(time.Second)); err != nil || empty != nil {
		t.Fatalf("CreateBatch(empty) = %#v, %v, want nil, nil", empty, err)
	}
	if err := spool.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(spool) error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("spool permissions = %o, want 600", got)
	}

	spool, err = OpenTrafficSpool(path, "401")
	if err != nil {
		t.Fatalf("reopen spool error = %v", err)
	}
	defer spool.Close()
	oldest, err := spool.OldestBatch()
	if err != nil {
		t.Fatalf("OldestBatch() error = %v", err)
	}
	if oldest == nil || oldest.ID != batch.ID || oldest.Traffic["1001"] != batch.Traffic["1001"] {
		t.Fatalf("OldestBatch() = %#v, want persisted %#v", oldest, batch)
	}
	if count, err := spool.BatchCount(); err != nil || count != 1 {
		t.Fatalf("BatchCount() = %d, %v, want 1, nil", count, err)
	}
	if err := spool.AckBatch(batch.ID); err != nil {
		t.Fatalf("AckBatch() error = %v", err)
	}
	if oldest, err := spool.OldestBatch(); err != nil || oldest != nil {
		t.Fatalf("OldestBatch(after ack) = %#v, %v, want nil, nil", oldest, err)
	}
}

func TestTrafficSpoolOrdersBatchesAndUsesUniqueIDs(t *testing.T) {
	spool, err := OpenTrafficSpool(filepath.Join(t.TempDir(), "traffic.db"), "node-a")
	if err != nil {
		t.Fatalf("OpenTrafficSpool() error = %v", err)
	}
	defer spool.Close()
	now := time.Unix(1_700_000_000, 0).UTC()

	if err := spool.AddPending(map[string]TrafficDelta{"1": {Upload: 1}}); err != nil {
		t.Fatal(err)
	}
	first, err := spool.CreateBatch(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := spool.AddPending(map[string]TrafficDelta{"1": {Download: 2}}); err != nil {
		t.Fatal(err)
	}
	second, err := spool.CreateBatch(now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatalf("batch IDs are equal: %q", first.ID)
	}
	oldest, err := spool.OldestBatch()
	if err != nil || oldest.ID != first.ID {
		t.Fatalf("OldestBatch() = %#v, %v, want first batch", oldest, err)
	}
}

func TestTrafficSpoolSaturatesCounterOverflow(t *testing.T) {
	spool, err := OpenTrafficSpool(filepath.Join(t.TempDir(), "traffic.db"), "401")
	if err != nil {
		t.Fatalf("OpenTrafficSpool() error = %v", err)
	}
	defer spool.Close()
	if err := spool.AddPending(map[string]TrafficDelta{"1001": {Upload: math.MaxUint64, Download: math.MaxUint64}}); err != nil {
		t.Fatal(err)
	}
	if err := spool.AddPending(map[string]TrafficDelta{"1001": {Upload: 1, Download: 1}}); err != nil {
		t.Fatal(err)
	}
	batch, err := spool.CreateBatch(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := batch.Traffic["1001"]; got.Upload != math.MaxUint64 || got.Download != math.MaxUint64 {
		t.Fatalf("overflowed traffic = %#v, want saturation", got)
	}
}

func TestTrafficSpoolRefusesCorruptPendingValueWithoutDeletingIt(t *testing.T) {
	spool, err := OpenTrafficSpool(filepath.Join(t.TempDir(), "traffic.db"), "401")
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	if err := spool.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(trafficPendingBucket).Put([]byte("1001"), []byte("corrupt"))
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := spool.CreateBatch(time.Now()); err == nil {
		t.Fatal("CreateBatch(corrupt pending) error = nil")
	}
	if err := spool.db.View(func(tx *bolt.Tx) error {
		if got := tx.Bucket(trafficPendingBucket).Get([]byte("1001")); string(got) != "corrupt" {
			t.Fatalf("corrupt pending value = %q, want preserved", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
