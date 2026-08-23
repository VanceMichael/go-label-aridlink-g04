package integration_test

import "testing"

func TestG04Task06TechnologyDeployAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	transfer, err := s.technology.Propose(s.as(s.manager), seed.Program.ID, seed.Site.ID, "Water harvesting protocol", "v2.1.0")
	if err != nil {
		t.Fatalf("propose technology transfer: %v", err)
	}
	if err := s.technology.Approve(s.as(s.technical), transfer.ID, transfer.Version); err != nil {
		t.Fatalf("approve technology transfer: %v", err)
	}
	approvedVersion := transfer.Version + 1
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_technology_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='technology.deployed' THEN
		RAISE EXCEPTION 'simulated technology audit failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create technology audit failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_technology_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_technology_audit()`); err != nil {
		t.Fatalf("create technology audit failure trigger: %v", err)
	}

	if err := s.technology.Deploy(s.as(s.field), transfer.ID, approvedVersion); err == nil {
		t.Fatal("expected deployment audit failure")
	}
	found, err := s.technology.Get(s.as(s.field), transfer.ID)
	if err != nil {
		t.Fatalf("read failed deployment: %v", err)
	}
	if found.Status != "approved" || found.Version != approvedVersion {
		t.Fatalf("failed deployment escaped rollback: %+v", found)
	}
	var auditCount, eventCount int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='technology.deployed' AND resource_id=$1`, transfer.ID).Scan(&auditCount); err != nil {
		t.Fatalf("inspect deployment audit: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='technology.deployed' AND aggregate_id=$1`, transfer.ID).Scan(&eventCount); err != nil {
		t.Fatalf("inspect deployment event: %v", err)
	}
	if auditCount != 0 || eventCount != 0 {
		t.Fatalf("failed deployment left side effects: audit=%d events=%d", auditCount, eventCount)
	}

	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_technology_audit ON audit_entries`); err != nil {
		t.Fatalf("remove technology audit failure trigger: %v", err)
	}
	if err := s.technology.Deploy(s.as(s.field), transfer.ID, approvedVersion); err != nil {
		t.Fatalf("deploy after audit recovery: %v", err)
	}
	found, err = s.technology.Get(s.as(s.field), transfer.ID)
	if err != nil {
		t.Fatalf("read recovered deployment: %v", err)
	}
	if found.Status != "deployed" || found.Version != approvedVersion+1 {
		t.Fatalf("unexpected recovered deployment: %+v", found)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='technology.deployed' AND resource_id=$1`, transfer.ID).Scan(&auditCount); err != nil {
		t.Fatalf("inspect recovered deployment audit: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='technology.deployed' AND aggregate_id=$1`, transfer.ID).Scan(&eventCount); err != nil {
		t.Fatalf("inspect recovered deployment event: %v", err)
	}
	if auditCount != 1 || eventCount != 1 {
		t.Fatalf("recovered deployment side effects: audit=%d events=%d", auditCount, eventCount)
	}
}
