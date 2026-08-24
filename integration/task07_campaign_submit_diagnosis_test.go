package integration_test

import (
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/monitoring"
)

func TestG04Task07CampaignSubmitAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	campaign, err := s.monitor.Plan(ctx, seed.Site.ID, "2026-Q4 diagnosis")
	if err != nil {
		t.Fatalf("plan campaign: %v", err)
	}
	if err := s.monitor.Start(ctx, campaign.ID, campaign.Version); err != nil {
		t.Fatalf("start campaign: %v", err)
	}
	observations := []monitoring.Observation{
		{Kind: "soil", MeasuredAt: s.clock.Now(), Value: 0.42, Unit: "fraction"},
		{Kind: "vegetation", MeasuredAt: s.clock.Now().Add(time.Minute), Value: 0.31, Unit: "fraction"},
		{Kind: "water", MeasuredAt: s.clock.Now().Add(2 * time.Minute), Value: 12, Unit: "mm"},
	}
	if err := s.monitor.AddObservations(ctx, campaign.ID, observations); err != nil {
		t.Fatalf("add observations: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_campaign_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='campaign.submitted' THEN
		RAISE EXCEPTION 'simulated campaign audit failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create campaign audit failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_campaign_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_campaign_audit()`); err != nil {
		t.Fatalf("create campaign audit failure trigger: %v", err)
	}

	if err := s.monitor.Submit(ctx, campaign.ID, campaign.Version+1); err == nil {
		t.Fatal("expected campaign audit failure")
	}
	found, err := s.monitor.Get(s.as(s.field), campaign.ID)
	if err != nil {
		t.Fatalf("read failed campaign submission: %v", err)
	}
	if found.Status != "collecting" || found.Version != campaign.Version+1 {
		t.Fatalf("failed campaign submission escaped rollback: %+v", found)
	}
	var auditCount, eventCount int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='campaign.submitted' AND resource_id=$1`, campaign.ID).Scan(&auditCount); err != nil {
		t.Fatalf("inspect campaign audit: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='campaign.submitted' AND aggregate_id=$1`, campaign.ID).Scan(&eventCount); err != nil {
		t.Fatalf("inspect campaign event: %v", err)
	}
	if auditCount != 0 || eventCount != 0 {
		t.Fatalf("failed campaign submission left side effects: audit=%d events=%d", auditCount, eventCount)
	}

	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_campaign_audit ON audit_entries`); err != nil {
		t.Fatalf("remove campaign audit failure trigger: %v", err)
	}
	if err := s.monitor.Submit(ctx, campaign.ID, campaign.Version+1); err != nil {
		t.Fatalf("submit after audit recovery: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='campaign.submitted' AND resource_id=$1`, campaign.ID).Scan(&auditCount); err != nil {
		t.Fatalf("inspect recovered campaign audit: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='campaign.submitted' AND aggregate_id=$1`, campaign.ID).Scan(&eventCount); err != nil {
		t.Fatalf("inspect recovered campaign event: %v", err)
	}
	if auditCount != 1 || eventCount != 1 {
		t.Fatalf("recovered campaign submission side effects: audit=%d events=%d", auditCount, eventCount)
	}
}
