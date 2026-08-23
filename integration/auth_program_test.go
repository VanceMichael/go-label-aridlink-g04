package integration_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/program"
)

func TestMigrationCreatesRelationalSchema(t *testing.T) {
	s := newSuite(t)
	expectedTables := []string{"organizations", "users", "sessions", "programs", "partnerships", "sites",
		"monitoring_campaigns", "observations", "intervention_plans", "work_orders", "evidence_bundles",
		"evidence_items", "reviews", "grant_milestones", "budget_reservations", "alerts", "alert_sites",
		"alert_acknowledgements", "technology_transfers", "training_cohorts", "training_attendance",
		"outbox_events", "jobs", "audit_entries", "idempotency_records"}
	for _, table := range expectedTables {
		var exists bool
		if err := s.store.DB().QueryRow(s.ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=$1)`, table).Scan(&exists); err != nil {
			t.Fatalf("inspect table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("expected table %s", table)
		}
	}
	if err := s.store.Migrate(s.ctx, "../migrations"); err != nil {
		t.Fatalf("migration must be repeatable: %v", err)
	}
	var applied int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("expected one applied migration, got %d", applied)
	}
}

func TestAuthenticationLoginLogoutAndExpiry(t *testing.T) {
	s := newSuite(t)
	result, err := s.auth.Login(s.ctx, auth.Credentials{Email: "manager@aridlink.test", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatalf("login manager: %v", err)
	}
	if result.Token == "" || !result.ExpiresAt.After(s.clock.Now()) {
		t.Fatalf("invalid login result: %+v", result)
	}
	actor, err := s.auth.Authenticate(s.ctx, result.Token)
	if err != nil {
		t.Fatalf("authenticate manager: %v", err)
	}
	if actor.Role != platform.RoleProgramManager || actor.OrganizationID != s.ownerOrganization {
		t.Fatalf("unexpected actor: %+v", actor)
	}
	if err := s.auth.Logout(s.ctx, result.Token); err != nil {
		t.Fatalf("logout manager: %v", err)
	}
	_, err = s.auth.Authenticate(s.ctx, result.Token)
	requireErrorIs(t, err, platform.ErrUnauthorized)
	_, err = s.auth.Login(s.ctx, auth.Credentials{Email: "manager@aridlink.test", Password: "incorrect-password"})
	requireErrorIs(t, err, platform.ErrUnauthorized)

	expiring, err := s.auth.Login(s.ctx, auth.Credentials{Email: "field@aridlink.test", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatal(err)
	}
	s.clock.Advance(3 * time.Hour)
	_, err = s.auth.Authenticate(s.ctx, expiring.Token)
	requireErrorIs(t, err, platform.ErrUnauthorized)
}

func TestUserCreationHonorsOrganizationAndRoleBoundaries(t *testing.T) {
	s := newSuite(t)
	managerContext := s.as(s.manager)
	_, err := s.auth.CreateUser(managerContext, s.partnerOrganization, "outsider@aridlink.test", "correct-horse-battery", platform.RoleFieldOfficer)
	requireErrorIs(t, err, platform.ErrForbidden)
	_, err = s.auth.CreateUser(managerContext, s.ownerOrganization, "admin2@aridlink.test", "correct-horse-battery", platform.RolePlatformAdmin)
	requireErrorIs(t, err, platform.ErrValidation)
	created, err := s.auth.CreateUser(managerContext, s.ownerOrganization, "field2@aridlink.test", "correct-horse-battery", platform.RoleFieldOfficer)
	if err != nil {
		t.Fatalf("manager creates local field user: %v", err)
	}
	if created.OrganizationID != s.ownerOrganization || created.Role != platform.RoleFieldOfficer {
		t.Fatalf("unexpected created user: %+v", created)
	}
	_, err = s.auth.CreateOrganization(managerContext, "Unauthorized Organization", "EG", "research")
	requireErrorIs(t, err, platform.ErrForbidden)
}

func TestProgramActivationRollsBackWhenOutboxConflicts(t *testing.T) {
	s := newSuite(t)
	ctx := s.as(s.manager)
	created, err := s.program.Create(ctx, program.CreateInput{OwnerOrganizationID: s.ownerOrganization, Name: "Atomic Activation",
		StartsOn: s.clock.Now(), EndsOn: s.clock.Now().AddDate(5, 0, 0), BudgetCents: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.program.AddPartnership(ctx, created.ID, s.partnerOrganization, "implementation"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_program_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.topic='program.activated' THEN RAISE EXCEPTION 'simulated outbox failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_program_event BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_program_event()`); err != nil {
		t.Fatal(err)
	}
	_, err = s.program.Activate(ctx, created.ID, created.Version)
	if err == nil {
		t.Fatal("expected activation failure")
	}
	found, err := s.program.Get(s.as(s.manager), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "draft" || found.Version != created.Version {
		t.Fatalf("activation leaked partial state: %+v", found)
	}
	var auditCount int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM audit_entries WHERE resource_type='program' AND resource_id=$1 AND action='program.activated'`, created.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 0 {
		t.Fatalf("activation audit leaked after rollback: %d", auditCount)
	}
}

func TestConcurrentProgramActivationHasSingleWinner(t *testing.T) {
	s := newSuite(t)
	ctx := s.as(s.manager)
	created, err := s.program.Create(ctx, program.CreateInput{OwnerOrganizationID: s.ownerOrganization, Name: "Concurrent Activation",
		StartsOn: s.clock.Now(), EndsOn: s.clock.Now().AddDate(5, 0, 0), BudgetCents: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.program.AddPartnership(ctx, created.ID, s.partnerOrganization, "implementation"); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.program.Activate(ctx, created.ID, created.Version)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	success, failures := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			failures++
			if !errors.Is(err, platform.ErrConflict) && !errors.Is(err, platform.ErrInvalidState) {
				t.Errorf("unexpected loser error: %v", err)
			}
		}
	}
	if success != 1 || failures != 1 {
		t.Fatalf("expected one activation winner, successes=%d failures=%d", success, failures)
	}
	found, err := s.program.Get(s.as(s.manager), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "active" || found.Version != 2 {
		t.Fatalf("unexpected final program: %+v", found)
	}
}

func TestProgramCloseRequiresAllRelatedResourcesSettled(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	err := s.program.Close(s.as(s.manager), seed.Program.ID, seed.Program.Version)
	requireErrorIs(t, err, platform.ErrValidation)
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE sites SET status='archived' WHERE id=$1`, seed.Site.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.program.Close(s.as(s.manager), seed.Program.ID, seed.Program.Version); err != nil {
		t.Fatalf("close settled program: %v", err)
	}
	found, err := s.program.Get(s.as(s.manager), seed.Program.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "closed" {
		t.Fatalf("expected closed program, got %s", found.Status)
	}
}
