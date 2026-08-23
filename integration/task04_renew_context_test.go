package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
)

func TestG04Task04RenewHonorsCancelledContext(t *testing.T) {
	s := newSuite(t)
	managerContext := s.as(s.manager)
	seed := s.seedProgram()
	plan, orders, err := s.work.CreatePlan(managerContext, seed.Site.ID, "Context-bound lease", 200_000, []intervention.WorkSpec{{Title: "Inspect water point"}})
	if err != nil {
		t.Fatalf("create work plan: %v", err)
	}
	if err := s.work.ApprovePlan(managerContext, plan.ID, plan.Version); err != nil {
		t.Fatalf("approve work plan: %v", err)
	}
	claimed, err := s.work.Claim(s.ctx, "worker-task04", time.Minute)
	if err != nil {
		t.Fatalf("claim work order: %v", err)
	}
	if claimed.ID != orders[0].ID {
		t.Fatalf("claimed order %s, expected %s", claimed.ID, orders[0].ID)
	}
	before, err := s.work.GetOrder(managerContext, claimed.ID)
	if err != nil {
		t.Fatalf("read claimed order: %v", err)
	}
	if before.LeaseExpiresAt == nil {
		t.Fatal("claimed order has no lease")
	}

	cancelled, cancel := context.WithCancel(s.ctx)
	cancel()
	if err := s.work.Renew(cancelled, claimed.ID, "worker-task04", time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("renew with cancelled context returned %v", err)
	}
	after, err := s.work.GetOrder(managerContext, claimed.ID)
	if err != nil {
		t.Fatalf("read order after cancelled renewal: %v", err)
	}
	if after.Version != before.Version {
		t.Fatalf("cancelled renewal advanced order version from %d to %d", before.Version, after.Version)
	}
	if after.LeaseExpiresAt == nil || !after.LeaseExpiresAt.Equal(*before.LeaseExpiresAt) {
		t.Fatalf("cancelled renewal changed lease from %v to %v", before.LeaseExpiresAt, after.LeaseExpiresAt)
	}
	if err := s.work.Renew(s.ctx, claimed.ID, "worker-task04", time.Hour); err != nil {
		t.Fatalf("valid lease renewal after cancellation: %v", err)
	}
	normal, err := s.work.GetOrder(managerContext, claimed.ID)
	if err != nil {
		t.Fatalf("read order after valid renewal: %v", err)
	}
	if normal.Version != before.Version+1 {
		t.Fatalf("valid renewal did not advance order version once: %d", normal.Version)
	}
	if normal.LeaseExpiresAt == nil || !normal.LeaseExpiresAt.After(*before.LeaseExpiresAt) {
		t.Fatalf("valid renewal did not extend lease: before=%v after=%v", before.LeaseExpiresAt, normal.LeaseExpiresAt)
	}
}
