package integration_test

import (
	"context"
	"errors"
	"testing"
)

func TestG04Task20ProgramCloseCancellation(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	if _, err := s.store.DB().Exec(s.ctx, `UPDATE sites SET status='archived' WHERE id=$1`, seed.Site.ID); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(s.as(s.manager))
	cancel()
	if err := s.program.Close(cancelled, seed.Program.ID, seed.Program.Version); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected close cancellation, got %v", err)
	}

	found, err := s.program.Get(s.as(s.manager), seed.Program.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "active" || found.Version != seed.Program.Version {
		t.Fatalf("cancelled close changed program: %+v", found)
	}
	if err := s.program.Close(s.as(s.manager), seed.Program.ID, seed.Program.Version); err != nil {
		t.Fatalf("close after cancellation: %v", err)
	}
	closed, err := s.program.Get(s.as(s.manager), seed.Program.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" || closed.Version != seed.Program.Version+1 {
		t.Fatalf("unexpected closed program: %+v", closed)
	}
}
