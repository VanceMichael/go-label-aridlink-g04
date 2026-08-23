package integration_test

import (
	"errors"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestG04Task25AlertAcknowledgementTenant(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	created, err := s.alert.Create(s.as(s.manager), seed.Program.ID, "drought", "warning", s.clock.Now(), s.clock.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("create alert: %v", err)
	}
	if err := s.alert.Publish(s.as(s.manager), created.ID, []string{seed.Site.ID}, created.Version); err != nil {
		t.Fatalf("publish alert: %v", err)
	}
	if err := s.alert.Acknowledge(s.as(s.manager), created.ID); !errors.Is(err, platform.ErrForbidden) {
		t.Errorf("unaffected program owner acknowledged alert: %v", err)
	}
	if err := s.alert.Acknowledge(s.as(s.field), created.ID); err != nil {
		t.Fatalf("affected site organization acknowledgement: %v", err)
	}
	var acknowledgements int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM alert_acknowledgements WHERE alert_id=$1`, created.ID).Scan(&acknowledgements); err != nil {
		t.Fatalf("inspect alert acknowledgements: %v", err)
	}
	if acknowledgements != 1 {
		t.Errorf("unaffected organization left an acknowledgement: %d", acknowledgements)
	}
}
