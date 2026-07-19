package xboard

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fakeTrafficSender struct {
	ack   TrafficAck
	err   error
	batch *TrafficBatch
	calls int
}

func (s *fakeTrafficSender) SubmitTraffic(_ context.Context, batch *TrafficBatch) (TrafficAck, error) {
	s.calls++
	s.batch = batch
	return s.ack, s.err
}

func TestTrafficReporterAcknowledgesAcceptedBatch(t *testing.T) {
	spool := newTestTrafficSpoolWithBatch(t)
	defer spool.Close()
	batch, err := spool.OldestBatch()
	if err != nil {
		t.Fatal(err)
	}
	sender := &fakeTrafficSender{ack: TrafficAck{BatchID: batch.ID, Status: TrafficBatchAccepted}}
	reporter := NewTrafficReporter(spool, sender)

	reported, err := reporter.ReportOldest(context.Background())
	if err != nil || !reported {
		t.Fatalf("ReportOldest() = %v, %v, want true, nil", reported, err)
	}
	if sender.batch == nil || sender.batch.ID != batch.ID {
		t.Fatalf("sender batch = %#v, want %q", sender.batch, batch.ID)
	}
	if oldest, err := spool.OldestBatch(); err != nil || oldest != nil {
		t.Fatalf("OldestBatch() after ACK = %#v, %v, want nil, nil", oldest, err)
	}
}

func TestTrafficReporterAcceptsAlreadyProcessedAck(t *testing.T) {
	spool := newTestTrafficSpoolWithBatch(t)
	defer spool.Close()
	batch, _ := spool.OldestBatch()
	sender := &fakeTrafficSender{ack: TrafficAck{BatchID: batch.ID, Status: TrafficBatchAlreadyProcessed}}

	reported, err := NewTrafficReporter(spool, sender).ReportOldest(context.Background())
	if err != nil || !reported {
		t.Fatalf("ReportOldest() = %v, %v, want true, nil", reported, err)
	}
	if count, _ := spool.BatchCount(); count != 0 {
		t.Fatalf("batch count after already_processed = %d, want 0", count)
	}
}

func TestTrafficReporterKeepsBatchOnAmbiguousOrInvalidResponse(t *testing.T) {
	tests := []struct {
		name   string
		sender *fakeTrafficSender
	}{
		{name: "network error", sender: &fakeTrafficSender{err: errors.New("timeout")}},
		{name: "mismatched batch", sender: &fakeTrafficSender{ack: TrafficAck{BatchID: "other", Status: TrafficBatchAccepted}}},
		{name: "unknown status", sender: &fakeTrafficSender{ack: TrafficAck{Status: "unknown"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spool := newTestTrafficSpoolWithBatch(t)
			defer spool.Close()
			batch, _ := spool.OldestBatch()
			if test.name != "mismatched batch" {
				test.sender.ack.BatchID = batch.ID
			}

			reported, err := NewTrafficReporter(spool, test.sender).ReportOldest(context.Background())
			if err == nil || reported {
				t.Fatalf("ReportOldest() = %v, %v, want false, error", reported, err)
			}
			if count, _ := spool.BatchCount(); count != 1 {
				t.Fatalf("batch count after failure = %d, want 1", count)
			}
		})
	}
}

func TestTrafficReporterDoesNothingWhenQueueIsEmpty(t *testing.T) {
	spool, err := OpenTrafficSpool(filepath.Join(t.TempDir(), "traffic.db"), "401")
	if err != nil {
		t.Fatal(err)
	}
	defer spool.Close()
	sender := &fakeTrafficSender{}

	reported, err := NewTrafficReporter(spool, sender).ReportOldest(context.Background())
	if err != nil || reported {
		t.Fatalf("ReportOldest(empty) = %v, %v, want false, nil", reported, err)
	}
	if sender.calls != 0 {
		t.Fatalf("sender calls = %d, want 0", sender.calls)
	}
}

func newTestTrafficSpoolWithBatch(t *testing.T) *TrafficSpool {
	t.Helper()
	spool, err := OpenTrafficSpool(filepath.Join(t.TempDir(), "traffic.db"), "401")
	if err != nil {
		t.Fatal(err)
	}
	if err := spool.AddPending(map[string]TrafficDelta{"1001": {Upload: 10, Download: 20}}); err != nil {
		spool.Close()
		t.Fatal(err)
	}
	if _, err := spool.CreateBatch(time.Unix(1_700_000_000, 0)); err != nil {
		spool.Close()
		t.Fatal(err)
	}
	return spool
}
