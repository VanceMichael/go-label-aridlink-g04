package integration_test

import (
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/training"
)

func TestG04Task27AttendanceCompletionAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	cohort, err := s.training.Schedule(s.as(s.manager), seed.Program.ID, "Completion audit boundary", 1, s.clock.Now().Add(-time.Hour), s.clock.Now().Add(8*time.Hour))
	if err != nil {
		t.Fatalf("schedule cohort: %v", err)
	}
	if err := s.training.OpenEnrollment(s.as(s.manager), cohort.ID, cohort.Version); err != nil {
		t.Fatalf("open enrollment: %v", err)
	}
	if err := s.training.Register(s.as(s.manager), cohort.ID, s.field.UserID); err != nil {
		t.Fatalf("register participant: %v", err)
	}
	if err := s.training.Start(s.as(s.manager), cohort.ID, cohort.Version+1); err != nil {
		t.Fatalf("start cohort: %v", err)
	}
	if err := s.training.RecordAttendance(s.as(s.manager), cohort.ID, []training.Attendance{{UserID: s.field.UserID, Status: "attended"}}); err != nil {
		t.Fatalf("record attendance: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_cohort_completion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.topic='cohort.completed' THEN
		RAISE EXCEPTION 'simulated cohort completion event failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create completion failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_cohort_completion BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_cohort_completion()`); err != nil {
		t.Fatalf("create completion failure trigger: %v", err)
	}
	if err := s.training.Complete(s.as(s.manager), cohort.ID, cohort.Version+2); err == nil {
		t.Fatal("expected cohort completion event failure")
	}
	found, err := s.training.Get(s.as(s.manager), cohort.ID)
	if err != nil {
		t.Fatalf("read failed cohort completion: %v", err)
	}
	if found.Status != "running" || found.Version != cohort.Version+2 {
		t.Fatalf("failed completion escaped rollback: %+v", found)
	}
	var audits, events int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='cohort.completed' AND resource_id=$1`, cohort.ID).Scan(&audits); err != nil {
		t.Fatalf("inspect completion audit: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='cohort.completed' AND aggregate_id=$1`, cohort.ID).Scan(&events); err != nil {
		t.Fatalf("inspect completion event: %v", err)
	}
	if audits != 0 || events != 0 {
		t.Fatalf("failed completion left side effects: audits=%d events=%d", audits, events)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_cohort_completion ON outbox_events`); err != nil {
		t.Fatalf("remove completion failure trigger: %v", err)
	}
	if err := s.training.Complete(s.as(s.manager), cohort.ID, found.Version); err != nil {
		t.Fatalf("complete cohort after recovery: %v", err)
	}
	if found, err = s.training.Get(s.as(s.manager), cohort.ID); err != nil || found.Status != "completed" {
		t.Fatalf("unexpected recovered cohort: %+v err=%v", found, err)
	}
	_ = platform.ErrConflict
}
