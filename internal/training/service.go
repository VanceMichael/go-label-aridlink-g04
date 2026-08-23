package training

import (
	"context"
	"errors"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/access"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/audit"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/jackc/pgx/v5"
)

type Cohort struct {
	ID        string    `json:"id"`
	ProgramID string    `json:"program_id"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	Capacity  int       `json:"capacity"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Attendance struct {
	UserID     string     `json:"user_id"`
	Status     string     `json:"status"`
	RecordedAt *time.Time `json:"recorded_at,omitempty"`
}
type Service struct {
	store  *store.Store
	ids    platform.IDGenerator
	clock  platform.Clock
	audit  *audit.Writer
	outbox *outbox.Service
}

func NewService(st *store.Store, ids platform.IDGenerator, clock platform.Clock, writer *audit.Writer, events *outbox.Service) *Service {
	return &Service{store: st, ids: ids, clock: clock, audit: writer, outbox: events}
}

func (s *Service) Schedule(ctx context.Context, programID, title string, capacity int, startsAt, endsAt time.Time) (Cohort, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return Cohort{}, err
	}
	if title == "" || capacity <= 0 || !endsAt.After(startsAt) {
		return Cohort{}, platform.FieldError{Field: "cohort", Message: "title, capacity and valid schedule required"}
	}
	now := s.clock.Now()
	c := Cohort{ID: s.ids.New("coh"), ProgramID: programID, Title: title, Status: "scheduled", Capacity: capacity, StartsAt: startsAt, EndsAt: endsAt, Version: 1, CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID, status string
		if err := tx.QueryRow(ctx, `SELECT owner_organization_id,status FROM programs WHERE id=$1`, programID).Scan(&ownerID, &status); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "active" {
			return platform.StateError{Resource: "program", Current: status, Target: "training"}
		}
		_, err := tx.Exec(ctx, `INSERT INTO training_cohorts(id,program_id,title,capacity,status,starts_at,ends_at,version,created_at,updated_at) VALUES($1,$2,$3,$4,'scheduled',$5,$6,1,$7,$7)`, c.ID, programID, title, capacity, startsAt, endsAt, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "cohort.scheduled", "training_cohort", c.ID, map[string]any{"capacity": capacity})
	})
	return c, err
}

func (s *Service) OpenEnrollment(ctx context.Context, cohortID string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID string
		if err := tx.QueryRow(ctx, `SELECT p.owner_organization_id FROM training_cohorts c JOIN programs p ON p.id=c.program_id WHERE c.id=$1 FOR UPDATE OF c`, cohortID).Scan(&ownerID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE training_cohorts SET status='enrolling',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2 AND status='scheduled'`, cohortID, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		return s.audit.Record(ctx, tx, actor, "cohort.enrollment_opened", "training_cohort", cohortID, map[string]any{"version": expectedVersion + 1})
	})
}

func (s *Service) Start(ctx context.Context, cohortID string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var startsAt, endsAt time.Time
		var registered int
		var ownerID string
		if err := tx.QueryRow(ctx, `SELECT c.starts_at,c.ends_at,(SELECT count(*) FROM training_attendance WHERE cohort_id=$1),p.owner_organization_id FROM training_cohorts c JOIN programs p ON p.id=c.program_id WHERE c.id=$1 AND c.status='enrolling' FOR UPDATE OF c`, cohortID).Scan(&startsAt, &endsAt, &registered, &ownerID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if now.Before(startsAt) || !now.Before(endsAt) {
			return platform.StateError{Resource: "cohort", Current: "outside schedule", Target: "running"}
		}
		if registered == 0 {
			return platform.FieldError{Field: "attendance", Message: "at least one registration required"}
		}
		tag, err := tx.Exec(ctx, `UPDATE training_cohorts SET status='running',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2 AND status='enrolling'`, cohortID, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		return s.audit.Record(ctx, tx, actor, "cohort.started", "training_cohort", cohortID, map[string]any{"registered": registered})
	})
}

func (s *Service) Register(ctx context.Context, cohortID, userID string) error {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager, platform.RoleFieldOfficer, platform.RoleTechnicalReviewer, platform.RoleFinanceReviewer)
	if err != nil {
		return err
	}
	if actor.UserID != userID && actor.Role != platform.RoleProgramManager {
		return platform.ErrForbidden
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var capacity int
		var status, ownerID, programID string
		if err := tx.QueryRow(ctx, `SELECT c.capacity,c.status,p.owner_organization_id,c.program_id FROM training_cohorts c JOIN programs p ON p.id=c.program_id WHERE c.id=$1 FOR UPDATE OF c`, cohortID).Scan(&capacity, &status, &ownerID, &programID); err != nil {
			return store.Translate(err)
		}
		if actor.Role == platform.RoleProgramManager {
			if err := platform.RequireOrganization(actor, ownerID); err != nil {
				return err
			}
		} else if actor.OrganizationID != ownerID {
			var partner bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM partnerships WHERE program_id=$1 AND organization_id=$2 AND status IN ('invited','active'))`, programID, actor.OrganizationID).Scan(&partner); err != nil {
				return err
			}
			if !partner {
				return platform.ErrForbidden
			}
		}
		if status != "enrolling" {
			return platform.StateError{Resource: "cohort", Current: status, Target: "registration"}
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM training_attendance WHERE cohort_id=$1`, cohortID).Scan(&count); err != nil {
			return err
		}
		if count >= capacity {
			return platform.ErrConflict
		}
		_, err := tx.Exec(ctx, `INSERT INTO training_attendance(cohort_id,user_id,status) VALUES($1,$2,'registered') ON CONFLICT(cohort_id,user_id) DO NOTHING`, cohortID, userID)
		return store.Translate(err)
	})
}

func (s *Service) RecordAttendance(ctx context.Context, cohortID string, records []Attendance) error {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return platform.FieldError{Field: "attendance", Message: "records required"}
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, ownerID string
		if err := tx.QueryRow(ctx, `SELECT c.status,p.owner_organization_id FROM training_cohorts c JOIN programs p ON p.id=c.program_id WHERE c.id=$1 FOR UPDATE OF c`, cohortID).Scan(&status, &ownerID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "running" {
			return platform.StateError{Resource: "cohort", Current: status, Target: "attendance"}
		}
		for _, record := range records {
			if record.Status != "attended" && record.Status != "absent" {
				return platform.FieldError{Field: "attendance.status", Message: "attended or absent required"}
			}
			tag, err := tx.Exec(ctx, `UPDATE training_attendance SET status=$3,recorded_at=$4 WHERE cohort_id=$1 AND user_id=$2 AND status='registered'`, cohortID, record.UserID, record.Status, now)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return platform.ErrConflict
			}
		}
		return s.audit.Record(ctx, tx, actor, "cohort.attendance_recorded", "training_cohort", cohortID, map[string]any{"records": len(records)})
	})
}

func (s *Service) Complete(ctx context.Context, cohortID string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, ownerID string
		var pending int
		if err := tx.QueryRow(ctx, `SELECT c.status,p.owner_organization_id FROM training_cohorts c JOIN programs p ON p.id=c.program_id WHERE c.id=$1 FOR UPDATE OF c`, cohortID).Scan(&status, &ownerID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "running" {
			return platform.StateError{Resource: "cohort", Current: status, Target: "completed"}
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM training_attendance WHERE cohort_id=$1 AND status='registered'`, cohortID).Scan(&pending); err != nil {
			return err
		}
		if pending > 0 {
			return platform.FieldError{Field: "attendance", Message: "all attendance records must be resolved"}
		}
		tag, err := tx.Exec(ctx, `UPDATE training_cohorts SET status='completed',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, cohortID, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		if err := s.audit.Record(ctx, tx, actor, "cohort.completed", "training_cohort", cohortID, map[string]any{"completed_at": now}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "cohort.completed", cohortID, map[string]any{"cohort_id": cohortID})
		return err
	})
}

func (s *Service) Get(ctx context.Context, id string) (Cohort, error) {
	var c Cohort
	err := s.store.DB().QueryRow(ctx, `SELECT id,program_id,title,capacity,status,starts_at,ends_at,version,created_at,updated_at FROM training_cohorts WHERE id=$1`, id).Scan(&c.ID, &c.ProgramID, &c.Title, &c.Capacity, &c.Status, &c.StartsAt, &c.EndsAt, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Cohort{}, platform.ErrNotFound
	}
	if err != nil {
		return Cohort{}, err
	}
	if err := access.RequireProgram(ctx, s.store.DB(), c.ProgramID); err != nil {
		return Cohort{}, err
	}
	return c, nil
}
