package integration_test

import (
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/training"
)

func TestG04Task10AttendanceAuditAtomicity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	second := mustCreateUser(t, s.auth, s.as(s.admin), s.partnerOrganization, "audit-field@aridlink.test", platform.RoleFieldOfficer)
	cohort, err := s.training.Schedule(s.as(s.manager), seed.Program.ID, "Audit-bound attendance", 2, s.clock.Now().Add(-time.Hour), s.clock.Now().Add(8*time.Hour))
	if err != nil {
		t.Fatalf("schedule cohort: %v", err)
	}
	if err := s.training.OpenEnrollment(s.as(s.manager), cohort.ID, cohort.Version); err != nil {
		t.Fatalf("open enrollment: %v", err)
	}
	for _, user := range []auth.User{{ID: s.field.UserID}, second} {
		if err := s.training.Register(s.as(s.manager), cohort.ID, user.ID); err != nil {
			t.Fatalf("register participant: %v", err)
		}
	}
	if err := s.training.Start(s.as(s.manager), cohort.ID, cohort.Version+1); err != nil {
		t.Fatalf("start cohort: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_attendance_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.action='cohort.attendance_recorded' THEN
		RAISE EXCEPTION 'simulated attendance audit failure';
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create attendance failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_attendance_audit BEFORE INSERT ON audit_entries FOR EACH ROW EXECUTE FUNCTION reject_attendance_audit()`); err != nil {
		t.Fatalf("create attendance failure trigger: %v", err)
	}
	records := []training.Attendance{{UserID: s.field.UserID, Status: "attended"}, {UserID: second.ID, Status: "absent"}}
	if err := s.training.RecordAttendance(s.as(s.manager), cohort.ID, records); err == nil {
		t.Fatal("expected attendance audit failure")
	}
	var registered, audits int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM training_attendance WHERE cohort_id=$1 AND status='registered'`, cohort.ID).Scan(&registered); err != nil {
		t.Fatalf("inspect failed attendance batch: %v", err)
	}
	if registered != 2 {
		t.Fatalf("failed attendance batch resolved participants: registered=%d", registered)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE action='cohort.attendance_recorded' AND resource_id=$1`, cohort.ID).Scan(&audits); err != nil {
		t.Fatalf("inspect attendance audit: %v", err)
	}
	if audits != 0 {
		t.Fatalf("failed attendance batch left audits: %d", audits)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_attendance_audit ON audit_entries`); err != nil {
		t.Fatalf("remove attendance failure trigger: %v", err)
	}
	if err := s.training.RecordAttendance(s.as(s.manager), cohort.ID, records); err != nil {
		t.Fatalf("record attendance after audit recovery: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM training_attendance WHERE cohort_id=$1 AND status IN ('attended','absent')`, cohort.ID).Scan(&registered); err != nil {
		t.Fatalf("inspect recovered attendance batch: %v", err)
	}
	if registered != 2 {
		t.Fatalf("recovered attendance batch resolved %d participants", registered)
	}
}
