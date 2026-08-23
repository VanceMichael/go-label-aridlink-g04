package integration_test

import (
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
)

func TestG04Task16EvidenceItemsAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	bundle, err := s.evidence.Create(ctx, seed.Site.ID, "", "")
	if err != nil {
		t.Fatalf("create evidence bundle: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_items_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='evidence.items_added' THEN
		RAISE EXCEPTION 'simulated evidence items audit failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create items failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_items_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_items_audit()`); err != nil {
		t.Fatalf("create items failure trigger: %v", err)
	}
	items := []evidence.Item{{Kind: "field_note", ObjectKey: "items/a.json", Checksum: "0011223344556677"}, {Kind: "remote_asset", ObjectKey: "items/b.json", Checksum: "8899aabbccddeeff"}}
	if err := s.evidence.AddItems(ctx, bundle.ID, items); err == nil {
		t.Fatal("expected evidence items audit failure")
	}
	var itemCount, audits int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM evidence_items WHERE bundle_id=$1`, bundle.ID).Scan(&itemCount); err != nil {
		t.Fatalf("inspect failed evidence items: %v", err)
	}
	if itemCount != 0 {
		t.Fatalf("failed item batch left %d evidence items", itemCount)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='evidence.items_added' AND resource_id=$1`, bundle.ID).Scan(&audits); err != nil {
		t.Fatalf("inspect items audit: %v", err)
	}
	if audits != 0 {
		t.Fatalf("failed item batch left %d audits", audits)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_items_audit ON audit_entries`); err != nil {
		t.Fatalf("remove items failure trigger: %v", err)
	}
	if err := s.evidence.AddItems(ctx, bundle.ID, items); err != nil {
		t.Fatalf("add items after audit recovery: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM evidence_items WHERE bundle_id=$1`, bundle.ID).Scan(&itemCount); err != nil {
		t.Fatalf("inspect recovered evidence items: %v", err)
	}
	if itemCount != 2 {
		t.Fatalf("recovered item batch inserted %d evidence items", itemCount)
	}
}
