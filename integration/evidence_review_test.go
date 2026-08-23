package integration_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestEvidenceItemsRollBackAsBatch(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	fieldContext := s.as(s.field)
	bundle, err := s.evidence.Create(fieldContext, seed.Site.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	items := []evidence.Item{{Kind: "field_note", ObjectKey: "notes/day-1.json", Checksum: "aaaabbbbccccdddd"}, {Kind: "unsupported", ObjectKey: "bad", Checksum: "1111222233334444"}, {Kind: "remote_asset", ObjectKey: "imagery/tile-9.tif", Checksum: "ffffeeee11112222"}}
	err = s.evidence.AddItems(fieldContext, bundle.ID, items)
	requireErrorIs(t, err, platform.ErrValidation)
	var count int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM evidence_items WHERE bundle_id=$1`, bundle.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("evidence batch partially persisted: %d", count)
	}
	items[1].Kind = "observation"
	if err := s.evidence.AddItems(fieldContext, bundle.ID, items); err != nil {
		t.Fatalf("add valid evidence batch: %v", err)
	}
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM evidence_items WHERE bundle_id=$1`, bundle.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected three evidence items, got %d", count)
	}
}

func TestSealFreezesEvidenceMembershipAndDigest(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	bundle, err := s.evidence.Create(ctx, seed.Site.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	items := []evidence.Item{{Kind: "remote_asset", ObjectKey: "tiles/b.tif", Checksum: "bbbbbbbbbbbbbbbb"}, {Kind: "field_note", ObjectKey: "notes/a.json", Checksum: "aaaaaaaaaaaaaaaa"}}
	if err := s.evidence.AddItems(ctx, bundle.ID, items); err != nil {
		t.Fatal(err)
	}
	digest, err := s.evidence.Seal(ctx, bundle.ID, bundle.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(digest) != 64 {
		t.Fatalf("unexpected digest %q", digest)
	}
	found, err := s.evidence.Get(s.as(s.field), bundle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "sealed" || found.Digest != digest || found.SealedAt == nil {
		t.Fatalf("unexpected sealed bundle: %+v", found)
	}
	err = s.evidence.AddItems(ctx, bundle.ID, []evidence.Item{{Kind: "field_note", ObjectKey: "notes/late.json", Checksum: "cccccccccccccccc"}})
	requireErrorIs(t, err, platform.ErrInvalidState)
	second, err := s.evidence.Create(ctx, seed.Site.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.evidence.AddItems(ctx, second.ID, []evidence.Item{{Kind: "field_note", ObjectKey: "notes/a.json", Checksum: "aaaaaaaaaaaaaaaa"}, {Kind: "remote_asset", ObjectKey: "tiles/b.tif", Checksum: "bbbbbbbbbbbbbbbb"}}); err != nil {
		t.Fatal(err)
	}
	secondDigest, err := s.evidence.Seal(ctx, second.ID, second.Version)
	if err != nil {
		t.Fatal(err)
	}
	if digest != secondDigest {
		t.Fatalf("digest must be independent of insertion order: %s != %s", digest, secondDigest)
	}
}

func TestConcurrentEvidenceRevisionsAreUnique(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	ctx := s.as(s.field)
	start := make(chan struct{})
	type outcome struct {
		bundle evidence.Bundle
		err    error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			b, err := s.evidence.Create(ctx, seed.Site.ID, "", "")
			results <- outcome{b, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	revisions := map[int]bool{}
	for result := range results {
		if result.err != nil {
			t.Fatalf("create concurrent revision: %v", result.err)
		}
		revisions[result.bundle.Revision] = true
	}
	if !revisions[1] || !revisions[2] || len(revisions) != 2 {
		t.Fatalf("unexpected revisions: %v", revisions)
	}
}

func TestReviewAssignmentRequiresTechnicalReviewerAndSealedBundle(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	fieldContext := s.as(s.field)
	bundle, err := s.evidence.Create(fieldContext, seed.Site.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.review.Assign(s.as(s.manager), bundle.ID, s.technical.UserID)
	requireErrorIs(t, err, platform.ErrInvalidState)
	if err := s.evidence.AddItems(fieldContext, bundle.ID, []evidence.Item{{Kind: "field_note", ObjectKey: "notes/site.json", Checksum: "0123456789abcdef"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.evidence.Seal(fieldContext, bundle.ID, bundle.Version); err != nil {
		t.Fatal(err)
	}
	_, err = s.review.Assign(s.as(s.manager), bundle.ID, s.field.UserID)
	requireErrorIs(t, err, platform.ErrForbidden)
	assigned, err := s.review.Assign(s.as(s.manager), bundle.ID, s.technical.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Status != "assigned" || assigned.Round != 1 {
		t.Fatalf("unexpected review: %+v", assigned)
	}
	found, err := s.evidence.Get(s.as(s.manager), bundle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "in_review" {
		t.Fatalf("bundle not advanced to review: %s", found.Status)
	}
}

func TestReviewConclusionIsAtomicWithEvidenceAndOutbox(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	bundle, reviewID := seedReview(t, s, seed.Site.ID)
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION reject_review_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.topic='review.accepted' THEN RAISE EXCEPTION 'simulated event failure'; END IF; RETURN NEW; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_review_event BEFORE INSERT ON outbox_events FOR EACH ROW EXECUTE FUNCTION reject_review_event()`); err != nil {
		t.Fatal(err)
	}
	err := s.review.Conclude(s.as(s.technical), reviewID, "accepted", "Evidence meets protocol", 1)
	if err == nil {
		t.Fatal("expected outbox failure")
	}
	reviewRecord, err := s.review.Get(s.as(s.technical), reviewID)
	if err != nil {
		t.Fatal(err)
	}
	if reviewRecord.Status != "assigned" {
		t.Fatalf("review escaped rollback: %+v", reviewRecord)
	}
	bundleRecord, err := s.evidence.Get(s.as(s.technical), bundle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bundleRecord.Status != "in_review" {
		t.Fatalf("bundle escaped rollback: %+v", bundleRecord)
	}
}

func TestConcurrentReviewConclusionsHaveSingleWinner(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	bundle, reviewID := seedReview(t, s, seed.Site.ID)
	ctx := s.as(s.technical)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, decision := range []string{"accepted", "rejected"} {
		decision := decision
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.review.Conclude(ctx, reviewID, decision, "Independent technical conclusion", 1)
		}()
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
			t.Errorf("unexpected loser error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("expected one conclusion winner, got %d", success)
	}
	reviewRecord, err := s.review.Get(s.as(s.technical), reviewID)
	if err != nil {
		t.Fatal(err)
	}
	bundleRecord, err := s.evidence.Get(s.as(s.technical), bundle.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reviewRecord.Status != bundleRecord.Status {
		t.Fatalf("review and bundle diverged: %s vs %s", reviewRecord.Status, bundleRecord.Status)
	}
}

func TestWorkCompletionRequiresOwnedLeaseAndSealedEvidence(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	managerContext := s.as(s.manager)
	plan, orders, err := s.work.CreatePlan(managerContext, seed.Site.ID, "Dune stabilization", 500_000, []intervention.WorkSpec{{Title: "Install windbreak"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.work.ApprovePlan(managerContext, plan.ID, plan.Version); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.work.Claim(s.ctx, "executor-a", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != orders[0].ID {
		t.Fatal("wrong work order claimed")
	}
	if err := s.work.Start(s.ctx, claimed.ID, "executor-a"); err != nil {
		t.Fatal(err)
	}
	bundle, err := s.evidence.Create(s.as(s.field), seed.Site.ID, "", claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	err = s.work.Complete(s.ctx, claimed.ID, "executor-a", "completed", bundle.ID)
	requireErrorIs(t, err, platform.ErrInvalidState)
	if err := s.evidence.AddItems(s.as(s.field), bundle.ID, []evidence.Item{{Kind: "completion_record", ObjectKey: "work/result.json", Checksum: "abcdef0123456789"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.evidence.Seal(s.as(s.field), bundle.ID, bundle.Version); err != nil {
		t.Fatal(err)
	}
	err = s.work.Complete(s.ctx, claimed.ID, "wrong-owner", "completed", bundle.ID)
	requireErrorIs(t, err, platform.ErrLeaseLost)
	if err := s.work.Complete(s.ctx, claimed.ID, "executor-a", "completed", bundle.ID); err != nil {
		t.Fatal(err)
	}
	found, err := s.work.GetOrder(s.as(s.manager), claimed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "completed" || found.OwnerToken != "" || found.LeaseExpiresAt != nil {
		t.Fatalf("work lease not released: %+v", found)
	}
}

func TestExpiredWorkLeaseCannotCompleteAfterReclaim(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	plan, _, err := s.work.CreatePlan(s.as(s.manager), seed.Site.ID, "Lease recovery", 300_000, []intervention.WorkSpec{{Title: "Prepare soil"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.work.ApprovePlan(s.as(s.manager), plan.ID, plan.Version); err != nil {
		t.Fatal(err)
	}
	first, err := s.work.Claim(s.ctx, "owner-old", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.clock.Advance(2 * time.Minute)
	second, err := s.work.Claim(s.ctx, "owner-new", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.OwnerToken != "owner-new" {
		t.Fatalf("unexpected reclaim: first=%+v second=%+v", first, second)
	}
	err = s.work.Renew(s.ctx, first.ID, "owner-old", time.Minute)
	requireErrorIs(t, err, platform.ErrLeaseLost)
	err = s.work.Start(s.ctx, first.ID, "owner-old")
	requireErrorIs(t, err, platform.ErrLeaseLost)
	if err := s.work.Start(s.ctx, second.ID, "owner-new"); err != nil {
		t.Fatalf("new owner starts work: %v", err)
	}
}

func seedReview(t *testing.T, s *suite, siteID string) (evidence.Bundle, string) {
	t.Helper()
	ctx := s.as(s.field)
	bundle, err := s.evidence.Create(ctx, siteID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.evidence.AddItems(ctx, bundle.ID, []evidence.Item{{Kind: "remote_asset", ObjectKey: "imagery/final.tif", Checksum: "0011223344556677"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.evidence.Seal(ctx, bundle.ID, bundle.Version); err != nil {
		t.Fatal(err)
	}
	assigned, err := s.review.Assign(s.as(s.manager), bundle.ID, s.technical.UserID)
	if err != nil {
		t.Fatal(err)
	}
	return bundle, assigned.ID
}
