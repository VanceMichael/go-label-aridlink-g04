package program

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/access"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/audit"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/jackc/pgx/v5"
)

type Program struct {
	ID                  string    `json:"id"`
	OwnerOrganizationID string    `json:"owner_organization_id"`
	Name                string    `json:"name"`
	StartsOn            time.Time `json:"starts_on"`
	EndsOn              time.Time `json:"ends_on"`
	Status              string    `json:"status"`
	BudgetCents         int64     `json:"budget_cents"`
	Version             int64     `json:"version"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type Partnership struct {
	ID             string    `json:"id"`
	ProgramID      string    `json:"program_id"`
	OrganizationID string    `json:"organization_id"`
	Role           string    `json:"role"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CreateInput struct {
	OwnerOrganizationID string    `json:"owner_organization_id"`
	Name                string    `json:"name"`
	StartsOn            time.Time `json:"starts_on"`
	EndsOn              time.Time `json:"ends_on"`
	BudgetCents         int64     `json:"budget_cents"`
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

func (s *Service) Create(ctx context.Context, input CreateInput) (Program, error) {
	actor, err := platform.RequireRole(ctx, platform.RolePlatformAdmin, platform.RoleProgramManager)
	if err != nil {
		return Program{}, err
	}
	if err := platform.RequireOrganization(actor, input.OwnerOrganizationID); err != nil {
		return Program{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || input.BudgetCents <= 0 || !input.EndsOn.After(input.StartsOn) {
		return Program{}, platform.FieldError{Field: "program", Message: "name, positive budget and valid dates are required"}
	}
	now := s.clock.Now()
	program := Program{ID: s.ids.New("prg"), OwnerOrganizationID: input.OwnerOrganizationID, Name: input.Name,
		StartsOn: dateOnly(input.StartsOn), EndsOn: dateOnly(input.EndsOn), Status: "draft", BudgetCents: input.BudgetCents,
		Version: 1, CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `INSERT INTO programs(id,owner_organization_id,name,starts_on,ends_on,status,budget_cents,version,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,'draft',$6,1,$7,$7)`, program.ID, program.OwnerOrganizationID, program.Name,
			program.StartsOn, program.EndsOn, program.BudgetCents, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "program.created", "program", program.ID, map[string]any{"budget_cents": input.BudgetCents})
	})
	return program, err
}

func (s *Service) AddPartnership(ctx context.Context, programID, organizationID, role string) (Partnership, error) {
	actor, err := platform.RequireRole(ctx, platform.RolePlatformAdmin, platform.RoleProgramManager)
	if err != nil {
		return Partnership{}, err
	}
	allowed := map[string]bool{"coordinator": true, "research": true, "implementation": true, "funding": true, "observer": true}
	if !allowed[role] || organizationID == "" {
		return Partnership{}, platform.FieldError{Field: "partnership", Message: "organization and valid role required"}
	}
	now := s.clock.Now()
	partnership := Partnership{ID: s.ids.New("par"), ProgramID: programID, OrganizationID: organizationID, Role: role,
		Status: "invited", CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID, status string
		if err := tx.QueryRow(ctx, `SELECT owner_organization_id,status FROM programs WHERE id=$1 FOR UPDATE`, programID).Scan(&ownerID, &status); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "draft" && status != "active" {
			return platform.StateError{Resource: "program", Current: status, Target: "partnership invitation"}
		}
		_, err := tx.Exec(ctx, `INSERT INTO partnerships(id,program_id,organization_id,role,status,created_at,updated_at)
			VALUES($1,$2,$3,$4,'invited',$5,$5)`, partnership.ID, programID, organizationID, role, now)
		if err != nil {
			return store.Translate(err)
		}
		if err := s.audit.Record(ctx, tx, actor, "partnership.invited", "program", programID,
			map[string]any{"organization_id": organizationID, "role": role}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "partnership.invited", partnership.ID, partnership)
		return err
	})
	return partnership, err
}

func (s *Service) Activate(ctx context.Context, programID string, expectedVersion int64) (Program, error) {
	actor, err := platform.RequireRole(ctx, platform.RolePlatformAdmin, platform.RoleProgramManager)
	if err != nil {
		return Program{}, err
	}
	now := s.clock.Now()
	var updated Program
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID, status string
		var activePartners int
		if err := tx.QueryRow(ctx, `SELECT owner_organization_id,status FROM programs WHERE id=$1 FOR UPDATE`, programID).Scan(&ownerID, &status); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "draft" {
			return platform.StateError{Resource: "program", Current: status, Target: "active"}
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM partnerships WHERE program_id=$1 AND status IN ('invited','active')`, programID).Scan(&activePartners); err != nil {
			return err
		}
		if activePartners == 0 {
			return platform.FieldError{Field: "partnerships", Message: "at least one partner is required before activation"}
		}
		err := tx.QueryRow(ctx, `UPDATE programs SET status='active',version=version+1,updated_at=$3
			WHERE id=$1 AND version=$2 RETURNING id,owner_organization_id,name,starts_on,ends_on,status,budget_cents,version,created_at,updated_at`,
			programID, expectedVersion, now).Scan(&updated.ID, &updated.OwnerOrganizationID, &updated.Name, &updated.StartsOn,
			&updated.EndsOn, &updated.Status, &updated.BudgetCents, &updated.Version, &updated.CreatedAt, &updated.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrConflict
		}
		if err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "program.activated", "program", programID, map[string]any{"version": updated.Version}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "program.activated", programID, map[string]any{"program_id": programID})
		return err
	})
	return updated, err
}

func (s *Service) Close(ctx context.Context, programID string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RolePlatformAdmin, platform.RoleProgramManager)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID, status string
		if err := tx.QueryRow(ctx, `SELECT owner_organization_id,status FROM programs WHERE id=$1 FOR UPDATE`, programID).Scan(&ownerID, &status); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "active" && status != "suspended" {
			return platform.StateError{Resource: "program", Current: status, Target: "closed"}
		}
		var blockers int
		query := `SELECT
			(SELECT count(*) FROM sites WHERE program_id=$1 AND status<>'archived') +
			(SELECT count(*) FROM reviews r JOIN evidence_bundles b ON b.id=r.bundle_id JOIN sites s ON s.id=b.site_id
			 WHERE s.program_id=$1 AND r.status IN ('assigned','in_progress')) +
			(SELECT count(*) FROM jobs WHERE subject_id=$1 AND status IN ('pending','running','retry'))`
		if err := tx.QueryRow(ctx, query, programID).Scan(&blockers); err != nil {
			return err
		}
		if blockers > 0 {
			return platform.FieldError{Field: "program", Message: "sites, reviews or jobs are still active"}
		}
		tag, err := tx.Exec(ctx, `UPDATE programs SET status='closed',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, programID, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		return s.audit.Record(ctx, tx, actor, "program.closed", "program", programID, map[string]any{"closed_at": now})
	})
}

func (s *Service) Get(ctx context.Context, id string) (Program, error) {
	if err := access.RequireProgram(ctx, s.store.DB(), id); err != nil {
		return Program{}, err
	}
	var program Program
	err := s.store.DB().QueryRow(ctx, `SELECT id,owner_organization_id,name,starts_on,ends_on,status,budget_cents,version,created_at,updated_at
		FROM programs WHERE id=$1`, id).Scan(&program.ID, &program.OwnerOrganizationID, &program.Name, &program.StartsOn,
		&program.EndsOn, &program.Status, &program.BudgetCents, &program.Version, &program.CreatedAt, &program.UpdatedAt)
	return program, store.Translate(err)
}

func dateOnly(value time.Time) time.Time {
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}
