package xboard

import (
	"context"
	"fmt"
)

const (
	TrafficBatchAccepted         = "accepted"
	TrafficBatchAlreadyProcessed = "already_processed"
)

type TrafficAck struct {
	BatchID string
	Status  string
}

type TrafficSender interface {
	SubmitTraffic(ctx context.Context, batch *TrafficBatch) (TrafficAck, error)
}

type TrafficReporter struct {
	spool  *TrafficSpool
	sender TrafficSender
}

func NewTrafficReporter(spool *TrafficSpool, sender TrafficSender) *TrafficReporter {
	return &TrafficReporter{spool: spool, sender: sender}
}

// ReportOldest submits at most one durable batch. It removes the local batch
// only after a matching accepted/already_processed business acknowledgement.
func (r *TrafficReporter) ReportOldest(ctx context.Context) (bool, error) {
	if r == nil || r.spool == nil {
		return false, fmt.Errorf("Xboard traffic spool is required")
	}
	if r.sender == nil {
		return false, fmt.Errorf("Xboard traffic sender is required")
	}
	batch, err := r.spool.OldestBatch()
	if err != nil {
		return false, err
	}
	if batch == nil {
		return false, nil
	}

	ack, err := r.sender.SubmitTraffic(ctx, batch)
	if err != nil {
		return false, err
	}
	if ack.BatchID != batch.ID {
		return false, fmt.Errorf("Xboard traffic ACK batch ID mismatch")
	}
	if ack.Status != TrafficBatchAccepted && ack.Status != TrafficBatchAlreadyProcessed {
		return false, fmt.Errorf("unexpected Xboard traffic ACK status: %q", ack.Status)
	}
	if err := r.spool.AckBatch(batch.ID); err != nil {
		return false, fmt.Errorf("acknowledge local Xboard traffic batch: %w", err)
	}
	return true, nil
}
