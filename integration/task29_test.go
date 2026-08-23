package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/monitoring"
)

func TestG04Task29MonitoringSubmitCancellation(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	fieldContext := s.as(s.field)
	campaign, err := s.monitor.Plan(fieldContext, seed.Site.ID, "2026-Q3")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.monitor.Start(fieldContext, campaign.ID, campaign.Version); err != nil {
		t.Fatal(err)
	}
	observations := []monitoring.Observation{
		{Kind: "soil", MeasuredAt: s.clock.Now(), Value: 1, Unit: "index"},
		{Kind: "vegetation", MeasuredAt: s.clock.Now(), Value: 2, Unit: "index"},
		{Kind: "dust", MeasuredAt: s.clock.Now(), Value: 3, Unit: "index"},
	}
	if err := s.monitor.AddObservations(fieldContext, campaign.ID, observations); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(fieldContext)
	cancel()
	if err := s.monitor.Submit(cancelled, campaign.ID, campaign.Version+1); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected submission cancellation, got %v", err)
	}

	found, err := s.monitor.Get(fieldContext, campaign.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "collecting" || found.Version != campaign.Version+1 {
		t.Fatalf("cancelled submission changed campaign: %+v", found)
	}
	var events int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='campaign.submitted' AND aggregate_id=$1`, campaign.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("cancelled submission emitted event: %d", events)
	}

	if err := s.monitor.Submit(fieldContext, campaign.ID, campaign.Version+1); err != nil {
		t.Fatalf("submit after cancellation: %v", err)
	}
	completed, err := s.monitor.Get(fieldContext, campaign.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "submitted" || completed.Version != campaign.Version+2 {
		t.Fatalf("unexpected submitted campaign: %+v", completed)
	}
}
