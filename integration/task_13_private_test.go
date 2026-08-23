package integration_test

import (
	"errors"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestG04Task13JobLeaseABA(t *testing.T) {
	s := newSuite(t)
	jobID, err := s.jobs.Enqueue(s.ctx, s.store.DB(), "monitoring.refresh", "program-1", "lease-aba-13", map[string]any{"window": "2026-08-23"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.jobs.Claim(s.ctx, "worker-old", time.Minute, 1)
	if err != nil || len(first) != 1 || first[0].ID != jobID {
		t.Fatalf("initial claim: jobs=%+v err=%v", first, err)
	}

	s.clock.Advance(time.Minute)
	second, err := s.jobs.Claim(s.ctx, "worker-new", time.Minute, 1)
	if err != nil || len(second) != 1 || second[0].ID != jobID {
		t.Fatalf("reclaim after expiry: jobs=%+v err=%v", second, err)
	}

	if err := s.jobs.Renew(s.ctx, jobID, "worker-old", time.Minute); !errors.Is(err, platform.ErrLeaseLost) {
		t.Fatalf("stale owner renewed reclaimed job: %v", err)
	}
	if err := s.jobs.Renew(s.ctx, jobID, "worker-new", time.Minute); err != nil {
		t.Fatalf("current owner could not renew job: %v", err)
	}
	current, err := s.jobs.Get(s.ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if current.OwnerToken != "worker-new" || current.LeaseExpiresAt == nil {
		t.Fatalf("lease ownership changed unexpectedly: %+v", current)
	}
}
