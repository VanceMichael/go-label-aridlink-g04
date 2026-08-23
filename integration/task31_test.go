package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestG04Task31LogoutCancellation(t *testing.T) {
	s := newSuite(t)
	login, err := s.auth.Login(s.ctx, auth.Credentials{Email: "manager@aridlink.test", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(s.ctx)
	cancel()
	if err := s.auth.Logout(cancelled, login.Token); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected logout cancellation, got %v", err)
	}
	if _, err := s.auth.Authenticate(s.ctx, login.Token); err != nil {
		t.Fatalf("cancelled logout revoked session: %v", err)
	}

	if err := s.auth.Logout(s.ctx, login.Token); err != nil {
		t.Fatalf("logout after cancellation: %v", err)
	}
	if _, err := s.auth.Authenticate(s.ctx, login.Token); !errors.Is(err, platform.ErrUnauthorized) {
		t.Fatalf("expected revoked session, got %v", err)
	}
}
