package integration_test

import (
	"context"
	"errors"
	"testing"
)

func TestG04Task26ProgramOverviewCancellation(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	managerCtx := s.as(s.manager)
	canceledCtx, cancel := context.WithCancel(managerCtx)
	cancel()
	if _, err := s.query.ProgramOverview(canceledCtx, seed.Program.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled overview request was not rejected: %v", err)
	}
	overview, err := s.query.ProgramOverview(managerCtx, seed.Program.ID)
	if err != nil {
		t.Fatal(err)
	}
	if overview.ProgramStatus != "active" || overview.SiteStates["approved"] != 1 {
		t.Fatalf("valid overview request did not return the committed snapshot: %+v", overview)
	}
}
