package integration_test

import (
	"testing"
	"time"
)

func TestG04Task05ReleaseExpiredAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	bundleID := acceptEvidence(t, s, seed.Site.ID)
	milestone, err := s.grant.CreateMilestone(s.as(s.manager), seed.Program.ID, seed.Site.ID, bundleID, "Short budget hold", 420_000)
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}
	if err := s.grant.MarkEligible(s.as(s.technical), milestone.ID, 1); err != nil {
		t.Fatalf("mark milestone eligible: %v", err)
	}
	reservation, err := s.grant.Reserve(s.as(s.finance), milestone.ID, time.Minute)
	if err != nil {
		t.Fatalf("reserve milestone: %v", err)
	}
	s.clock.Advance(time.Minute)
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_expired_milestone() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.status='eligible' THEN
		RAISE EXCEPTION 'simulated milestone transition failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create milestone failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_expired_milestone BEFORE UPDATE ON grant_milestones FOR EACH ROW EXECUTE FUNCTION reject_expired_milestone()`); err != nil {
		t.Fatalf("create milestone failure trigger: %v", err)
	}

	if released, err := s.grant.ReleaseExpired(s.ctx, 10); err == nil || released != 0 {
		t.Fatalf("expected atomic release failure, released=%d err=%v", released, err)
	}
	var reservationStatus, milestoneStatus string
	if err := s.store.DB().QueryRow(s.ctx, `SELECT r.status,m.status FROM budget_reservations r JOIN grant_milestones m ON m.id=r.milestone_id WHERE r.id=$1`, reservation.ID).Scan(&reservationStatus, &milestoneStatus); err != nil {
		t.Fatalf("inspect failed release: %v", err)
	}
	if reservationStatus != "held" || milestoneStatus != "reserved" {
		t.Fatalf("failed release escaped transaction: reservation=%s milestone=%s", reservationStatus, milestoneStatus)
	}

	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_expired_milestone ON grant_milestones`); err != nil {
		t.Fatalf("remove milestone failure trigger: %v", err)
	}
	if released, err := s.grant.ReleaseExpired(s.ctx, 10); err != nil || released != 1 {
		t.Fatalf("release after recovery: released=%d err=%v", released, err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT r.status,m.status FROM budget_reservations r JOIN grant_milestones m ON m.id=r.milestone_id WHERE r.id=$1`, reservation.ID).Scan(&reservationStatus, &milestoneStatus); err != nil {
		t.Fatalf("inspect recovered release: %v", err)
	}
	if reservationStatus != "released" || milestoneStatus != "eligible" {
		t.Fatalf("recovered release left inconsistent state: reservation=%s milestone=%s", reservationStatus, milestoneStatus)
	}
}
