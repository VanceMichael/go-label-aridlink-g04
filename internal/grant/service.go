package grant

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

type Milestone struct {
	ID               string    `json:"id"`
	ProgramID        string    `json:"program_id"`
	SiteID           string    `json:"site_id"`
	RequiredBundleID string    `json:"required_bundle_id,omitempty"`
	Title            string    `json:"title"`
	Status           string    `json:"status"`
	AmountCents      int64     `json:"amount_cents"`
	Version          int64     `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Reservation struct {
	ID          string    `json:"id"`
	MilestoneID string    `json:"milestone_id"`
	ProgramID   string    `json:"program_id"`
	Status      string    `json:"status"`
	AmountCents int64     `json:"amount_cents"`
	ExpiresAt   time.Time `json:"expires_at"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
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

func (s *Service) CreateMilestone(ctx context.Context, programID, siteID, bundleID, title string, amount int64) (Milestone, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return Milestone{}, err
	}
	if title == "" || amount <= 0 {
		return Milestone{}, platform.FieldError{Field: "milestone", Message: "title and positive amount required"}
	}
	now := s.clock.Now()
	m := Milestone{ID: s.ids.New("mil"), ProgramID: programID, SiteID: siteID, RequiredBundleID: bundleID, Title: title, AmountCents: amount, Status: "planned", Version: 1, CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID, programStatus, linkedProgram string
		if err := tx.QueryRow(ctx, `SELECT p.owner_organization_id,p.status,s.program_id FROM programs p JOIN sites s ON s.id=$2 WHERE p.id=$1 FOR UPDATE OF p`, programID, siteID).Scan(&ownerID, &programStatus, &linkedProgram); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if linkedProgram != programID {
			return platform.ErrConflict
		}
		if programStatus != "active" {
			return platform.StateError{Resource: "program", Current: programStatus, Target: "milestone creation"}
		}
		if bundleID != "" {
			var bundleSite string
			if err := tx.QueryRow(ctx, `SELECT site_id FROM evidence_bundles WHERE id=$1`, bundleID).Scan(&bundleSite); err != nil {
				return store.Translate(err)
			}
			if bundleSite != siteID {
				return platform.ErrConflict
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO grant_milestones(id,program_id,site_id,required_bundle_id,title,amount_cents,status,version,created_at,updated_at) VALUES($1,$2,$3,NULLIF($4,''),$5,$6,'planned',1,$7,$7)`, m.ID, programID, siteID, bundleID, title, amount, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "milestone.created", "milestone", m.ID, map[string]any{"amount_cents": amount, "site_id": siteID})
	})
	return m, err
}

func (s *Service) MarkEligible(ctx context.Context, milestoneID string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleTechnicalReviewer)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, bundleID, bundleStatus, ownerID string
		if err := tx.QueryRow(ctx, `SELECT m.status,COALESCE(m.required_bundle_id,''),COALESCE(b.status,''),p.owner_organization_id FROM grant_milestones m JOIN programs p ON p.id=m.program_id LEFT JOIN evidence_bundles b ON b.id=m.required_bundle_id WHERE m.id=$1 FOR UPDATE OF m`, milestoneID).Scan(&status, &bundleID, &bundleStatus, &ownerID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "planned" {
			return platform.StateError{Resource: "milestone", Current: status, Target: "eligible"}
		}
		if bundleID == "" || bundleStatus != "accepted" {
			return platform.FieldError{Field: "evidence", Message: "accepted evidence required"}
		}
		tag, err := tx.Exec(ctx, `UPDATE grant_milestones SET status='eligible',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, milestoneID, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		return s.audit.Record(ctx, tx, actor, "milestone.eligible", "milestone", milestoneID, map[string]any{"bundle_id": bundleID})
	})
}

func (s *Service) Reserve(ctx context.Context, milestoneID string, ttl time.Duration) (Reservation, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleFinanceReviewer)
	if err != nil {
		return Reservation{}, err
	}
	if ttl <= 0 {
		return Reservation{}, platform.FieldError{Field: "ttl", Message: "positive reservation ttl required"}
	}
	now := s.clock.Now()
	var reservation Reservation
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var m Milestone
		var budget int64
		var ownerID string
		if err := tx.QueryRow(ctx, `SELECT m.id,m.program_id,m.site_id,COALESCE(m.required_bundle_id,''),m.title,m.status,m.amount_cents,m.version,m.created_at,m.updated_at,p.budget_cents,p.owner_organization_id FROM grant_milestones m JOIN programs p ON p.id=m.program_id WHERE m.id=$1 FOR UPDATE OF m,p`, milestoneID).Scan(&m.ID, &m.ProgramID, &m.SiteID, &m.RequiredBundleID, &m.Title, &m.Status, &m.AmountCents, &m.Version, &m.CreatedAt, &m.UpdatedAt, &budget, &ownerID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if m.Status != "eligible" {
			return platform.StateError{Resource: "milestone", Current: m.Status, Target: "reserved"}
		}
		var committed int64
		if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(amount_cents),0) FROM budget_reservations WHERE program_id=$1 AND status IN ('held','consumed')`, m.ProgramID).Scan(&committed); err != nil {
			return err
		}
		if committed+m.AmountCents > budget {
			return platform.ErrBudgetExceeded
		}
		reservation = Reservation{ID: s.ids.New("rsv"), MilestoneID: m.ID, ProgramID: m.ProgramID, AmountCents: m.AmountCents, Status: "held", ExpiresAt: now.Add(ttl), Version: 1, CreatedAt: now, UpdatedAt: now}
		if _, err := tx.Exec(ctx, `INSERT INTO budget_reservations(id,milestone_id,program_id,amount_cents,status,expires_at,version,created_at,updated_at) VALUES($1,$2,$3,$4,'held',$5,1,$6,$6)`, reservation.ID, m.ID, m.ProgramID, m.AmountCents, reservation.ExpiresAt, now); err != nil {
			return store.Translate(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE grant_milestones SET status='reserved',version=version+1,updated_at=$2 WHERE id=$1`, m.ID, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, tx, actor, "budget.reserved", "milestone", m.ID, map[string]any{"reservation_id": reservation.ID, "amount_cents": m.AmountCents})
	})
	return reservation, err
}

func (s *Service) Disburse(ctx context.Context, milestoneID string) error {
	actor, err := platform.RequireRole(ctx, platform.RoleFinanceReviewer)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, bundleStatus, reservationID, reservationStatus, ownerID string
		if err := tx.QueryRow(ctx, `SELECT m.status,COALESCE(b.status,''),r.id,r.status,p.owner_organization_id FROM grant_milestones m JOIN programs p ON p.id=m.program_id LEFT JOIN evidence_bundles b ON b.id=m.required_bundle_id JOIN budget_reservations r ON r.milestone_id=m.id WHERE m.id=$1 FOR UPDATE OF m,r`, milestoneID).Scan(&status, &bundleStatus, &reservationID, &reservationStatus, &ownerID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "reserved" || reservationStatus != "held" {
			return platform.StateError{Resource: "milestone", Current: status, Target: "disbursed"}
		}
		if bundleStatus != "accepted" {
			return platform.FieldError{Field: "evidence", Message: "evidence is no longer accepted"}
		}
		if _, err := tx.Exec(ctx, `UPDATE budget_reservations SET status='consumed',version=version+1,updated_at=$2 WHERE id=$1`, reservationID, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE grant_milestones SET status='disbursed',version=version+1,updated_at=$2 WHERE id=$1`, milestoneID, now); err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "milestone.disbursed", "milestone", milestoneID, map[string]any{"reservation_id": reservationID}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "milestone.disbursed", milestoneID, map[string]any{"milestone_id": milestoneID})
		return err
	})
}

func (s *Service) ReleaseExpired(ctx context.Context, limit int) (int, error) {
	now := s.clock.Now()
	released := 0
	err := s.store.WithTxOptions(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id,milestone_id FROM budget_reservations WHERE status='held' AND expires_at<=$1 ORDER BY expires_at LIMIT $2`, now, limit)
		if err != nil {
			return err
		}
		pairs := make([][2]string, 0)
		for rows.Next() {
			var p [2]string
			if err := rows.Scan(&p[0], &p[1]); err != nil {
				rows.Close()
				return err
			}
			pairs = append(pairs, p)
		}
		rows.Close()
		for _, p := range pairs {
			tag, err := s.store.DB().Exec(ctx, `UPDATE budget_reservations SET status='released',version=version+1,updated_at=$2 WHERE id=$1 AND status='held'`, p[0], now)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				continue
			}
			if _, err := tx.Exec(ctx, `UPDATE grant_milestones SET status='eligible',version=version+1,updated_at=$2 WHERE id=$1 AND status='reserved'`, p[1], now); err != nil {
				return err
			}
			released++
		}
		return nil
	})
	return released, err
}

func (s *Service) GetReservation(ctx context.Context, id string) (Reservation, error) {
	var r Reservation
	err := s.store.DB().QueryRow(ctx, `SELECT id,milestone_id,program_id,amount_cents,status,expires_at,version,created_at,updated_at FROM budget_reservations WHERE id=$1`, id).Scan(&r.ID, &r.MilestoneID, &r.ProgramID, &r.AmountCents, &r.Status, &r.ExpiresAt, &r.Version, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, platform.ErrNotFound
	}
	if err != nil {
		return Reservation{}, err
	}
	if err := access.RequireProgram(ctx, s.store.DB(), r.ProgramID); err != nil {
		return Reservation{}, err
	}
	return r, nil
}
