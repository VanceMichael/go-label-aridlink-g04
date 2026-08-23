package integration_test

import (
	"fmt"
	"testing"
	"time"
)

func TestG04Task15AlertExpiryBatchRollback(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	managerCtx := s.as(s.manager)
	first, err := s.alert.Create(managerCtx, seed.Program.ID, "drought", "warning", s.clock.Now().Add(-3*time.Hour), s.clock.Now().Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.alert.Publish(managerCtx, first.ID, []string{seed.Site.ID}, first.Version); err != nil {
		t.Fatal(err)
	}
	second, err := s.alert.Create(managerCtx, seed.Program.ID, "flood", "watch", s.clock.Now().Add(-2*time.Hour), s.clock.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.alert.Publish(managerCtx, second.ID, []string{seed.Site.ID}, second.Version); err != nil {
		t.Fatal(err)
	}
	function := fmt.Sprintf(`CREATE OR REPLACE FUNCTION reject_second_alert_expiry() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id='%s' AND NEW.status='expired' THEN RAISE EXCEPTION 'simulated second alert expiry failure'; END IF; RETURN NEW; END $$`, second.ID)
	if _, err := s.store.DB().Exec(s.ctx, function); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_second_alert_expiry BEFORE UPDATE ON alerts FOR EACH ROW EXECUTE FUNCTION reject_second_alert_expiry()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.alert.Expire(s.ctx, 2); err == nil {
		t.Fatal("expected batch expiry failure")
	}
	firstAfterFailure, err := s.alert.Get(managerCtx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondAfterFailure, err := s.alert.Get(managerCtx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstAfterFailure.Status != "published" || secondAfterFailure.Status != "published" {
		t.Fatalf("expiry batch partially committed: first=%+v second=%+v", firstAfterFailure, secondAfterFailure)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_second_alert_expiry ON alerts`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP FUNCTION reject_second_alert_expiry()`); err != nil {
		t.Fatal(err)
	}
	expired, err := s.alert.Expire(s.ctx, 2)
	if err != nil || expired != 2 {
		t.Fatalf("recovered expiry did not process both alerts: count=%d err=%v", expired, err)
	}
	firstFinal, err := s.alert.Get(managerCtx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondFinal, err := s.alert.Get(managerCtx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if firstFinal.Status != "expired" || secondFinal.Status != "expired" {
		t.Fatalf("unexpected recovered alert states: first=%+v second=%+v", firstFinal, secondFinal)
	}
}
