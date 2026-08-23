package technology

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

type Transfer struct {
	ID                string    `json:"id"`
	ProgramID         string    `json:"program_id"`
	SiteID            string    `json:"site_id"`
	Title             string    `json:"title"`
	TechnologyVersion string    `json:"technology_version"`
	Status            string    `json:"status"`
	Version           int64     `json:"version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
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

func (s *Service) Propose(ctx context.Context, programID, siteID, title, techVersion string) (Transfer, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return Transfer{}, err
	}
	if title == "" || techVersion == "" {
		return Transfer{}, platform.FieldError{Field: "technology", Message: "title and immutable technology version required"}
	}
	now := s.clock.Now()
	t := Transfer{ID: s.ids.New("tec"), ProgramID: programID, SiteID: siteID, Title: title, TechnologyVersion: techVersion, Status: "proposed", Version: 1, CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID, linkedProgram string
		if err := tx.QueryRow(ctx, `SELECT p.owner_organization_id,s.program_id FROM programs p JOIN sites s ON s.id=$2 WHERE p.id=$1`, programID, siteID).Scan(&ownerID, &linkedProgram); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if linkedProgram != programID {
			return platform.ErrConflict
		}
		_, err := tx.Exec(ctx, `INSERT INTO technology_transfers(id,program_id,site_id,title,technology_version,status,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'proposed',1,$6,$6)`, t.ID, programID, siteID, title, techVersion, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "technology.proposed", "technology_transfer", t.ID, map[string]any{"site_id": siteID, "technology_version": techVersion})
	})
	return t, err
}

func (s *Service) Approve(ctx context.Context, id string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleTechnicalReviewer)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, ownerID string
		if err := tx.QueryRow(ctx, `SELECT t.status,p.owner_organization_id FROM technology_transfers t JOIN programs p ON p.id=t.program_id WHERE t.id=$1 FOR UPDATE OF t`, id).Scan(&status, &ownerID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "proposed" {
			return platform.StateError{Resource: "technology_transfer", Current: status, Target: "approved"}
		}
		tag, err := tx.Exec(ctx, `UPDATE technology_transfers SET status='approved',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, id, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		if err := s.audit.Record(ctx, tx, actor, "technology.approved", "technology_transfer", id, map[string]any{"version": expectedVersion + 1}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "technology.approved", id, map[string]any{"transfer_id": id})
		return err
	})
}

func (s *Service) Deploy(ctx context.Context, id string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleFieldOfficer)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, siteID, organizationID string
		if err := tx.QueryRow(ctx, `SELECT t.status,t.site_id,s.organization_id FROM technology_transfers t JOIN sites s ON s.id=t.site_id WHERE t.id=$1 FOR UPDATE OF t`, id).Scan(&status, &siteID, &organizationID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, organizationID); err != nil {
			return err
		}
		if status != "approved" {
			return platform.StateError{Resource: "technology_transfer", Current: status, Target: "deployed"}
		}
		tag, err := tx.Exec(ctx, `UPDATE technology_transfers SET status='deployed',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, id, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		if err := s.audit.Record(ctx, tx, actor, "technology.deployed", "technology_transfer", id, map[string]any{"site_id": siteID}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "technology.deployed", id, map[string]any{"transfer_id": id, "site_id": siteID})
		return err
	})
}

func (s *Service) Get(ctx context.Context, id string) (Transfer, error) {
	var t Transfer
	err := s.store.DB().QueryRow(ctx, `SELECT id,program_id,COALESCE(site_id,''),title,technology_version,status,version,created_at,updated_at FROM technology_transfers WHERE id=$1`, id).Scan(&t.ID, &t.ProgramID, &t.SiteID, &t.Title, &t.TechnologyVersion, &t.Status, &t.Version, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Transfer{}, platform.ErrNotFound
	}
	if err != nil {
		return Transfer{}, err
	}
	if err := access.RequireProgram(ctx, s.store.DB(), t.ProgramID); err != nil {
		return Transfer{}, err
	}
	return t, nil
}
