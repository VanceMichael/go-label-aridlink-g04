package integration_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/httpapi"
)

func TestG04Task30ExactIdempotencyReplay(t *testing.T) {
	s := newSuite(t)
	login, err := s.auth.Login(s.ctx, auth.Credentials{Email: "manager@aridlink.test", Password: "correct-horse-battery"})
	if err != nil {
		t.Fatalf("login manager: %v", err)
	}
	router := httpapi.NewRouter(httpapi.Dependencies{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: s.clock, IDs: s.ids,
		Store: s.store, Idempotency: s.idempotency, Auth: s.auth, Programs: s.program, Queries: s.query, Sites: s.site,
		Monitoring: s.monitor, Intervention: s.work, Evidence: s.evidence, Reviews: s.review, Grants: s.grant,
		Alerts: s.alert, Technology: s.technology, Training: s.training})
	body := `{"owner_organization_id":"` + s.ownerOrganization + `","name":"Byte-stable watershed plan","starts_on":"2026-08-23T04:00:00Z","ends_on":"2030-08-23T04:00:00Z","budget_cents":700000}`
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/programs", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+login.Token)
		r.Header.Set("Idempotency-Key", "task30-program-create")
		return r
	}
	first := httptest.NewRecorder()
	router.ServeHTTP(first, request())
	if first.Code != http.StatusCreated {
		t.Fatalf("first program response: status=%d body=%s", first.Code, first.Body.String())
	}
	second := httptest.NewRecorder()
	router.ServeHTTP(second, request())
	if second.Code != http.StatusCreated || second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay response: status=%d replay=%q body=%s", second.Code, second.Header().Get("Idempotency-Replayed"), second.Body.String())
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("idempotency replay changed response bytes: first=%q second=%q", first.Body.String(), second.Body.String())
	}
}
