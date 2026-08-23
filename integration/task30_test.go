package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
)

func TestG04Task30EvidenceItemIntakeCancellation(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	fieldContext := s.as(s.field)
	bundle, err := s.evidence.Create(fieldContext, seed.Site.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	items := []evidence.Item{{Kind: "field_note", ObjectKey: "notes/site.json", Checksum: "0123456789abcdef"}}

	cancelled, cancel := context.WithCancel(fieldContext)
	cancel()
	if err := s.evidence.AddItems(cancelled, bundle.ID, items); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected item intake cancellation, got %v", err)
	}

	var itemCount, auditCount int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM evidence_items WHERE bundle_id=$1`, bundle.ID).Scan(&itemCount); err != nil {
		t.Fatal(err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE resource_type='evidence_bundle' AND resource_id=$1 AND action='evidence.items_added'`, bundle.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if itemCount != 0 || auditCount != 0 {
		t.Fatalf("cancelled item intake leaked state: items=%d audits=%d", itemCount, auditCount)
	}

	if err := s.evidence.AddItems(fieldContext, bundle.ID, items); err != nil {
		t.Fatalf("add items after cancellation: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM evidence_items WHERE bundle_id=$1`, bundle.ID).Scan(&itemCount); err != nil {
		t.Fatal(err)
	}
	if itemCount != 1 {
		t.Fatalf("unexpected item count after successful intake: %d", itemCount)
	}
}
