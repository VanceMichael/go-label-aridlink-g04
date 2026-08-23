package integration_test

import (
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
)

func TestG04Task08EvidenceSealAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	bundle, err := s.evidence.Create(ctx, seed.Site.ID, "", "")
	if err != nil {
		t.Fatalf("create evidence bundle: %v", err)
	}
	if err := s.evidence.AddItems(ctx, bundle.ID, []evidence.Item{{Kind: "field_note", ObjectKey: "notes/seal.json", Checksum: "0011223344556677"}}); err != nil {
		t.Fatalf("add evidence item: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_evidence_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.topic='evidence.sealed' THEN
		RAISE EXCEPTION 'simulated evidence event failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create evidence event failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_evidence_event BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_evidence_event()`); err != nil {
		t.Fatalf("create evidence event failure trigger: %v", err)
	}

	if _, err := s.evidence.Seal(ctx, bundle.ID, bundle.Version); err == nil {
		t.Fatal("expected evidence event failure")
	}
	found, err := s.evidence.Get(s.as(s.field), bundle.ID)
	if err != nil {
		t.Fatalf("read failed seal: %v", err)
	}
	if found.Status != "draft" || found.Version != bundle.Version || found.Digest != "" {
		t.Fatalf("failed seal escaped rollback: %+v", found)
	}
	var auditCount int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='evidence.sealed' AND resource_id=$1`, bundle.ID).Scan(&auditCount); err != nil {
		t.Fatalf("inspect evidence audit: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("failed seal left audit entries: %d", auditCount)
	}

	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_evidence_event ON outbox_events`); err != nil {
		t.Fatalf("remove evidence event failure trigger: %v", err)
	}
	digest, err := s.evidence.Seal(ctx, bundle.ID, bundle.Version)
	if err != nil || digest == "" {
		t.Fatalf("seal after event recovery: digest=%q err=%v", digest, err)
	}
	found, err = s.evidence.Get(s.as(s.field), bundle.ID)
	if err != nil {
		t.Fatalf("read recovered seal: %v", err)
	}
	if found.Status != "sealed" || found.Version != bundle.Version+1 || found.Digest != digest {
		t.Fatalf("unexpected recovered seal: %+v", found)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='evidence.sealed' AND resource_id=$1`, bundle.ID).Scan(&auditCount); err != nil {
		t.Fatalf("inspect recovered evidence audit: %v", err)
	}
	var eventCount int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='evidence.sealed' AND aggregate_id=$1`, bundle.ID).Scan(&eventCount); err != nil {
		t.Fatalf("inspect recovered evidence event: %v", err)
	}
	if auditCount != 1 || eventCount != 1 {
		t.Fatalf("recovered seal side effects: audit=%d events=%d", auditCount, eventCount)
	}
}
