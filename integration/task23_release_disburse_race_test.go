package integration_test

import (
	"testing"
	"time"
)

func TestG04Task23ReleaseDisburseRace(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	bundleID := acceptEvidence(t, s, seed.Site.ID)
	milestone, err := s.grant.CreateMilestone(s.as(s.manager), seed.Program.ID, seed.Site.ID, bundleID, "Recovery boundary", 310_000)
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}
	if err := s.grant.MarkEligible(s.as(s.technical), milestone.ID, milestone.Version); err != nil {
		t.Fatalf("mark eligible: %v", err)
	}
	reservation, err := s.grant.Reserve(s.as(s.finance), milestone.ID, time.Minute)
	if err != nil {
		t.Fatalf("reserve budget: %v", err)
	}
	s.clock.Advance(time.Minute)
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_release_milestone() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.status='eligible' THEN
		RAISE EXCEPTION 'simulated milestone recovery failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create recovery failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_release_milestone BEFORE UPDATE ON grant_milestones FOR EACH ROW EXECUTE FUNCTION reject_release_milestone()`); err != nil {
		t.Fatalf("create recovery failure trigger: %v", err)
	}
	if released, err := s.grant.ReleaseExpired(s.ctx, 10); err == nil || released != 0 {
		t.Fatalf("expected atomic recovery failure, released=%d err=%v", released, err)
	}
	found, err := s.grant.GetReservation(s.as(s.finance), reservation.ID)
	if err != nil {
		t.Fatalf("read failed recovery reservation: %v", err)
	}
	var milestoneStatus string
	if err := s.store.DB().QueryRow(s.ctx, `SELECT status FROM grant_milestones WHERE id=$1`, milestone.ID).Scan(&milestoneStatus); err != nil {
		t.Fatalf("read failed recovery milestone: %v", err)
	}
	if found.Status != "held" || milestoneStatus != "reserved" {
		t.Fatalf("recovery split reservation and milestone: reservation=%s milestone=%s", found.Status, milestoneStatus)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_release_milestone ON grant_milestones`); err != nil {
		t.Fatalf("remove recovery failure trigger: %v", err)
	}
	if released, err := s.grant.ReleaseExpired(s.ctx, 10); err != nil || released != 1 {
		t.Fatalf("release after recovery: released=%d err=%v", released, err)
	}
}
