package worker

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

type Job struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	SubjectID      string          `json:"subject_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Status         string          `json:"status"`
	OwnerToken     string          `json:"owner_token,omitempty"`
	LastError      string          `json:"last_error,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	Attempts       int             `json:"attempts"`
	LeaseExpiresAt *time.Time      `json:"lease_expires_at,omitempty"`
	AvailableAt    time.Time       `json:"available_at"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}
type Jobs struct {
	store *store.Store
	ids   platform.IDGenerator
	clock platform.Clock
}

func NewJobs(st *store.Store, ids platform.IDGenerator, clock platform.Clock) *Jobs {
	return &Jobs{store: st, ids: ids, clock: clock}
}

func (j *Jobs) Enqueue(ctx context.Context, db store.DBTX, kind, subjectID, idempotencyKey string, payload any) (string, error) {
	if kind == "" || subjectID == "" || idempotencyKey == "" {
		return "", platform.FieldError{Field: "job", Message: "kind, subject and idempotency key required"}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	id := j.ids.New("job")
	now := j.clock.Now()
	err = db.QueryRow(ctx, `INSERT INTO jobs(id,kind,subject_id,idempotency_key,payload,status,available_at,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,'pending',$6,$6,$6)
		ON CONFLICT(idempotency_key) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key
		RETURNING id`, id, kind, subjectID, idempotencyKey, encoded, now).Scan(&id)
	if err != nil {
		return "", store.Translate(err)
	}
	return id, nil
}

func (j *Jobs) Claim(ctx context.Context, owner string, lease time.Duration, limit int) ([]Job, error) {
	if owner == "" || lease <= 0 || limit <= 0 {
		return nil, platform.FieldError{Field: "claim", Message: "owner, lease and limit required"}
	}
	now := j.clock.Now()
	claimed := make([]Job, 0, limit)
	err := j.store.WithTxOptions(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM jobs WHERE available_at<=$1 AND (status IN ('pending','retry') OR (status='running' AND lease_expires_at<=$1)) ORDER BY available_at,created_at FOR UPDATE SKIP LOCKED LIMIT $2`, now, limit)
		if err != nil {
			return err
		}
		ids := make([]string, 0)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			var job Job
			err := tx.QueryRow(ctx, `UPDATE jobs SET status='running',owner_token=$2,lease_expires_at=$3,attempts=attempts+1,updated_at=$4 WHERE id=$1 RETURNING id,kind,subject_id,idempotency_key,payload,status,attempts,COALESCE(owner_token,''),lease_expires_at,available_at,COALESCE(last_error,''),created_at,updated_at`, id, owner, now.Add(lease), now).Scan(&job.ID, &job.Kind, &job.SubjectID, &job.IdempotencyKey, &job.Payload, &job.Status, &job.Attempts, &job.OwnerToken, &job.LeaseExpiresAt, &job.AvailableAt, &job.LastError, &job.CreatedAt, &job.UpdatedAt)
			if err != nil {
				return err
			}
			claimed = append(claimed, job)
		}
		return nil
	})
	return claimed, err
}

func (j *Jobs) Renew(ctx context.Context, id, owner string, lease time.Duration) error {
	now := j.clock.Now()
	tag, err := j.store.DB().Exec(ctx, `UPDATE jobs SET lease_expires_at=$4,updated_at=$3 WHERE id=$1 AND status='running' AND owner_token=$2 AND lease_expires_at>$3`, id, owner, now, now.Add(lease))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return platform.ErrLeaseLost
	}
	return nil
}

func (j *Jobs) Succeed(ctx context.Context, id, owner string) error {
	now := j.clock.Now()
	tag, err := j.store.DB().Exec(ctx, `UPDATE jobs SET status='succeeded',owner_token=NULL,lease_expires_at=NULL,updated_at=$3 WHERE id=$1 AND status='running' AND owner_token=$2 AND lease_expires_at>$3`, id, owner, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return platform.ErrLeaseLost
	}
	return nil
}

func (j *Jobs) Fail(ctx context.Context, id, owner string, cause error, retryAfter time.Duration, maxAttempts int) error {
	now := j.clock.Now()
	tag, err := j.store.ExecCommitted(ctx, `UPDATE jobs SET status='dead',updated_at=$5
		WHERE id=$1 AND status='running' AND owner_token=$2 AND lease_expires_at>$3 AND attempts >= $4`,
		id, owner, now, maxAttempts, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		tag, err = j.store.ExecCommitted(ctx, `UPDATE jobs SET owner_token=NULL,lease_expires_at=NULL,
			available_at=$2,last_error=$3,updated_at=$4 WHERE id=$1 AND status='dead'`,
			id, now.Add(retryAfter), cause.Error(), now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrLeaseLost
		}
		return nil
	}
	return j.store.WithTx(ctx, func(tx pgx.Tx) error {
		var attempts int
		err := tx.QueryRow(ctx, `SELECT attempts FROM jobs WHERE id=$1 AND status='running' AND owner_token=$2 AND lease_expires_at>$3 FOR UPDATE`, id, owner, now).Scan(&attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrLeaseLost
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE jobs SET status='retry',owner_token=NULL,lease_expires_at=NULL,available_at=$2,last_error=$3,updated_at=$4 WHERE id=$1`, id, now.Add(retryAfter), cause.Error(), now)
		return err
	})
}

func (j *Jobs) Get(ctx context.Context, id string) (Job, error) {
	var job Job
	err := j.store.DB().QueryRow(ctx, `SELECT id,kind,subject_id,idempotency_key,payload,status,attempts,COALESCE(owner_token,''),lease_expires_at,available_at,COALESCE(last_error,''),created_at,updated_at FROM jobs WHERE id=$1`, id).Scan(&job.ID, &job.Kind, &job.SubjectID, &job.IdempotencyKey, &job.Payload, &job.Status, &job.Attempts, &job.OwnerToken, &job.LeaseExpiresAt, &job.AvailableAt, &job.LastError, &job.CreatedAt, &job.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, platform.ErrNotFound
	}
	if err != nil {
		return Job{}, fmt.Errorf("get job: %w", err)
	}
	return job, nil
}
