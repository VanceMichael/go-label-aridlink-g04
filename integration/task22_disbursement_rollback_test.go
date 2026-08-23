package integration_test

import (
	"testing"
	"time"
)

func TestG04Task22DisbursementRollback(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	bundleID := acceptEvidence(t, s, seed.Site.ID)
	milestone, err := s.grant.CreateMilestone(s.as(s.manager), seed.Program.ID, seed.Site.ID, bundleID, "Disbursement rollback boundary", 350_000)
	if err != nil {
		t.Fatalf("create milestone: %v", err)
	}
	if err := s.grant.MarkEligible(s.as(s.technical), milestone.ID, milestone.Version); err != nil {
		t.Fatalf("mark milestone eligible: %v", err)
	}
	reservation, err := s.grant.Reserve(s.as(s.finance), milestone.ID, time.Hour)
	if err != nil {
		t.Fatalf("reserve milestone: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_disbursement_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='milestone.disbursed' THEN
		RAISE EXCEPTION 'simulated disbursement audit failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create disbursement audit failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_disbursement_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_disbursement_audit()`); err != nil {
		t.Fatalf("create disbursement audit failure trigger: %v", err)
	}
	if err := s.grant.Disburse(s.as(s.finance), milestone.ID); err == nil {
		t.Fatal("expected disbursement event failure")
	}
	var milestoneStatus, reservationStatus string
	if err := s.store.DB().QueryRow(s.ctx, `SELECT m.status,r.status FROM grant_milestones m JOIN budget_reservations r ON r.milestone_id=m.id WHERE m.id=$1`, milestone.ID).Scan(&milestoneStatus, &reservationStatus); err != nil {
		t.Fatalf("inspect failed disbursement: %v", err)
	}
	if milestoneStatus != "reserved" || reservationStatus != "held" {
		t.Errorf("failed disbursement escaped rollback: milestone=%s reservation=%s id=%s", milestoneStatus, reservationStatus, reservation.ID)
	}
	var audits, events int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='milestone.disbursed' AND resource_id=$1`, milestone.ID).Scan(&audits); err != nil {
		t.Fatalf("inspect disbursement audit: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='milestone.disbursed' AND aggregate_id=$1`, milestone.ID).Scan(&events); err != nil {
		t.Fatalf("inspect disbursement event: %v", err)
	}
	if audits != 0 || events != 0 {
		t.Errorf("failed disbursement left side effects: audits=%d events=%d", audits, events)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_disbursement_audit ON audit_entries`); err != nil {
		t.Fatalf("remove disbursement audit failure trigger: %v", err)
	}
	if err := s.grant.Disburse(s.as(s.finance), milestone.ID); err != nil {
		t.Errorf("disbursement after recovery: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT status FROM grant_milestones WHERE id=$1`, milestone.ID).Scan(&milestoneStatus); err != nil {
		t.Fatalf("inspect recovered milestone: %v", err)
	}
	if milestoneStatus != "disbursed" {
		t.Errorf("recovered milestone did not disburse: %s", milestoneStatus)
	}
}
