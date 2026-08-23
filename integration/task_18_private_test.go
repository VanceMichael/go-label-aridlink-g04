package integration_test

import (
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
)

func TestG04Task18ReviewAssignmentAuditRollback(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	fieldCtx := s.as(s.field)
	bundle, err := s.evidence.Create(fieldCtx, seed.Site.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.evidence.AddItems(fieldCtx, bundle.ID, []evidence.Item{{Kind: "field_note", ObjectKey: "notes/review-18.json", Checksum: "0123456789abcdef"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.evidence.Seal(fieldCtx, bundle.ID, bundle.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_review_assignment_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='review.assigned' THEN RAISE EXCEPTION 'simulated review assignment audit failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_review_assignment_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_review_assignment_audit()`); err != nil {
		t.Fatal(err)
	}
	_, err = s.review.Assign(s.as(s.manager), bundle.ID, s.technical.UserID)
	if err == nil {
		t.Fatal("expected assignment audit failure")
	}
	failedBundle, err := s.evidence.Get(s.as(s.manager), bundle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failedBundle.Status != "sealed" {
		t.Fatalf("review assignment escaped rollback: %+v", failedBundle)
	}
	var reviews int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM reviews WHERE bundle_id=$1`, bundle.ID).Scan(&reviews); err != nil {
		t.Fatal(err)
	}
	if reviews != 0 {
		t.Fatalf("review row escaped rollback: %d", reviews)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_review_assignment_audit ON audit_entries`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP FUNCTION reject_review_assignment_audit()`); err != nil {
		t.Fatal(err)
	}
	assigned, err := s.review.Assign(s.as(s.manager), bundle.ID, s.technical.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Status != "assigned" || assigned.Round != 1 {
		t.Fatalf("retry did not create the first review: %+v", assigned)
	}
}
