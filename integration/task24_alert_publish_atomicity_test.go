package integration_test

import (
	"testing"
	"time"
)

func TestG04Task24AlertPublishAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	created, err := s.alert.Create(s.as(s.manager), seed.Program.ID, "flood", "warning", s.clock.Now(), s.clock.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("create flood alert: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_alert_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='alert.published' THEN
		RAISE EXCEPTION 'simulated alert audit failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create alert failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_alert_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_alert_audit()`); err != nil {
		t.Fatalf("create alert failure trigger: %v", err)
	}
	if err := s.alert.Publish(s.as(s.manager), created.ID, []string{seed.Site.ID}, created.Version); err == nil {
		t.Fatal("expected flood alert audit failure")
	}
	found, err := s.alert.Get(s.as(s.manager), created.ID)
	if err != nil {
		t.Fatalf("read failed flood alert: %v", err)
	}
	if found.Status != "draft" || found.Version != created.Version {
		t.Fatalf("failed flood publish changed alert: %+v", found)
	}
	var affected, audits, events int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM alert_sites WHERE alert_id=$1`, created.ID).Scan(&affected); err != nil {
		t.Fatalf("inspect affected sites: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='alert.published' AND resource_id=$1`, created.ID).Scan(&audits); err != nil {
		t.Fatalf("inspect alert audit: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='alert.published' AND aggregate_id=$1`, created.ID).Scan(&events); err != nil {
		t.Fatalf("inspect alert event: %v", err)
	}
	if affected != 0 || audits != 0 || events != 0 {
		t.Fatalf("failed flood publish left side effects: affected=%d audits=%d events=%d", affected, audits, events)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_alert_audit ON audit_entries`); err != nil {
		t.Fatalf("remove alert failure trigger: %v", err)
	}
	recovered, err := s.alert.Create(s.as(s.manager), seed.Program.ID, "flood", "advisory", s.clock.Now(), s.clock.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("create recovered flood alert: %v", err)
	}
	if err := s.alert.Publish(s.as(s.manager), recovered.ID, []string{seed.Site.ID}, recovered.Version); err != nil {
		t.Fatalf("publish flood alert after recovery: %v", err)
	}
	if found, err = s.alert.Get(s.as(s.manager), recovered.ID); err != nil || found.Status != "published" {
		t.Fatalf("unexpected recovered flood alert: %+v err=%v", found, err)
	}
}
