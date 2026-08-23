package integration_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestG04Task12ExclusiveWorkClaim(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	plan, orders, err := s.work.CreatePlan(s.as(s.manager), seed.Site.ID, "Exclusive field operation", 200_000, []intervention.WorkSpec{{Title: "Inspect terrace"}})
	if err != nil {
		t.Fatalf("create work plan: %v", err)
	}
	if err := s.work.ApprovePlan(s.as(s.manager), plan.ID, plan.Version); err != nil {
		t.Fatalf("approve work plan: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE OR REPLACE FUNCTION delay_claim_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.status='claimed' THEN
		PERFORM pg_sleep(0.2);
	END IF;
	RETURN NEW;
END $$`); err != nil {
		t.Fatalf("create claim barrier function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER delay_claim_update BEFORE UPDATE ON work_orders FOR EACH ROW EXECUTE FUNCTION delay_claim_update()`); err != nil {
		t.Fatalf("create claim barrier trigger: %v", err)
	}
	start := make(chan struct{})
	results := make(chan struct {
		order intervention.WorkOrder
		err   error
	}, 2)
	var wg sync.WaitGroup
	for _, token := range []string{"worker-task12-a", "worker-task12-b"} {
		token := token
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			order, claimErr := s.work.Claim(s.ctx, token, time.Minute)
			results <- struct {
				order intervention.WorkOrder
				err   error
			}{order: order, err: claimErr}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	owners := map[string]bool{}
	for result := range results {
		if result.err == nil {
			successes++
			owners[result.order.OwnerToken] = true
			if result.order.ID != orders[0].ID {
				t.Fatalf("claimed unexpected work order: %+v", result.order)
			}
			continue
		}
		if !errors.Is(result.err, platform.ErrNotFound) && !errors.Is(result.err, platform.ErrConflict) {
			t.Fatalf("unexpected losing claim error: %v", result.err)
		}
	}
	if successes != 1 || len(owners) != 1 {
		t.Fatalf("expected one exclusive claim, successes=%d owners=%v", successes, owners)
	}
	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER delay_claim_update ON work_orders`); err != nil {
		t.Fatalf("remove claim barrier trigger: %v", err)
	}
}
