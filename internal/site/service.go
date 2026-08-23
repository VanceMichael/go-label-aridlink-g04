package site

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/access"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/audit"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/jackc/pgx/v5"
)

type Site struct {
	ID             string    `json:"id"`
	ProgramID      string    `json:"program_id"`
	OrganizationID string    `json:"organization_id"`
	Name           string    `json:"name"`
	CountryCode    string    `json:"country_code"`
	AreaHectares   float64   `json:"area_hectares"`
	Ecosystem      string    `json:"ecosystem"`
	Status         string    `json:"status"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CreateInput struct {
	ProgramID      string  `json:"program_id"`
	OrganizationID string  `json:"organization_id"`
	Name           string  `json:"name"`
	CountryCode    string  `json:"country_code"`
	AreaHectares   float64 `json:"area_hectares"`
	Ecosystem      string  `json:"ecosystem"`
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

func (s *Service) Create(ctx context.Context, input CreateInput) (Site, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return Site{}, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.CountryCode = strings.ToUpper(strings.TrimSpace(input.CountryCode))
	allowed := map[string]bool{"dryland": true, "grassland": true, "wetland": true, "forest_edge": true, "oasis": true}
	if input.Name == "" || input.ProgramID == "" || input.OrganizationID == "" || input.AreaHectares <= 0 || !allowed[input.Ecosystem] {
		return Site{}, platform.FieldError{Field: "site", Message: "program, organization, name, area and ecosystem are required"}
	}
	now := s.clock.Now()
	created := Site{ID: s.ids.New("sit"), ProgramID: input.ProgramID, OrganizationID: input.OrganizationID,
		Name: input.Name, CountryCode: input.CountryCode, AreaHectares: input.AreaHectares, Ecosystem: input.Ecosystem,
		Status: "proposed", Version: 1, CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID, status string
		if err := tx.QueryRow(ctx, `SELECT owner_organization_id,status FROM programs WHERE id=$1 FOR SHARE`, input.ProgramID).Scan(&ownerID, &status); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "active" {
			return platform.StateError{Resource: "program", Current: status, Target: "site creation"}
		}
		var partner bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM partnerships WHERE program_id=$1 AND organization_id=$2 AND status IN ('invited','active'))`,
			input.ProgramID, input.OrganizationID).Scan(&partner); err != nil {
			return err
		}
		if !partner {
			return platform.ErrForbidden
		}
		insertDB := s.store.DB()
		insertSQL := `INSERT INTO sites(id,program_id,organization_id,name,country_code,area_hectares,ecosystem,status,version,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,'proposed',1,$8,$8)`
		_, err := insertDB.Exec(ctx, insertSQL, created.ID, created.ProgramID, created.OrganizationID,
			created.Name, created.CountryCode, created.AreaHectares, created.Ecosystem, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "site.proposed", "site", created.ID, map[string]any{"program_id": created.ProgramID})
	})
	return created, err
}

func (s *Service) Approve(ctx context.Context, siteID string, expectedVersion int64) (Site, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return Site{}, err
	}
	now := s.clock.Now()
	var approved Site
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID, status string
		err := tx.QueryRow(ctx, `SELECT p.owner_organization_id,s.status FROM sites s JOIN programs p ON p.id=s.program_id WHERE s.id=$1 FOR UPDATE OF s`, siteID).Scan(&ownerID, &status)
		if err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "proposed" {
			return platform.StateError{Resource: "site", Current: status, Target: "approved"}
		}
		err = tx.QueryRow(ctx, `UPDATE sites SET status='approved',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2
			RETURNING id,program_id,organization_id,name,country_code,area_hectares,ecosystem,status,version,created_at,updated_at`, siteID, expectedVersion, now).
			Scan(&approved.ID, &approved.ProgramID, &approved.OrganizationID, &approved.Name, &approved.CountryCode,
				&approved.AreaHectares, &approved.Ecosystem, &approved.Status, &approved.Version, &approved.CreatedAt, &approved.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.ErrConflict
		}
		if err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "site.approved", "site", siteID, map[string]any{"version": approved.Version}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "site.approved", siteID, map[string]any{"site_id": siteID})
		return err
	})
	return approved, err
}

func (s *Service) Archive(ctx context.Context, siteID string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, ownerID string
		err := tx.QueryRow(ctx, `SELECT s.status,p.owner_organization_id FROM sites s JOIN programs p ON p.id=s.program_id WHERE s.id=$1 FOR UPDATE OF s`, siteID).Scan(&status, &ownerID)
		if err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "restored" && status != "approved" {
			return platform.StateError{Resource: "site", Current: status, Target: "archived"}
		}
		var blockers int
		query := `SELECT
			(SELECT count(*) FROM work_orders WHERE site_id=$1 AND status IN ('claimed','running')) +
			(SELECT count(*) FROM evidence_bundles WHERE site_id=$1 AND status IN ('draft','sealed','in_review')) +
			(SELECT count(*) FROM budget_reservations r JOIN grant_milestones m ON m.id=r.milestone_id WHERE m.site_id=$1 AND r.status='held')`
		if err := tx.QueryRow(ctx, query, siteID).Scan(&blockers); err != nil {
			return err
		}
		if blockers > 0 {
			return platform.FieldError{Field: "site", Message: "active work, evidence or budget reservations prevent archival"}
		}
		tag, err := tx.Exec(ctx, `UPDATE sites SET status='archived',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, siteID, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		return s.audit.Record(ctx, tx, actor, "site.archived", "site", siteID, map[string]any{"archived_at": now})
	})
}

func (s *Service) Get(ctx context.Context, siteID string) (Site, error) {
	if err := access.RequireSite(ctx, s.store.DB(), siteID); err != nil {
		return Site{}, err
	}
	var found Site
	err := s.store.DB().QueryRow(ctx, `SELECT id,program_id,organization_id,name,country_code,area_hectares,ecosystem,status,version,created_at,updated_at
		FROM sites WHERE id=$1`, siteID).Scan(&found.ID, &found.ProgramID, &found.OrganizationID, &found.Name, &found.CountryCode,
		&found.AreaHectares, &found.Ecosystem, &found.Status, &found.Version, &found.CreatedAt, &found.UpdatedAt)
	return found, store.Translate(err)
}

func (s *Service) ListByProgram(ctx context.Context, programID string) ([]Site, error) {
	if err := access.RequireProgram(ctx, s.store.DB(), programID); err != nil {
		return nil, err
	}
	rows, err := s.store.DB().Query(ctx, `SELECT id,program_id,organization_id,name,country_code,area_hectares,ecosystem,status,version,created_at,updated_at
		FROM sites WHERE program_id=$1 ORDER BY created_at,id`, programID)
	if err != nil {
		return nil, fmt.Errorf("list sites: %w", err)
	}
	defer rows.Close()
	result := make([]Site, 0)
	for rows.Next() {
		var found Site
		if err := rows.Scan(&found.ID, &found.ProgramID, &found.OrganizationID, &found.Name, &found.CountryCode,
			&found.AreaHectares, &found.Ecosystem, &found.Status, &found.Version, &found.CreatedAt, &found.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, found)
	}
	return result, rows.Err()
}
