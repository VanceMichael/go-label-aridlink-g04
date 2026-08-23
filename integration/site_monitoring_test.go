package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/monitoring"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/program"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/site"
)

func TestSiteProposalRequiresActiveProgramAndPartnership(t *testing.T) {
	s := newSuite(t)
	ctx := s.as(s.manager)
	created, err := s.program.Create(ctx, program.CreateInput{OwnerOrganizationID: s.ownerOrganization, Name: "Inactive Program", StartsOn: s.clock.Now(), EndsOn: s.clock.Now().AddDate(5, 0, 0), BudgetCents: 900_000})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.site.Create(ctx, site.CreateInput{ProgramID: created.ID, OrganizationID: s.partnerOrganization, Name: "Premature Site", CountryCode: "JOR", AreaHectares: 50, Ecosystem: "dryland"})
	requireErrorIs(t, err, platform.ErrInvalidState)
	if _, err := s.program.AddPartnership(ctx, created.ID, s.partnerOrganization, "implementation"); err != nil {
		t.Fatal(err)
	}
	active, err := s.program.Activate(ctx, created.ID, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.auth.CreateOrganization(s.as(s.admin), "Unrelated Institute", "MAR", "research")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.site.Create(ctx, site.CreateInput{ProgramID: active.ID, OrganizationID: other.ID, Name: "Unrelated Site", CountryCode: "MAR", AreaHectares: 50, Ecosystem: "oasis"})
	requireErrorIs(t, err, platform.ErrForbidden)
	createdSite, err := s.site.Create(ctx, site.CreateInput{ProgramID: active.ID, OrganizationID: s.partnerOrganization, Name: "Partner Site", CountryCode: "JOR", AreaHectares: 50, Ecosystem: "dryland"})
	if err != nil {
		t.Fatal(err)
	}
	if createdSite.Status != "proposed" || createdSite.Version != 1 {
		t.Fatalf("unexpected site: %+v", createdSite)
	}
}

func TestConcurrentSiteApprovalOnlyAdvancesOneVersion(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.manager)
	if _, err := s.store.DB().Exec(s.ctx, `DELETE FROM outbox_events WHERE topic='site.approved' AND aggregate_id=$1`, seed.Site.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE sites SET status='proposed',version=1 WHERE id=$1`, seed.Site.ID); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, err := s.site.Approve(ctx, seed.Site.ID, 1); results <- err }()
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		if !errors.Is(err, platform.ErrConflict) && !errors.Is(err, platform.ErrInvalidState) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("expected one approval winner, got %d", success)
	}
	found, err := s.site.Get(s.as(s.manager), seed.Site.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "approved" || found.Version != 2 {
		t.Fatalf("unexpected site: %+v", found)
	}
	var events int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM outbox_events WHERE topic='site.approved' AND aggregate_id=$1`, seed.Site.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("expected one approval event, got %d", events)
	}
}

func TestSiteArchiveChecksWorkEvidenceAndBudgetBlockers(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.manager)
	plan, orders, err := s.work.CreatePlan(ctx, seed.Site.ID, "Windbreak maintenance", 200_000, []intervention.WorkSpec{{Title: "Stabilize dune"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE intervention_plans SET status='approved' WHERE id=$1`, plan.ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.work.Claim(s.ctx, "worker-one", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != orders[0].ID {
		t.Fatalf("claimed %s, expected %s", claimed.ID, orders[0].ID)
	}
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE sites SET status='restored' WHERE id=$1`, seed.Site.ID); err != nil {
		t.Fatal(err)
	}
	err = s.site.Archive(ctx, seed.Site.ID, seed.Site.Version)
	requireErrorIs(t, err, platform.ErrValidation)
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE work_orders SET status='cancelled',owner_token=NULL,lease_expires_at=NULL WHERE id=$1`, claimed.ID); err != nil {
		t.Fatal(err)
	}
	bundle, err := s.evidence.Create(s.as(s.field), seed.Site.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	err = s.site.Archive(ctx, seed.Site.ID, seed.Site.Version)
	requireErrorIs(t, err, platform.ErrValidation)
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE evidence_bundles SET status='superseded' WHERE id=$1`, bundle.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.site.Archive(ctx, seed.Site.ID, seed.Site.Version); err != nil {
		t.Fatalf("archive settled site: %v", err)
	}
}

func TestMonitoringCampaignEnforcesSingleCollector(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	fieldContext := s.as(s.field)
	first, err := s.monitor.Plan(fieldContext, seed.Site.ID, "2026-Q3")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.monitor.Plan(fieldContext, seed.Site.ID, "2026-Q4")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.monitor.Start(fieldContext, first.ID, first.Version); err != nil {
		t.Fatal(err)
	}
	err = s.monitor.Start(fieldContext, second.ID, second.Version)
	requireErrorIs(t, err, platform.ErrConflict)
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE monitoring_campaigns SET status='cancelled' WHERE id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.monitor.Start(fieldContext, second.ID, second.Version); err != nil {
		t.Fatalf("start after collector released: %v", err)
	}
}

func TestObservationBatchIsAtomic(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	campaign, err := s.monitor.Plan(ctx, seed.Site.ID, "2026-Q3")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.monitor.Start(ctx, campaign.ID, campaign.Version); err != nil {
		t.Fatal(err)
	}
	items := []monitoring.Observation{{Kind: "soil", MeasuredAt: s.clock.Now(), Value: 0.42, Unit: "fraction"}, {Kind: "invalid-kind", MeasuredAt: s.clock.Now(), Value: 4, Unit: "ppm"}, {Kind: "vegetation", MeasuredAt: s.clock.Now(), Value: 0.31, Unit: "fraction"}}
	err = s.monitor.AddObservations(ctx, campaign.ID, items)
	requireErrorIs(t, err, platform.ErrValidation)
	var count int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM observations WHERE campaign_id=$1`, campaign.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("partial observation batch persisted: %d", count)
	}
	items[1].Kind = "dust"
	if err := s.monitor.AddObservations(ctx, campaign.ID, items); err != nil {
		t.Fatalf("add valid batch: %v", err)
	}
	observations, err := s.monitor.ListObservations(s.as(s.field), campaign.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 3 {
		t.Fatalf("expected three observations, got %d", len(observations))
	}
}

func TestCampaignSubmissionRequiresCompleteObservationSet(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	campaign, err := s.monitor.Plan(ctx, seed.Site.ID, "2026-Q3")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.monitor.Start(ctx, campaign.ID, campaign.Version); err != nil {
		t.Fatal(err)
	}
	if err := s.monitor.AddObservations(ctx, campaign.ID, []monitoring.Observation{{Kind: "soil", MeasuredAt: s.clock.Now(), Value: 1, Unit: "index"}}); err != nil {
		t.Fatal(err)
	}
	err = s.monitor.Submit(ctx, campaign.ID, 2)
	requireErrorIs(t, err, platform.ErrValidation)
	if err := s.monitor.AddObservations(ctx, campaign.ID, []monitoring.Observation{{Kind: "vegetation", MeasuredAt: s.clock.Now(), Value: 2, Unit: "index"}, {Kind: "dust", MeasuredAt: s.clock.Now(), Value: 3, Unit: "index"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.monitor.Submit(ctx, campaign.ID, 2); err != nil {
		t.Fatalf("submit complete campaign: %v", err)
	}
	found, err := s.monitor.Get(s.as(s.field), campaign.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "submitted" || found.SubmittedAt == nil {
		t.Fatalf("unexpected campaign: %+v", found)
	}
}

func TestObservationScanHonorsCancelledContext(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	campaign, err := s.monitor.Plan(ctx, seed.Site.ID, "2026-Q3")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.monitor.Start(ctx, campaign.ID, campaign.Version); err != nil {
		t.Fatal(err)
	}
	items := make([]monitoring.Observation, 40)
	for i := range items {
		items[i] = monitoring.Observation{Kind: "soil", MeasuredAt: s.clock.Now().Add(time.Duration(i) * time.Minute), Value: float64(i), Unit: "index"}
	}
	if err := s.monitor.AddObservations(ctx, campaign.ID, items); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(s.ctx)
	cancel()
	_, err = s.monitor.ListObservations(cancelled, campaign.ID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}
