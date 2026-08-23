package integration_test

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/alert"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/audit"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/grant"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/idempotency"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/monitoring"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/program"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/query"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/review"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/site"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/technology"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/training"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/worker"
	"github.com/jackc/pgx/v5"
)

var nonIdentifier = regexp.MustCompile(`[^a-z0-9_]+`)

type suite struct {
	t                   *testing.T
	ctx                 context.Context
	store               *store.Store
	clock               *platform.FixedClock
	ids                 *platform.SequenceIDs
	auth                *auth.Service
	program             *program.Service
	site                *site.Service
	monitor             *monitoring.Service
	work                *intervention.Service
	evidence            *evidence.Service
	review              *review.Service
	grant               *grant.Service
	alert               *alert.Service
	technology          *technology.Service
	training            *training.Service
	outbox              *outbox.Service
	jobs                *worker.Jobs
	query               *query.Service
	idempotency         *idempotency.Service
	admin               platform.Actor
	manager             platform.Actor
	field               platform.Actor
	technical           platform.Actor
	finance             platform.Actor
	ownerOrganization   string
	partnerOrganization string
}

type seededProgram struct {
	Program program.Program
	Site    site.Site
}

func newSuite(t *testing.T) *suite {
	t.Helper()
	ctx := context.Background()
	baseURL := os.Getenv("ARIDLINK_TEST_DATABASE_URL")
	if baseURL == "" {
		baseURL = "postgres://aridlink:aridlink@localhost:55432/aridlink?sslmode=disable"
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	schema := nonIdentifier.ReplaceAllString(strings.ToLower("test_"+t.Name()+fmt.Sprintf("_%d", time.Now().UnixNano())), "_")
	adminConnection, err := pgx.Connect(ctx, baseURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	if _, err := adminConnection.Exec(ctx, `CREATE SCHEMA `+pgx.Identifier{schema}.Sanitize()); err != nil {
		adminConnection.Close(ctx)
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		adminConnection.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+pgx.Identifier{schema}.Sanitize()+` CASCADE`)
		adminConnection.Close(context.Background())
	})
	parameters := parsed.Query()
	parameters.Set("search_path", schema)
	parsed.RawQuery = parameters.Encode()
	st, err := store.Open(ctx, parsed.String())
	if err != nil {
		t.Fatalf("open schema store: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx, "../migrations"); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	clock := platform.NewFixedClock(time.Date(2026, 8, 23, 4, 0, 0, 0, time.UTC))
	ids := &platform.SequenceIDs{}
	writer := audit.NewWriter(ids, clock)
	events := outbox.NewService(st, ids, clock)
	authService := auth.NewService(st, ids, clock, writer, 2*time.Hour)
	if err := authService.Bootstrap(ctx, "admin@aridlink.test", "correct-horse-battery"); err != nil {
		t.Fatalf("bootstrap auth: %v", err)
	}
	adminLogin, err := authService.Login(ctx, auth.Credentials{Email: "admin@aridlink.test", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatalf("login bootstrap admin: %v", err)
	}
	adminActor, err := authService.Authenticate(ctx, adminLogin.Token)
	if err != nil {
		t.Fatalf("authenticate bootstrap admin: %v", err)
	}
	adminContext := platform.WithActor(ctx, adminActor)
	owner, err := authService.CreateOrganization(adminContext, "Joint Action Secretariat", "INT", "international")
	if err != nil {
		t.Fatalf("create owner organization: %v", err)
	}
	partner, err := authService.CreateOrganization(adminContext, "Al Noor Field Institute", "JOR", "research")
	if err != nil {
		t.Fatalf("create partner organization: %v", err)
	}
	manager := mustCreateUser(t, authService, adminContext, owner.ID, "manager@aridlink.test", platform.RoleProgramManager)
	field := mustCreateUser(t, authService, adminContext, partner.ID, "field@aridlink.test", platform.RoleFieldOfficer)
	technical := mustCreateUser(t, authService, adminContext, owner.ID, "reviewer@aridlink.test", platform.RoleTechnicalReviewer)
	finance := mustCreateUser(t, authService, adminContext, owner.ID, "finance@aridlink.test", platform.RoleFinanceReviewer)
	result := &suite{t: t, ctx: ctx, store: st, clock: clock, ids: ids, auth: authService,
		program: program.NewService(st, ids, clock, writer, events), site: site.NewService(st, ids, clock, writer, events),
		monitor: monitoring.NewService(st, ids, clock, writer, events), work: intervention.NewService(st, ids, clock, writer, events),
		evidence: evidence.NewService(st, ids, clock, writer, events), review: review.NewService(st, ids, clock, writer, events),
		grant: grant.NewService(st, ids, clock, writer, events), alert: alert.NewService(st, ids, clock, writer, events),
		technology: technology.NewService(st, ids, clock, writer, events), training: training.NewService(st, ids, clock, writer, events),
		outbox: events, jobs: worker.NewJobs(st, ids, clock), query: query.NewService(st, clock),
		idempotency: idempotency.NewService(st, ids, clock, time.Hour), admin: adminActor,
		manager: actorFor(manager), field: actorFor(field), technical: actorFor(technical), finance: actorFor(finance),
		ownerOrganization: owner.ID, partnerOrganization: partner.ID}
	return result
}

func mustCreateUser(t *testing.T, service *auth.Service, ctx context.Context, organizationID, email string, role platform.Role) auth.User {
	t.Helper()
	user, err := service.CreateUser(ctx, organizationID, email, "correct-horse-battery", role)
	if err != nil {
		t.Fatalf("create %s: %v", role, err)
	}
	return user
}

func actorFor(user auth.User) platform.Actor {
	return platform.Actor{UserID: user.ID, OrganizationID: user.OrganizationID, Role: user.Role}
}

func (s *suite) as(actor platform.Actor) context.Context {
	return platform.WithActor(s.ctx, actor)
}

func (s *suite) seedProgram() seededProgram {
	s.t.Helper()
	managerContext := s.as(s.manager)
	created, err := s.program.Create(managerContext, program.CreateInput{OwnerOrganizationID: s.ownerOrganization,
		Name: "Dryland Recovery 2026-2030", StartsOn: s.clock.Now(), EndsOn: s.clock.Now().AddDate(5, 0, 0), BudgetCents: 10_000_000})
	if err != nil {
		s.t.Fatalf("create program: %v", err)
	}
	if _, err := s.program.AddPartnership(managerContext, created.ID, s.partnerOrganization, "implementation"); err != nil {
		s.t.Fatalf("add partnership: %v", err)
	}
	active, err := s.program.Activate(managerContext, created.ID, created.Version)
	if err != nil {
		s.t.Fatalf("activate program: %v", err)
	}
	createdSite, err := s.site.Create(managerContext, site.CreateInput{ProgramID: active.ID, OrganizationID: s.partnerOrganization,
		Name: "Wadi Al Noor Demonstration Site", CountryCode: "JOR", AreaHectares: 1420.5, Ecosystem: "dryland"})
	if err != nil {
		s.t.Fatalf("create site: %v", err)
	}
	approved, err := s.site.Approve(managerContext, createdSite.ID, createdSite.Version)
	if err != nil {
		s.t.Fatalf("approve site: %v", err)
	}
	return seededProgram{Program: active, Site: approved}
}

func (s *suite) count(table string) int {
	s.t.Helper()
	allowed := map[string]bool{"organizations": true, "users": true, "sessions": true, "programs": true, "partnerships": true,
		"sites": true, "monitoring_campaigns": true, "observations": true, "intervention_plans": true, "work_orders": true,
		"evidence_bundles": true, "evidence_items": true, "reviews": true, "grant_milestones": true, "budget_reservations": true,
		"alerts": true, "alert_sites": true, "alert_acknowledgements": true, "outbox_events": true, "jobs": true, "audit_entries": true}
	if !allowed[table] {
		s.t.Fatalf("table %s is not allowed", table)
	}
	var count int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
		s.t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("expected error %v, got %v", target, err)
	}
}
