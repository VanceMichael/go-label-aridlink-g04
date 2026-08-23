package alert

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

type Alert struct {
	ID        string    `json:"id"`
	ProgramID string    `json:"program_id"`
	Kind      string    `json:"kind"`
	Severity  string    `json:"severity"`
	Status    string    `json:"status"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

func (s *Service) Create(ctx context.Context, programID, kind, severity string, startsAt, endsAt time.Time) (Alert, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return Alert{}, err
	}
	kinds := map[string]bool{"dust": true, "drought": true, "flood": true, "wildfire": true}
	severities := map[string]bool{"advisory": true, "watch": true, "warning": true, "emergency": true}
	if !kinds[kind] || !severities[severity] || !endsAt.After(startsAt) {
		return Alert{}, platform.FieldError{Field: "alert", Message: "kind, severity and valid window required"}
	}
	now := s.clock.Now()
	created := Alert{ID: s.ids.New("alt"), ProgramID: programID, Kind: kind, Severity: severity, Status: "draft", StartsAt: startsAt, EndsAt: endsAt, Version: 1, CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerID, status string
		if err := tx.QueryRow(ctx, `SELECT owner_organization_id,status FROM programs WHERE id=$1 FOR SHARE`, programID).Scan(&ownerID, &status); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "active" {
			return platform.StateError{Resource: "program", Current: status, Target: "alert creation"}
		}
		_, err := tx.Exec(ctx, `INSERT INTO alerts(id,program_id,kind,severity,status,starts_at,ends_at,version,created_at,updated_at) VALUES($1,$2,$3,$4,'draft',$5,$6,1,$7,$7)`, created.ID, programID, kind, severity, startsAt, endsAt, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "alert.created", "alert", created.ID, map[string]any{"kind": kind, "severity": severity})
	})
	return created, err
}

func (s *Service) Publish(ctx context.Context, alertID string, siteIDs []string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return err
	}
	if len(siteIDs) == 0 {
		return platform.FieldError{Field: "sites", Message: "at least one affected site required"}
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var programID, status, ownerID string
		if err := tx.QueryRow(ctx, `SELECT a.program_id,a.status,p.owner_organization_id FROM alerts a JOIN programs p ON p.id=a.program_id WHERE a.id=$1 FOR UPDATE OF a`, alertID).Scan(&programID, &status, &ownerID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "draft" {
			return platform.StateError{Resource: "alert", Current: status, Target: "published"}
		}
		seen := make(map[string]struct{}, len(siteIDs))
		for _, siteID := range siteIDs {
			if _, ok := seen[siteID]; ok {
				continue
			}
			seen[siteID] = struct{}{}
			var linkedProgram string
			if err := tx.QueryRow(ctx, `SELECT program_id FROM sites WHERE id=$1`, siteID).Scan(&linkedProgram); err != nil {
				return store.Translate(err)
			}
			if linkedProgram != programID {
				return platform.ErrConflict
			}
			if _, err := tx.Exec(ctx, `INSERT INTO alert_sites(alert_id,site_id) VALUES($1,$2)`, alertID, siteID); err != nil {
				return store.Translate(err)
			}
		}
		tag, err := tx.Exec(ctx, `UPDATE alerts SET status='published',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, alertID, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		if err := s.audit.Record(ctx, tx, actor, "alert.published", "alert", alertID, map[string]any{"site_count": len(seen)}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "alert.published", alertID, map[string]any{"alert_id": alertID, "site_ids": siteIDs})
		return err
	})
}

func (s *Service) Acknowledge(ctx context.Context, alertID string) error {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager, platform.RoleFieldOfficer)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status string
		var affected bool
		if err := tx.QueryRow(ctx, `SELECT status FROM alerts WHERE id=$1 FOR SHARE`, alertID).Scan(&status); err != nil {
			return store.Translate(err)
		}
		if status != "published" {
			return platform.StateError{Resource: "alert", Current: status, Target: "acknowledged"}
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM alert_sites a JOIN sites s ON s.id=a.site_id WHERE a.alert_id=$1 AND s.organization_id=$2)`, alertID, actor.OrganizationID).Scan(&affected); err != nil {
			return err
		}
		if !affected {
			return platform.ErrForbidden
		}
		_, err := tx.Exec(ctx, `INSERT INTO alert_acknowledgements(alert_id,organization_id,acknowledged_by,acknowledged_at) VALUES($1,$2,$3,$4) ON CONFLICT(alert_id,organization_id) DO NOTHING`, alertID, actor.OrganizationID, actor.UserID, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "alert.acknowledged", "alert", alertID, map[string]any{"organization_id": actor.OrganizationID})
	})
}

func (s *Service) Expire(ctx context.Context, limit int) (int, error) {
	now := s.clock.Now()
	expired := 0
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM alerts WHERE status='published' AND ends_at<=$1 ORDER BY ends_at FOR UPDATE SKIP LOCKED LIMIT $2`, now, limit)
		if err != nil {
			return err
		}
		ids := make([]string, 0)
		for rows.Next() {
			if err := platform.CheckContext(ctx); err != nil {
				rows.Close()
				return err
			}
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			tag, err := tx.Exec(ctx, `UPDATE alerts SET status='expired',version=version+1,updated_at=$2 WHERE id=$1 AND status='published'`, id, now)
			if err != nil {
				return err
			}
			expired += int(tag.RowsAffected())
		}
		return nil
	})
	return expired, err
}

func (s *Service) Get(ctx context.Context, id string) (Alert, error) {
	var a Alert
	err := s.store.DB().QueryRow(ctx, `SELECT id,program_id,kind,severity,status,starts_at,ends_at,version,created_at,updated_at FROM alerts WHERE id=$1`, id).Scan(&a.ID, &a.ProgramID, &a.Kind, &a.Severity, &a.Status, &a.StartsAt, &a.EndsAt, &a.Version, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Alert{}, platform.ErrNotFound
	}
	if err != nil {
		return Alert{}, err
	}
	if err := access.RequireProgram(ctx, s.store.DB(), a.ProgramID); err != nil {
		return Alert{}, err
	}
	return a, nil
}
