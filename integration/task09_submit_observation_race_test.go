package integration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/monitoring"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestG04Task09SubmitObservationRace(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	campaign, err := s.monitor.Plan(ctx, seed.Site.ID, "snapshot-race-cycle")
	if err != nil {
		t.Fatalf("plan campaign: %v", err)
	}
	if err := s.monitor.Start(ctx, campaign.ID, campaign.Version); err != nil {
		t.Fatalf("start campaign: %v", err)
	}
	base := []monitoring.Observation{{Kind: "soil", MeasuredAt: s.clock.Now(), Value: 1, Unit: "index"}, {Kind: "water", MeasuredAt: s.clock.Now().Add(time.Minute), Value: 2, Unit: "index"}, {Kind: "vegetation", MeasuredAt: s.clock.Now().Add(2 * time.Minute), Value: 3, Unit: "index"}}
	if err := s.monitor.AddObservations(ctx, campaign.ID, base); err != nil {
		t.Fatalf("add baseline observations: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION pause_late_observation() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.kind='dust' THEN
		PERFORM pg_sleep(0.3);
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create observation pause function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER pause_late_observation BEFORE INSERT ON observations FOR EACH ROW EXECUTE FUNCTION pause_late_observation()`); err != nil {
		t.Fatalf("create observation pause trigger: %v", err)
	}
	late := monitoring.Observation{Kind: "dust", MeasuredAt: s.clock.Now().Add(3 * time.Minute), Value: 4, Unit: "index"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var submitErr, addErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		addErr = s.monitor.AddObservations(ctx, campaign.ID, []monitoring.Observation{late})
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		time.Sleep(50 * time.Millisecond)
		submitErr = s.monitor.Submit(ctx, campaign.ID, campaign.Version+1)
	}()
	close(start)
	wg.Wait()
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER pause_late_observation ON observations`); err != nil {
		t.Fatalf("remove observation pause trigger: %v", err)
	}
	if addErr != nil && !errors.Is(addErr, platform.ErrConflict) && !strings.Contains(addErr.Error(), "40001") {
		t.Fatalf("late observation failed unexpectedly: %v", addErr)
	}
	if submitErr != nil {
		if !errors.Is(submitErr, platform.ErrConflict) && !strings.Contains(submitErr.Error(), "40001") {
			t.Fatalf("concurrent submit failed unexpectedly: %v", submitErr)
		}
		current, err := s.monitor.Get(ctx, campaign.ID)
		if err != nil {
			t.Fatalf("read campaign after serialization conflict: %v", err)
		}
		if err := s.monitor.Submit(ctx, campaign.ID, current.Version); err != nil {
			t.Fatalf("submit after serialization retry: %v", err)
		}
	}
	var details []byte
	if err := s.store.DB().QueryRow(s.ctx, `SELECT details FROM audit_entries WHERE action='campaign.submitted' AND resource_id=$1`, campaign.ID).Scan(&details); err != nil {
		t.Fatalf("read submitted audit: %v", err)
	}
	var event struct {
		ObservationCount int `json:"observation_count"`
	}
	if err := json.Unmarshal(details, &event); err != nil {
		t.Fatalf("decode submitted audit: %v", err)
	}
	if event.ObservationCount != 4 {
		t.Fatalf("submitted event used stale observation snapshot: %+v", event)
	}
}
