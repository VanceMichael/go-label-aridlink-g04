package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/jackc/pgx/v5"
)

type Event struct {
	ID             string          `json:"id"`
	Topic          string          `json:"topic"`
	AggregateID    string          `json:"aggregate_id"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
	Attempts       int             `json:"attempts"`
	OwnerToken     string          `json:"owner_token,omitempty"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	AvailableAt    time.Time       `json:"available_at"`
}

type Service struct {
	store *store.Store
	ids   platform.IDGenerator
	clock platform.Clock
}

func NewService(st *store.Store, ids platform.IDGenerator, clock platform.Clock) *Service {
	return &Service{store: st, ids: ids, clock: clock}
}

func (s *Service) Enqueue(ctx context.Context, db store.DBTX, topic, aggregateID string, payload any) (string, error) {
	if topic == "" || aggregateID == "" {
		return "", platform.FieldError{Field: "topic", Message: "topic and aggregate id are required"}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal outbox payload: %w", err)
	}
	id := s.ids.New("evt")
	now := s.clock.Now()
	_, err = db.Exec(ctx, `INSERT INTO outbox_events
		(id, topic, aggregate_id, payload, status, available_at, created_at)
		VALUES($1,$2,$3,$4,'pending',$5,$5)`, id, topic, aggregateID, encoded, now)
	if err != nil {
		return "", fmt.Errorf("enqueue event: %w", store.Translate(err))
	}
	return id, nil
}

func (s *Service) Claim(ctx context.Context, owner string, lease time.Duration, limit int) ([]Event, error) {
	if owner == "" || lease <= 0 || limit <= 0 {
		return nil, platform.FieldError{Field: "claim", Message: "owner, lease and limit are required"}
	}
	now := s.clock.Now()
	events := make([]Event, 0, limit)
	err := s.store.WithTxOptions(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM outbox_events
			WHERE available_at <= $1 AND (status IN ('pending','retry') OR (status='leased' AND lease_expires_at <= $1))
			ORDER BY available_at, created_at FOR UPDATE SKIP LOCKED LIMIT $2`, now, limit)
		if err != nil {
			return fmt.Errorf("select outbox claims: %w", err)
		}
		ids := make([]string, 0, limit)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, id := range ids {
			var event Event
			err := tx.QueryRow(ctx, `UPDATE outbox_events SET status='leased', owner_token=$2,
				lease_expires_at=$3, attempts=attempts+1 WHERE id=$1
				RETURNING id, topic, aggregate_id, payload, status, attempts, owner_token, lease_expires_at, available_at`,
				id, owner, now.Add(lease)).Scan(&event.ID, &event.Topic, &event.AggregateID, &event.Payload,
				&event.Status, &event.Attempts, &event.OwnerToken, &event.LeaseExpiresAt, &event.AvailableAt)
			if err != nil {
				return fmt.Errorf("claim outbox event: %w", err)
			}
			events = append(events, event)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Service) Acknowledge(ctx context.Context, eventID, owner string) error {
	now := s.clock.Now()
	tag, err := s.store.DB().Exec(ctx, `UPDATE outbox_events SET status='delivered', delivered_at=$3,
		owner_token=NULL, lease_expires_at=NULL WHERE id=$1 AND status='leased' AND owner_token=$2 AND lease_expires_at>$3`,
		eventID, owner, now)
	if err != nil {
		return fmt.Errorf("acknowledge event: %w", store.Translate(err))
	}
	if tag.RowsAffected() != 1 {
		return platform.ErrLeaseLost
	}
	return nil
}

func (s *Service) Fail(ctx context.Context, eventID, owner string, cause error, retryAfter time.Duration, maxAttempts int) error {
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var attempts int
		err := tx.QueryRow(ctx, `SELECT attempts FROM outbox_events WHERE id=$1 AND status='leased'
			AND owner_token=$2 AND lease_expires_at>$3 FOR UPDATE`, eventID, owner, now).Scan(&attempts)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return platform.ErrLeaseLost
			}
			return err
		}
		status := "retry"
		if attempts >= maxAttempts {
			status = "dead"
		}
		_, err = tx.Exec(ctx, `UPDATE outbox_events SET status=$2, available_at=$3,
			owner_token=NULL, lease_expires_at=NULL, last_error=$4 WHERE id=$1`,
			eventID, status, now.Add(retryAfter), cause.Error())
		return err
	})
}
