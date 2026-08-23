package integration_test

import (
	"errors"
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/program"
)

func TestG04Task01ActivationAtomicity(t *testing.T) {
	s := newSuite(t)
	ctx := s.as(s.manager)
	created, err := s.program.Create(ctx, program.CreateInput{
		OwnerOrganizationID: s.ownerOrganization,
		Name:                "Stale Activation Contract",
		StartsOn:            s.clock.Now(),
		EndsOn:              s.clock.Now().AddDate(5, 0, 0),
		BudgetCents:         1_500_000,
	})
	if err != nil {
		t.Fatalf("create draft program: %v", err)
	}
	if _, err := s.program.AddPartnership(ctx, created.ID, s.partnerOrganization, "implementation"); err != nil {
		t.Fatalf("add implementation partner: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE programs SET version=version+1 WHERE id=$1`, created.ID); err != nil {
		t.Fatalf("advance program version: %v", err)
	}

	_, err = s.program.Activate(ctx, created.ID, created.Version)
	if !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("expected stale activation conflict, got %v", err)
	}

	var status string
	var version int64
	if err := s.store.DB().QueryRow(s.ctx, `SELECT status,version FROM programs WHERE id=$1`, created.ID).Scan(&status, &version); err != nil {
		t.Fatalf("read program after conflict: %v", err)
	}
	if status != "draft" || version != created.Version+1 {
		t.Fatalf("stale activation changed program: status=%s version=%d", status, version)
	}

	var eventCount, auditCount int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='program.activated' AND aggregate_id=$1`, created.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count activation events: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='program.activated' AND resource_id=$1`, created.ID).Scan(&auditCount); err != nil {
		t.Fatalf("count activation audits: %v", err)
	}
	if eventCount != 0 || auditCount != 0 {
		t.Fatalf("failed activation leaked side effects: events=%d audits=%d", eventCount, auditCount)
	}

	activated, err := s.program.Activate(ctx, created.ID, version)
	if err != nil {
		t.Fatalf("activate with current version: %v", err)
	}
	if activated.Status != "active" || activated.Version != version+1 {
		t.Fatalf("valid activation returned unexpected program: %+v", activated)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='program.activated' AND aggregate_id=$1`, created.ID).Scan(&eventCount); err != nil {
		t.Fatalf("count successful activation events: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='program.activated' AND resource_id=$1`, created.ID).Scan(&auditCount); err != nil {
		t.Fatalf("count successful activation audits: %v", err)
	}
	if eventCount != 1 || auditCount != 1 {
		t.Fatalf("valid activation side effects mismatch: events=%d audits=%d", eventCount, auditCount)
	}
}
