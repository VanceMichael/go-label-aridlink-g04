package integration_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/training"
)

func TestConcurrentBudgetReservationsNeverExceedProgramBudget(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	bundleID := acceptEvidence(t, s, seed.Site.ID)
	milestones := make([]string, 0, 2)
	for i := range 2 {
		milestone, err := s.grant.CreateMilestone(s.as(s.manager), seed.Program.ID, seed.Site.ID, bundleID, fmt.Sprintf("Restoration tranche %d", i+1), 6_000_000)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.grant.MarkEligible(s.as(s.technical), milestone.ID, milestone.Version); err != nil {
			t.Fatal(err)
		}
		milestones = append(milestones, milestone.ID)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, milestoneID := range milestones {
		milestoneID := milestoneID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.grant.Reserve(s.as(s.finance), milestoneID, time.Hour)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, platform.ErrBudgetExceeded) && !errors.Is(err, platform.ErrConflict) {
			t.Fatalf("unexpected reservation loser: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected one budget winner, got %d", successes)
	}
	var held int64
	if err := s.store.DB().QueryRow(s.ctx, `SELECT COALESCE(sum(amount_cents),0) FROM budget_reservations WHERE program_id=$1 AND status='held'`, seed.Program.ID).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 6_000_000 {
		t.Fatalf("budget was overcommitted: %d", held)
	}
}

func TestDisbursementConsumesReservationAndPublishesEventAtomically(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	bundleID := acceptEvidence(t, s, seed.Site.ID)
	milestone, err := s.grant.CreateMilestone(s.as(s.manager), seed.Program.ID, seed.Site.ID, bundleID, "Verified planting", 750_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.grant.MarkEligible(s.as(s.technical), milestone.ID, 1); err != nil {
		t.Fatal(err)
	}
	reservation, err := s.grant.Reserve(s.as(s.finance), milestone.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.grant.Disburse(s.as(s.finance), milestone.ID); err != nil {
		t.Fatal(err)
	}
	found, err := s.grant.GetReservation(s.as(s.finance), reservation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "consumed" {
		t.Fatalf("reservation not consumed: %+v", found)
	}
	var milestoneStatus string
	var events int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT status FROM grant_milestones WHERE id=$1`, milestone.ID).Scan(&milestoneStatus); err != nil {
		t.Fatal(err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='milestone.disbursed' AND aggregate_id=$1`, milestone.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if milestoneStatus != "disbursed" || events != 1 {
		t.Fatalf("disbursement diverged: status=%s events=%d", milestoneStatus, events)
	}
}

func TestDisbursementRollsBackWhenOutboxWriteFails(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	bundleID := acceptEvidence(t, s, seed.Site.ID)
	milestone, err := s.grant.CreateMilestone(s.as(s.manager), seed.Program.ID, seed.Site.ID, bundleID, "Rollback tranche", 350_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.grant.MarkEligible(s.as(s.technical), milestone.ID, 1); err != nil {
		t.Fatal(err)
	}
	reservation, err := s.grant.Reserve(s.as(s.finance), milestone.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE FUNCTION fail_disbursement_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.topic='milestone.disbursed' THEN RAISE EXCEPTION 'event unavailable'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER fail_disbursement_event BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION fail_disbursement_event()`); err != nil {
		t.Fatal(err)
	}
	if err := s.grant.Disburse(s.as(s.finance), milestone.ID); err == nil {
		t.Fatal("expected outbox failure")
	}
	var milestoneStatus, reservationStatus string
	if err := s.store.DB().QueryRow(s.ctx, `SELECT m.status,r.status FROM grant_milestones m JOIN budget_reservations r ON r.milestone_id=m.id WHERE m.id=$1`, milestone.ID).Scan(&milestoneStatus, &reservationStatus); err != nil {
		t.Fatal(err)
	}
	if milestoneStatus != "reserved" || reservationStatus != "held" {
		t.Fatalf("failed disbursement escaped rollback: milestone=%s reservation=%s (%s)", milestoneStatus, reservationStatus, reservation.ID)
	}
}

func TestExpiredReservationReturnsMilestoneToEligible(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	bundleID := acceptEvidence(t, s, seed.Site.ID)
	milestone, err := s.grant.CreateMilestone(s.as(s.manager), seed.Program.ID, seed.Site.ID, bundleID, "Short hold", 420_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.grant.MarkEligible(s.as(s.technical), milestone.ID, 1); err != nil {
		t.Fatal(err)
	}
	reservation, err := s.grant.Reserve(s.as(s.finance), milestone.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if released, err := s.grant.ReleaseExpired(s.ctx, 10); err != nil || released != 0 {
		t.Fatalf("premature release: released=%d err=%v", released, err)
	}
	s.clock.Advance(time.Minute)
	if released, err := s.grant.ReleaseExpired(s.ctx, 10); err != nil || released != 1 {
		t.Fatalf("release expired: released=%d err=%v", released, err)
	}
	found, err := s.grant.GetReservation(s.as(s.finance), reservation.ID)
	if err != nil {
		t.Fatal(err)
	}
	var milestoneStatus string
	if err := s.store.DB().QueryRow(s.ctx, `SELECT status FROM grant_milestones WHERE id=$1`, milestone.ID).Scan(&milestoneStatus); err != nil {
		t.Fatal(err)
	}
	if found.Status != "released" || milestoneStatus != "eligible" {
		t.Fatalf("release states diverged: reservation=%s milestone=%s", found.Status, milestoneStatus)
	}
}

func TestAlertPublishIsAtomicWithAffectedSitesAndOutbox(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	created, err := s.alert.Create(s.as(s.manager), seed.Program.ID, "drought", "warning", s.clock.Now(), s.clock.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE FUNCTION fail_alert_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.topic='alert.published' THEN RAISE EXCEPTION 'event unavailable'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER fail_alert_event BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION fail_alert_event()`); err != nil {
		t.Fatal(err)
	}
	if err := s.alert.Publish(s.as(s.manager), created.ID, []string{seed.Site.ID}, created.Version); err == nil {
		t.Fatal("expected event failure")
	}
	found, err := s.alert.Get(s.as(s.manager), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var affected int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM alert_sites WHERE alert_id=$1`, created.ID).Scan(&affected); err != nil {
		t.Fatal(err)
	}
	if found.Status != "draft" || found.Version != 1 || affected != 0 {
		t.Fatalf("failed publish escaped rollback: alert=%+v affected=%d", found, affected)
	}
}

func TestAlertAcknowledgementIsIsolatedByAffectedOrganization(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	created, err := s.alert.Create(s.as(s.manager), seed.Program.ID, "dust", "emergency", s.clock.Now(), s.clock.Now().Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.alert.Publish(s.as(s.manager), created.ID, []string{seed.Site.ID, seed.Site.ID}, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.alert.Acknowledge(s.as(s.field), created.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.alert.Acknowledge(s.as(s.field), created.ID); err != nil {
		t.Fatalf("repeated acknowledgement should be harmless: %v", err)
	}
	outsiderOrg, err := s.auth.CreateOrganization(s.as(s.admin), "Unaffected Observatory", "TUN", "research")
	if err != nil {
		t.Fatal(err)
	}
	outsiderUser := mustCreateUser(t, s.auth, s.as(s.admin), outsiderOrg.ID, "outsider@aridlink.test", platform.RoleFieldOfficer)
	if err := s.alert.Acknowledge(s.as(actorFor(outsiderUser)), created.ID); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("unaffected tenant acknowledgement: %v", err)
	}
	var acknowledgements int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM alert_acknowledgements WHERE alert_id=$1`, created.ID).Scan(&acknowledgements); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 1 {
		t.Fatalf("unexpected acknowledgement count: %d", acknowledgements)
	}
}

func TestAlertExpiryOnlyAdvancesDuePublishedAlerts(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	due, err := s.alert.Create(s.as(s.manager), seed.Program.ID, "wildfire", "watch", s.clock.Now().Add(-time.Hour), s.clock.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	future, err := s.alert.Create(s.as(s.manager), seed.Program.ID, "flood", "advisory", s.clock.Now(), s.clock.Now().Add(3*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id      string
		version int64
	}{{due.ID, due.Version}, {future.ID, future.Version}} {
		if err := s.alert.Publish(s.as(s.manager), item.id, []string{seed.Site.ID}, item.version); err != nil {
			t.Fatal(err)
		}
	}
	s.clock.Advance(time.Minute)
	if expired, err := s.alert.Expire(s.ctx, 10); err != nil || expired != 1 {
		t.Fatalf("expire alerts: count=%d err=%v", expired, err)
	}
	dueFound, _ := s.alert.Get(s.as(s.manager), due.ID)
	futureFound, _ := s.alert.Get(s.as(s.manager), future.ID)
	if dueFound.Status != "expired" || futureFound.Status != "published" {
		t.Fatalf("unexpected expiry states: due=%s future=%s", dueFound.Status, futureFound.Status)
	}
}

func TestTechnologyTransferRequiresApprovalAndSiteTenant(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	transfer, err := s.technology.Propose(s.as(s.manager), seed.Program.ID, seed.Site.ID, "Biochar soil protocol", "v2.4.1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.technology.Deploy(s.as(s.field), transfer.ID, transfer.Version); !errors.Is(err, platform.ErrInvalidState) {
		t.Fatalf("deploy before approval: %v", err)
	}
	if err := s.technology.Approve(s.as(s.technical), transfer.ID, transfer.Version); err != nil {
		t.Fatal(err)
	}
	foreign := platform.Actor{UserID: "foreign-field", OrganizationID: s.ownerOrganization, Role: platform.RoleFieldOfficer}
	if err := s.technology.Deploy(s.as(foreign), transfer.ID, 2); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign deployment: %v", err)
	}
	if err := s.technology.Deploy(s.as(s.field), transfer.ID, 2); err != nil {
		t.Fatal(err)
	}
	found, err := s.technology.Get(s.as(s.manager), transfer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "deployed" || found.Version != 3 {
		t.Fatalf("unexpected transfer: %+v", found)
	}
}

func TestTrainingLifecycleRequiresRegistrationAndResolvedAttendance(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	cohort, err := s.training.Schedule(s.as(s.manager), seed.Program.ID, "Dryland field methods", 2, s.clock.Now().Add(-time.Hour), s.clock.Now().Add(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.training.OpenEnrollment(s.as(s.manager), cohort.ID, cohort.Version); err != nil {
		t.Fatal(err)
	}
	if err := s.training.Start(s.as(s.manager), cohort.ID, 2); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("start empty cohort: %v", err)
	}
	if err := s.training.Register(s.as(s.field), cohort.ID, s.field.UserID); err != nil {
		t.Fatal(err)
	}
	if err := s.training.Start(s.as(s.manager), cohort.ID, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.training.Complete(s.as(s.manager), cohort.ID, 3); !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("complete unresolved cohort: %v", err)
	}
	if err := s.training.RecordAttendance(s.as(s.manager), cohort.ID, []training.Attendance{{UserID: s.field.UserID, Status: "attended"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.training.Complete(s.as(s.manager), cohort.ID, 3); err != nil {
		t.Fatal(err)
	}
	found, err := s.training.Get(s.as(s.manager), cohort.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "completed" || found.Version != 4 {
		t.Fatalf("unexpected cohort: %+v", found)
	}
}

func TestTrainingAttendanceBatchRollsBackOnInvalidParticipant(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	second := mustCreateUser(t, s.auth, s.as(s.admin), s.partnerOrganization, "second-field@aridlink.test", platform.RoleFieldOfficer)
	cohort, err := s.training.Schedule(s.as(s.manager), seed.Program.ID, "Joint monitoring", 2, s.clock.Now().Add(-time.Hour), s.clock.Now().Add(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.training.OpenEnrollment(s.as(s.manager), cohort.ID, 1); err != nil {
		t.Fatal(err)
	}
	for _, user := range []auth.User{{ID: s.field.UserID}, second} {
		if err := s.training.Register(s.as(s.manager), cohort.ID, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.training.Start(s.as(s.manager), cohort.ID, 2); err != nil {
		t.Fatal(err)
	}
	err = s.training.RecordAttendance(s.as(s.manager), cohort.ID, []training.Attendance{{UserID: s.field.UserID, Status: "attended"}, {UserID: second.ID, Status: "unknown"}})
	if !errors.Is(err, platform.ErrValidation) {
		t.Fatalf("invalid attendance batch: %v", err)
	}
	var registered int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM training_attendance WHERE cohort_id=$1 AND status='registered'`, cohort.ID).Scan(&registered); err != nil {
		t.Fatal(err)
	}
	if registered != 2 {
		t.Fatalf("attendance batch partially persisted: %d still registered", registered)
	}
}

func TestConcurrentTrainingRegistrationHonorsCapacity(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	second := mustCreateUser(t, s.auth, s.as(s.admin), s.partnerOrganization, "capacity-field@aridlink.test", platform.RoleFieldOfficer)
	cohort, err := s.training.Schedule(s.as(s.manager), seed.Program.ID, "Limited workshop", 1, s.clock.Now().Add(-time.Hour), s.clock.Now().Add(8*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.training.OpenEnrollment(s.as(s.manager), cohort.ID, 1); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, userID := range []string{s.field.UserID, second.ID} {
		userID := userID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.training.Register(s.as(s.manager), cohort.ID, userID)
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, platform.ErrConflict) {
			t.Fatalf("unexpected registration loser: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("expected one registration, got %d", successes)
	}
}

func TestCrossTenantRolesCannotMutateAnotherProgramsOperations(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	outsiderOrg, err := s.auth.CreateOrganization(s.as(s.admin), "Independent Dryland Agency", "DZA", "government")
	if err != nil {
		t.Fatal(err)
	}
	outsiderManager := mustCreateUser(t, s.auth, s.as(s.admin), outsiderOrg.ID, "outsider-manager@aridlink.test", platform.RoleProgramManager)
	outsiderTechnical := mustCreateUser(t, s.auth, s.as(s.admin), outsiderOrg.ID, "outsider-technical@aridlink.test", platform.RoleTechnicalReviewer)
	outsiderFinance := mustCreateUser(t, s.auth, s.as(s.admin), outsiderOrg.ID, "outsider-finance@aridlink.test", platform.RoleFinanceReviewer)
	outsiderField := mustCreateUser(t, s.auth, s.as(s.admin), outsiderOrg.ID, "outsider-field@aridlink.test", platform.RoleFieldOfficer)
	outsiderContext := s.as(actorFor(outsiderManager))

	if _, err := s.program.Get(outsiderContext, seed.Program.ID); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign manager read program: %v", err)
	}
	if _, err := s.site.Get(outsiderContext, seed.Site.ID); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign manager read site: %v", err)
	}
	if _, err := s.monitor.Plan(outsiderContext, seed.Site.ID, "foreign-cycle"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign manager planned monitoring: %v", err)
	}
	campaign, err := s.monitor.Plan(s.as(s.field), seed.Site.ID, "tenant-cycle")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.monitor.Get(outsiderContext, campaign.ID); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign manager read campaign: %v", err)
	}
	transfer, err := s.technology.Propose(s.as(s.manager), seed.Program.ID, seed.Site.ID, "Tenant-scoped protocol", "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.technology.Approve(s.as(actorFor(outsiderTechnical)), transfer.ID, transfer.Version); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign reviewer approved technology: %v", err)
	}
	if err := s.technology.Approve(s.as(s.technical), transfer.ID, transfer.Version); err != nil {
		t.Fatal(err)
	}

	cohort, err := s.training.Schedule(s.as(s.manager), seed.Program.ID, "Tenant operations", 4, s.clock.Now().Add(-time.Hour), s.clock.Now().Add(4*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.training.OpenEnrollment(s.as(actorFor(outsiderManager)), cohort.ID, cohort.Version); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign manager opened enrollment: %v", err)
	}
	if err := s.training.OpenEnrollment(s.as(s.manager), cohort.ID, cohort.Version); err != nil {
		t.Fatal(err)
	}
	if err := s.training.Register(s.as(actorFor(outsiderField)), cohort.ID, outsiderField.ID); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("unaffiliated field officer registered: %v", err)
	}

	bundleID := acceptEvidence(t, s, seed.Site.ID)
	if _, err := s.evidence.Get(outsiderContext, bundleID); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign manager read evidence: %v", err)
	}
	milestone, err := s.grant.CreateMilestone(s.as(s.manager), seed.Program.ID, seed.Site.ID, bundleID, "Scoped payment", 500_000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.grant.MarkEligible(s.as(actorFor(outsiderTechnical)), milestone.ID, milestone.Version); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign reviewer marked milestone eligible: %v", err)
	}
	if err := s.grant.MarkEligible(s.as(s.technical), milestone.ID, milestone.Version); err != nil {
		t.Fatal(err)
	}
	if _, err := s.grant.Reserve(s.as(actorFor(outsiderFinance)), milestone.ID, time.Hour); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign finance reviewer reserved budget: %v", err)
	}
	if _, err := s.grant.Reserve(s.as(s.finance), milestone.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := s.grant.Disburse(s.as(actorFor(outsiderFinance)), milestone.ID); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("foreign finance reviewer disbursed budget: %v", err)
	}
	if err := s.grant.Disburse(s.as(s.finance), milestone.ID); err != nil {
		t.Fatal(err)
	}
}

func acceptEvidence(t *testing.T, s *suite, siteID string) string {
	t.Helper()
	bundle, reviewID := seedReview(t, s, siteID)
	if err := s.review.Conclude(s.as(s.technical), reviewID, "accepted", "Protocol and provenance verified", 1); err != nil {
		t.Fatal(err)
	}
	found, err := s.evidence.Get(s.as(s.manager), bundle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "accepted" {
		t.Fatalf("evidence not accepted: %+v", found)
	}
	return bundle.ID
}
