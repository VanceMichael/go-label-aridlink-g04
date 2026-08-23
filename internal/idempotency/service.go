package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/jackc/pgx/v5"
)

type Key struct {
	OrganizationID string
	Method         string
	Path           string
	RequestKey     string
}

type Result struct {
	Status int
	Body   json.RawMessage
}

type Claim struct {
	ID        string
	Key       Key
	Hash      string
	ExpiresAt time.Time
	Replay    *Result
}

type Service struct {
	store *store.Store
	ids   platform.IDGenerator
	clock platform.Clock
	ttl   time.Duration
}

func NewService(st *store.Store, ids platform.IDGenerator, clock platform.Clock, ttl time.Duration) *Service {
	return &Service{store: st, ids: ids, clock: clock, ttl: ttl}
}

func HashRequest(body []byte) string {
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:])
}

func (s *Service) Begin(ctx context.Context, key Key, requestHash string) (Claim, error) {
	if key.OrganizationID == "" || key.Method == "" || key.Path == "" || key.RequestKey == "" || requestHash == "" {
		return Claim{}, platform.FieldError{Field: "idempotency", Message: "organization, method, path, key and request hash are required"}
	}
	now := s.clock.Now()
	claim := Claim{ID: s.ids.New("ide"), Key: key, Hash: requestHash, ExpiresAt: now.Add(s.ttl)}
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var existingID, existingHash, state string
		var status *int
		var body []byte
		var expiresAt time.Time
		err := tx.QueryRow(ctx, `SELECT id,request_hash,state,response_status,response_body,expires_at
			FROM idempotency_records WHERE organization_id=$1 AND method=$2 AND path=$3 AND request_key=$4 FOR UPDATE`,
			key.OrganizationID, key.Method, key.Path, key.RequestKey).Scan(&existingID, &existingHash, &state, &status, &body, &expiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			_, err = tx.Exec(ctx, `INSERT INTO idempotency_records
				(id,organization_id,method,path,request_key,request_hash,state,expires_at,created_at,updated_at)
				VALUES($1,$2,$3,$4,$5,$6,'processing',$7,$8,$8)`, claim.ID, key.OrganizationID, key.Method,
				key.Path, key.RequestKey, requestHash, claim.ExpiresAt, now)
			return store.Translate(err)
		}
		if err != nil {
			return err
		}
		if expiresAt.Before(now) && state != "processing" {
			_, err = tx.Exec(ctx, `UPDATE idempotency_records SET id=$2,request_hash=$3,state='processing',
				response_status=NULL,response_body=NULL,expires_at=$4,updated_at=$5 WHERE id=$1`,
				existingID, claim.ID, requestHash, claim.ExpiresAt, now)
			return err
		}
		if existingHash != requestHash {
			return platform.ConflictError{Resource: "idempotency key", Key: key.RequestKey}
		}
		claim.ID = existingID
		claim.ExpiresAt = expiresAt
		switch state {
		case "processing":
			return fmt.Errorf("%w: request is processing or requires operator recovery", platform.ErrConflict)
		case "completed":
			if status == nil {
				return fmt.Errorf("completed idempotency record has no response")
			}
			claim.Replay = &Result{Status: *status, Body: append(json.RawMessage(nil), body...)}
			return nil
		case "failed":
			_, err = tx.Exec(ctx, `UPDATE idempotency_records SET state='processing',response_status=NULL,
				response_body=NULL,updated_at=$2 WHERE id=$1`, existingID, now)
			return err
		default:
			return fmt.Errorf("unknown idempotency state %q", state)
		}
	})
	return claim, err
}

func (s *Service) Complete(ctx context.Context, claim Claim, result Result) error {
	if claim.Replay != nil {
		return platform.ErrConflict
	}
	if result.Status < 200 || result.Status > 599 || !json.Valid(result.Body) {
		return platform.FieldError{Field: "response", Message: "valid HTTP status and JSON response are required"}
	}
	now := s.clock.Now()
	tag, err := s.store.DB().Exec(ctx, `UPDATE idempotency_records SET state='completed',response_status=$2,
		response_body=$3,updated_at=$4 WHERE id=$1 AND request_hash=$5 AND state='processing'`,
		claim.ID, result.Status, result.Body, now, claim.Hash)
	if err != nil {
		return fmt.Errorf("complete idempotency record: %w", store.Translate(err))
	}
	if tag.RowsAffected() != 1 {
		return platform.ErrConflict
	}
	return nil
}

func (s *Service) Fail(ctx context.Context, claim Claim, cause error) error {
	if claim.Replay != nil {
		return nil
	}
	now := s.clock.Now()
	tag, err := s.store.DB().Exec(ctx, `UPDATE idempotency_records SET state='failed',updated_at=$2
		WHERE id=$1 AND request_hash=$3 AND state='processing'`, claim.ID, now, claim.Hash)
	if err != nil {
		return fmt.Errorf("fail idempotency record after %v: %w", cause, err)
	}
	if tag.RowsAffected() != 1 {
		return platform.ErrConflict
	}
	return nil
}
