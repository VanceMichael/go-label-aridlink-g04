package integration_test

import (
	"errors"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestG04Task28OutboxExpiredOwnerAck(t *testing.T) {
	s := newSuite(t)
	eventID, err := s.outbox.Enqueue(s.ctx, s.store.DB(), "training.completed", "cohort-28", map[string]string{"cohort_id": "cohort-28"})
	if err != nil {
		t.Fatalf("enqueue event: %v", err)
	}
	claimed, err := s.outbox.Claim(s.ctx, "worker-old", time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != eventID {
		t.Fatalf("claim event: events=%+v err=%v", claimed, err)
	}
	s.clock.Advance(time.Minute)
	if err := s.outbox.Acknowledge(s.ctx, eventID, "worker-old"); !errors.Is(err, platform.ErrLeaseLost) {
		t.Errorf("expired owner acknowledgement was accepted: %v", err)
	}
	var status string
	if err := s.store.DB().QueryRow(s.ctx, `SELECT status FROM outbox_events WHERE id=$1`, eventID).Scan(&status); err != nil {
		t.Fatalf("inspect expired event: %v", err)
	}
	if status != "leased" {
		t.Errorf("expired event disappeared from recovery: %s", status)
	}
	claimed, err = s.outbox.Claim(s.ctx, "worker-new", time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != eventID {
		t.Fatalf("reclaim event: events=%+v err=%v", claimed, err)
	}
	if err := s.outbox.Acknowledge(s.ctx, eventID, "worker-new"); err != nil {
		t.Errorf("current owner acknowledgement: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT status FROM outbox_events WHERE id=$1`, eventID).Scan(&status); err != nil {
		t.Fatalf("inspect delivered event: %v", err)
	}
	if status != "delivered" {
		t.Errorf("reclaimed event did not deliver: %s", status)
	}
}
