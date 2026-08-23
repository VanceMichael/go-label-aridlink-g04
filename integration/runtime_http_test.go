package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/delivery"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/httpapi"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/idempotency"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

func TestOutboxLeaseOwnershipRetryAndDeadLetter(t *testing.T) {
	s := newSuite(t)
	created, err := s.outbox.Enqueue(s.ctx, s.store.DB(), "test.delivery", "aggregate-1", map[string]string{"state": "ready"})
	if err != nil {
		t.Fatal(err)
	}
	eventID := created
	claimed, err := s.outbox.Claim(s.ctx, "worker-a", time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID != eventID {
		t.Fatalf("claim event: %+v err=%v", claimed, err)
	}
	if err := s.outbox.Acknowledge(s.ctx, eventID, "worker-b"); !errors.Is(err, platform.ErrLeaseLost) {
		t.Fatalf("foreign acknowledgement: %v", err)
	}
	if err := s.outbox.Fail(s.ctx, eventID, "worker-a", errors.New("endpoint unavailable"), time.Minute, 2); err != nil {
		t.Fatal(err)
	}
	if events, err := s.outbox.Claim(s.ctx, "worker-a", time.Minute, 1); err != nil || len(events) != 0 {
		t.Fatalf("event claimed before retry window: %+v err=%v", events, err)
	}
	s.clock.Advance(time.Minute)
	claimed, err = s.outbox.Claim(s.ctx, "worker-b", time.Minute, 1)
	if err != nil || len(claimed) != 1 || claimed[0].Attempts != 2 {
		t.Fatalf("reclaim retry: %+v err=%v", claimed, err)
	}
	if err := s.outbox.Fail(s.ctx, eventID, "worker-b", errors.New("permanent failure"), time.Minute, 2); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.store.DB().QueryRow(s.ctx, `SELECT status FROM outbox_events WHERE id=$1`, eventID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "dead" {
		t.Fatalf("event not dead-lettered: %s", status)
	}
}

func TestConcurrentOutboxClaimsDoNotDuplicateDelivery(t *testing.T) {
	s := newSuite(t)
	for i := range 4 {
		if _, err := s.outbox.Enqueue(s.ctx, s.store.DB(), "test.concurrent", "aggregate-"+string(rune('a'+i)), map[string]int{"index": i}); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan []outbox.Event, 2)
	errorsChannel := make(chan error, 2)
	var wg sync.WaitGroup
	for _, owner := range []string{"worker-a", "worker-b"} {
		owner := owner
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			events, err := s.outbox.Claim(s.ctx, owner, time.Minute, 4)
			results <- events
			errorsChannel <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for events := range results {
		for _, event := range events {
			if seen[event.ID] {
				t.Fatalf("event claimed twice: %s", event.ID)
			}
			seen[event.ID] = true
		}
	}
	if len(seen) != 4 {
		t.Fatalf("expected four unique claims, got %d", len(seen))
	}
}

func TestExpiredJobLeaseCanBeReclaimedButOldOwnerCannotFinish(t *testing.T) {
	s := newSuite(t)
	jobID, err := s.jobs.Enqueue(s.ctx, s.store.DB(), "alert.expire", "program-1", "expire-program-1", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.jobs.Claim(s.ctx, "worker-old", time.Minute, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first job claim: %+v err=%v", first, err)
	}
	s.clock.Advance(time.Minute)
	second, err := s.jobs.Claim(s.ctx, "worker-new", time.Minute, 1)
	if err != nil || len(second) != 1 || second[0].ID != jobID {
		t.Fatalf("job reclaim: %+v err=%v", second, err)
	}
	if err := s.jobs.Succeed(s.ctx, jobID, "worker-old"); !errors.Is(err, platform.ErrLeaseLost) {
		t.Fatalf("stale owner completion: %v", err)
	}
	if err := s.jobs.Succeed(s.ctx, jobID, "worker-new"); err != nil {
		t.Fatal(err)
	}
	found, err := s.jobs.Get(s.ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if found.Status != "succeeded" || found.OwnerToken != "" || found.LeaseExpiresAt != nil {
		t.Fatalf("unexpected completed job: %+v", found)
	}
}

func TestJobEnqueueReturnsCanonicalIDForDuplicateKey(t *testing.T) {
	s := newSuite(t)
	first, err := s.jobs.Enqueue(s.ctx, s.store.DB(), "grant.release_expired", "program-1", "release-program-1", map[string]int{"limit": 10})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.jobs.Enqueue(s.ctx, s.store.DB(), "grant.release_expired", "program-1", "release-program-1", map[string]int{"limit": 10})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("duplicate enqueue returned phantom id: first=%s second=%s", first, second)
	}
	if s.count("jobs") != 1 {
		t.Fatalf("duplicate job was inserted")
	}
}

func TestIdempotencyReplayConflictScopeAndFailureRecovery(t *testing.T) {
	s := newSuite(t)
	key := idempotency.Key{OrganizationID: s.ownerOrganization, Method: http.MethodPost, Path: "/v1/programs", RequestKey: "request-42"}
	body := []byte(`{"name":"Dryland Plan"}`)
	claim, err := s.idempotency.Begin(s.ctx, key, idempotency.HashRequest(body))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.idempotency.Begin(s.ctx, key, idempotency.HashRequest(body)); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("processing duplicate: %v", err)
	}
	result := idempotency.Result{Status: http.StatusCreated, Body: json.RawMessage(`{"id":"program-1"}`)}
	if err := s.idempotency.Complete(s.ctx, claim, result); err != nil {
		t.Fatal(err)
	}
	replay, err := s.idempotency.Begin(s.ctx, key, idempotency.HashRequest(body))
	if err != nil || replay.Replay == nil || replay.Replay.Status != http.StatusCreated || !bytes.Equal(replay.Replay.Body, result.Body) {
		t.Fatalf("unexpected replay: %+v err=%v", replay, err)
	}
	if _, err := s.idempotency.Begin(s.ctx, key, idempotency.HashRequest([]byte(`{"name":"Changed"}`))); !errors.Is(err, platform.ErrConflict) {
		t.Fatalf("changed payload reused key: %v", err)
	}
	otherPath := key
	otherPath.Path = "/v1/sites"
	otherClaim, err := s.idempotency.Begin(s.ctx, otherPath, idempotency.HashRequest(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.idempotency.Fail(s.ctx, otherClaim, errors.New("temporary")); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.idempotency.Begin(s.ctx, otherPath, idempotency.HashRequest(body))
	if err != nil || recovered.Replay != nil || recovered.ID != otherClaim.ID {
		t.Fatalf("failed claim not recovered: %+v err=%v", recovered, err)
	}
}

func TestHTTPLoginIdempotentMutationLogoutAndErrorMapping(t *testing.T) {
	s := newSuite(t)
	seed := s.seedProgram()
	router := httpapi.NewRouter(httpapi.Dependencies{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Clock: s.clock, IDs: s.ids,
		Store: s.store, Idempotency: s.idempotency, Auth: s.auth, Programs: s.program, Queries: s.query, Sites: s.site,
		Monitoring: s.monitor, Intervention: s.work, Evidence: s.evidence, Reviews: s.review, Grants: s.grant,
		Alerts: s.alert, Technology: s.technology, Training: s.training})
	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/programs/"+seed.Program.ID, nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status: %d", unauthorized.Code)
	}
	loginBody := `{"email":"manager@aridlink.test","password":"correct-horse-battery"}`
	loginResponse := httptest.NewRecorder()
	router.ServeHTTP(loginResponse, httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(loginBody)))
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", loginResponse.Code, loginResponse.Body.String())
	}
	var login auth.LoginResult
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	alertBody := `{"program_id":"` + seed.Program.ID + `","kind":"drought","severity":"warning","starts_at":"2026-08-23T04:00:00Z","ends_at":"2026-08-24T04:00:00Z"}`
	first := authenticatedRequest(http.MethodPost, "/v1/alerts", alertBody, login.Token, "alert-request-1")
	firstResponse := httptest.NewRecorder()
	router.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusCreated {
		t.Fatalf("create alert status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	secondResponse := httptest.NewRecorder()
	router.ServeHTTP(secondResponse, authenticatedRequest(http.MethodPost, "/v1/alerts", alertBody, login.Token, "alert-request-1"))
	if secondResponse.Code != http.StatusCreated || secondResponse.Header().Get("Idempotency-Replayed") != "true" || secondResponse.Body.String() != firstResponse.Body.String() {
		t.Fatalf("idempotency replay mismatch: first=%d/%s second=%d/%s replay=%q", firstResponse.Code, firstResponse.Body.String(), secondResponse.Code, secondResponse.Body.String(), secondResponse.Header().Get("Idempotency-Replayed"))
	}
	badResponse := httptest.NewRecorder()
	router.ServeHTTP(badResponse, authenticatedRequest(http.MethodPost, "/v1/alerts", `{"unknown":true}`, login.Token, ""))
	if badResponse.Code != http.StatusBadRequest || !strings.Contains(badResponse.Body.String(), "validation_failed") {
		t.Fatalf("validation mapping status=%d body=%s", badResponse.Code, badResponse.Body.String())
	}
	logoutResponse := httptest.NewRecorder()
	router.ServeHTTP(logoutResponse, authenticatedRequest(http.MethodPost, "/v1/auth/logout", "", login.Token, "logout-request-1"))
	if logoutResponse.Code != http.StatusNoContent {
		t.Fatalf("logout status=%d body=%s", logoutResponse.Code, logoutResponse.Body.String())
	}
	var logoutClaims int
	if err := s.store.DB().QueryRow(s.ctx, `SELECT count(*) FROM idempotency_records WHERE path='/v1/auth/logout'`).Scan(&logoutClaims); err != nil {
		t.Fatal(err)
	}
	if logoutClaims != 0 {
		t.Fatalf("logout left an unreplayable idempotency record: %d", logoutClaims)
	}
	afterLogout := httptest.NewRecorder()
	router.ServeHTTP(afterLogout, authenticatedRequest(http.MethodGet, "/v1/programs/"+seed.Program.ID, "", login.Token, ""))
	if afterLogout.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session still accepted: %d", afterLogout.Code)
	}
}

func TestWebhookPropagatesContextHeadersAndClosesResponse(t *testing.T) {
	var closed atomic.Bool
	var received *http.Request
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		received = request
		return &http.Response{StatusCode: http.StatusAccepted, Header: make(http.Header), Body: &trackingBody{Reader: strings.NewReader(`{"accepted":true}`), closed: &closed}, Request: request}, nil
	})
	sender, err := delivery.NewWebhookSender(&http.Client{Transport: transport}, "https://hooks.aridlink.test/base", "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	event := outbox.Event{ID: "evt-42", Topic: "evidence.sealed", AggregateID: "bundle-9", Payload: json.RawMessage(`{"bundle_id":"bundle-9"}`)}
	if err := sender.Send(ctx, event); err != nil {
		t.Fatal(err)
	}
	if received == nil || received.Context() != ctx {
		t.Fatal("request did not retain caller context")
	}
	if received.URL.Path != "/base/events/evidence.sealed" || received.Header.Get("Idempotency-Key") != event.ID || received.Header.Get("X-AridLink-Aggregate") != event.AggregateID || received.Header.Get("Authorization") != "Bearer secret-token" {
		t.Fatalf("unexpected webhook request: url=%s headers=%v", received.URL, received.Header)
	}
	if !closed.Load() {
		t.Fatal("webhook response body was not closed")
	}
}

func TestWebhookCancellationAndFailureBodyAreReturned(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		select {
		case <-request.Context().Done():
			return nil, request.Context().Err()
		default:
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("downstream maintenance")), Request: request}, nil
		}
	})
	sender, err := delivery.NewWebhookSender(&http.Client{Transport: transport}, "https://hooks.aridlink.test", "")
	if err != nil {
		t.Fatal(err)
	}
	event := outbox.Event{ID: "evt-9", Topic: "alert.published", AggregateID: "alert-2", Payload: json.RawMessage(`{}`)}
	if err := sender.Send(context.Background(), event); err == nil || !strings.Contains(err.Error(), "503") || !strings.Contains(err.Error(), "downstream maintenance") {
		t.Fatalf("unexpected webhook failure: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sender.Send(cancelled, event); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation not propagated: %v", err)
	}
}

func authenticatedRequest(method, path, body, token, requestKey string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	if requestKey != "" {
		request.Header.Set("Idempotency-Key", requestKey)
	}
	return request
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

type trackingBody struct {
	io.Reader
	closed *atomic.Bool
}

func (body *trackingBody) Close() error {
	body.closed.Store(true)
	return nil
}
