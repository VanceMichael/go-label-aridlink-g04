package integration_test

import (
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
)

func TestG04Task11PlanOrderAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.manager)
	beforePlans, beforeOrders := s.count("intervention_plans"), s.count("work_orders")
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_plan_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='plan.created' THEN
		RAISE EXCEPTION 'simulated plan audit failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create plan failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_plan_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_plan_audit()`); err != nil {
		t.Fatalf("create plan failure trigger: %v", err)
	}
	plan, orders, err := s.work.CreatePlan(ctx, seed.Site.ID, "Atomic dune works", 300_000, []intervention.WorkSpec{{Title: "Build contour bund"}, {Title: "Install runoff gauge"}})
	if err == nil {
		t.Fatal("expected plan audit failure")
	}
	if plan.ID == "" || len(orders) != 2 {
		t.Fatalf("unexpected failed plan result: plan=%+v orders=%d", plan, len(orders))
	}
	if got := s.count("intervention_plans"); got != beforePlans {
		t.Fatalf("failed plan creation left %d plans, want %d", got, beforePlans)
	}
	if got := s.count("work_orders"); got != beforeOrders {
		t.Fatalf("failed plan creation left %d work orders, want %d", got, beforeOrders)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_plan_audit ON audit_entries`); err != nil {
		t.Fatalf("remove plan failure trigger: %v", err)
	}
	created, createdOrders, err := s.work.CreatePlan(ctx, seed.Site.ID, "Atomic dune works", 300_000, []intervention.WorkSpec{{Title: "Build contour bund"}, {Title: "Install runoff gauge"}})
	if err != nil || created.Status != "draft" || len(createdOrders) != 2 {
		t.Fatalf("plan create after audit recovery: plan=%+v orders=%d err=%v", created, len(createdOrders), err)
	}
}
