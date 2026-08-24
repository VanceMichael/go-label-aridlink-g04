package integration_test

import (
	"errors"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestG04Task02ProgramCloseActiveJob(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	jobID, err := s.jobs.Enqueue(s.ctx, s.store.DB(), "program.reconcile", seed.Program.ID, "reconcile-"+seed.Program.ID, map[string]string{"program_id": seed.Program.ID})
	if err != nil {
		t.Fatalf("enqueue reconciliation job: %v", err)
	}
	claimed, err := s.jobs.Claim(s.ctx, "worker-terminal", time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != jobID {
		t.Fatalf("claim reconciliation job: jobs=%+v err=%v", claimed, err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE FUNCTION reject_terminal_lease_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.status='dead' AND NEW.owner_token IS NULL THEN
				RAISE EXCEPTION 'simulated lease cleanup failure';
			END IF;
			RETURN NEW;
		END $$`); err != nil {
		t.Fatalf("create cleanup failure function: %v", err)
	}
	if _, err := s.store.DB().Exec(s.ctx, `CREATE TRIGGER reject_terminal_lease_cleanup
		BEFORE UPDATE ON jobs FOR EACH ROW EXECUTE FUNCTION reject_terminal_lease_cleanup()`); err != nil {
		t.Fatalf("create cleanup failure trigger: %v", err)
	}

	err = s.jobs.Fail(s.ctx, jobID, "worker-terminal", errors.New("permanent reconciliation failure"), time.Minute, 1)
	if err == nil {
		t.Fatal("expected terminal failure transition to fail")
	}
	job, getErr := s.jobs.Get(s.ctx, jobID)
	if getErr != nil {
		t.Fatalf("read job after failed transition: %v", getErr)
	}
	if job.Status != "running" || job.OwnerToken != "worker-terminal" || job.LeaseExpiresAt == nil {
		t.Errorf("failed transition split job state and lease: %+v", job)
	}
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE sites SET status='archived' WHERE id=$1`, seed.Site.ID); err != nil {
		t.Fatalf("archive settled site: %v", err)
	}
	closeErr := s.program.Close(s.as(s.manager), seed.Program.ID, seed.Program.Version)
	if !errors.Is(closeErr, platform.ErrValidation) {
		t.Errorf("program close did not observe leased job: %v", closeErr)
	}

	if _, err := s.store.DB().Exec(s.ctx, `DROP TRIGGER reject_terminal_lease_cleanup ON jobs`); err != nil {
		t.Fatalf("drop cleanup failure trigger: %v", err)
	}
	if err := s.jobs.Fail(s.ctx, jobID, "worker-terminal", errors.New("permanent reconciliation failure"), time.Minute, 1); err != nil {
		t.Fatalf("settle job after database recovery: %v", err)
	}
	job, err = s.jobs.Get(s.ctx, jobID)
	if err != nil {
		t.Fatalf("read settled job: %v", err)
	}
	if job.Status != "dead" || job.OwnerToken != "" || job.LeaseExpiresAt != nil {
		t.Fatalf("terminal job retained lease after recovery: %+v", job)
	}
	if err := s.program.Close(s.as(s.manager), seed.Program.ID, seed.Program.Version); err != nil {
		t.Fatalf("close program after job settled: %v", err)
	}
}
