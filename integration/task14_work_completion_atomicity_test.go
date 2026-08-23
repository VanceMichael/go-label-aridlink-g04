package integration_test

import (
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
)

func TestG04Task14WorkCompletionAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.manager)
	plan, orders, err := s.work.CreatePlan(ctx, seed.Site.ID, "Completion evidence chain", 250_000, []intervention.WorkSpec{{Title: "Verify recharge basin"}})
	if err != nil {
		t.Fatalf("create work plan: %v", err)
	}
	if err := s.work.ApprovePlan(ctx, plan.ID, plan.Version); err != nil {
		t.Fatalf("approve work plan: %v", err)
	}
	claimed, err := s.work.Claim(s.ctx, "worker-task14", time.Minute)
	if err != nil || claimed.ID != orders[0].ID {
		t.Fatalf("claim work order: %+v err=%v", claimed, err)
	}
	if err := s.work.Start(s.ctx, claimed.ID, claimed.OwnerToken); err != nil {
		t.Fatalf("start work order: %v", err)
	}
	bundle, err := s.evidence.Create(s.as(s.field), seed.Site.ID, "", claimed.ID)
	if err != nil {
		t.Fatalf("create completion evidence: %v", err)
	}
	if err := s.evidence.AddItems(s.as(s.field), bundle.ID, []evidence.Item{{Kind: "field_note", ObjectKey: "completion/task14.json", Checksum: "1122334455667788"}}); err != nil {
		t.Fatalf("add completion evidence: %v", err)
	}
	if _, err := s.evidence.Seal(s.as(s.field), bundle.ID, bundle.Version); err != nil {
		t.Fatalf("seal completion evidence: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_completion_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.topic='work_order.completed' THEN
		RAISE EXCEPTION 'simulated completion event failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create completion failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_completion_event BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_completion_event()`); err != nil {
		t.Fatalf("create completion failure trigger: %v", err)
	}
	if err := s.work.Complete(s.ctx, claimed.ID, claimed.OwnerToken, "basin verified", bundle.ID); err == nil {
		t.Fatal("expected completion event failure")
	}
	found, err := s.work.GetOrder(ctx, claimed.ID)
	if err != nil {
		t.Fatalf("read failed completion: %v", err)
	}
	if found.Status != "running" || found.OwnerToken != claimed.OwnerToken || found.ResultSummary != "" {
		t.Fatalf("failed completion escaped rollback: %+v", found)
	}
	var audits, events int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='work_order.completed' AND resource_id=$1`, claimed.ID).Scan(&audits); err != nil {
		t.Fatalf("inspect completion audit: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='work_order.completed' AND aggregate_id=$1`, claimed.ID).Scan(&events); err != nil {
		t.Fatalf("inspect completion event: %v", err)
	}
	if audits != 0 || events != 0 {
		t.Fatalf("failed completion left side effects: audits=%d events=%d", audits, events)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_completion_event ON outbox_events`); err != nil {
		t.Fatalf("remove completion failure trigger: %v", err)
	}
	if err := s.work.Complete(s.ctx, claimed.ID, claimed.OwnerToken, "basin verified", bundle.ID); err != nil {
		t.Fatalf("complete after event recovery: %v", err)
	}
	found, err = s.work.GetOrder(ctx, claimed.ID)
	if err != nil || found.Status != "completed" || found.ResultSummary != "basin verified" {
		t.Fatalf("unexpected recovered completion: %+v err=%v", found, err)
	}
}
