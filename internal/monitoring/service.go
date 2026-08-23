package monitoring

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/access"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/audit"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/jackc/pgx/v5"
)

type Campaign struct {
	ID              string     `json:"id"`
	SiteID          string     `json:"site_id"`
	CycleKey        string     `json:"cycle_key"`
	Status          string     `json:"status"`
	BaselineVersion int64      `json:"baseline_version"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	SubmittedAt     *time.Time `json:"submitted_at,omitempty"`
	Version         int64      `json:"version"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type Observation struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	MeasuredAt time.Time `json:"measured_at"`
	Value      float64   `json:"value"`
	Unit       string    `json:"unit"`
	Quality    string    `json:"quality"`
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

func (s *Service) Plan(ctx context.Context, siteID, cycleKey string) (Campaign, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager, platform.RoleFieldOfficer)
	if err != nil {
		return Campaign{}, err
	}
	if siteID == "" || cycleKey == "" {
		return Campaign{}, platform.FieldError{Field: "campaign", Message: "site and cycle are required"}
	}
	now := s.clock.Now()
	var siteVersion int64
	c := Campaign{ID: s.ids.New("cam"), SiteID: siteID, CycleKey: cycleKey, Status: "planned", BaselineVersion: siteVersion, Version: 1, CreatedAt: now, UpdatedAt: now}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var organizationID, ownerID, status string
		if err := tx.QueryRow(ctx, `SELECT s.organization_id,p.owner_organization_id,s.status,s.version FROM sites s JOIN programs p ON p.id=s.program_id WHERE s.id=$1 FOR SHARE OF s`, siteID).Scan(&organizationID, &ownerID, &status, &siteVersion); err != nil {
			return store.Translate(err)
		}
		if actor.Role == platform.RoleProgramManager {
			if err := platform.RequireOrganization(actor, ownerID); err != nil {
				return err
			}
		} else if err := platform.RequireOrganization(actor, organizationID); err != nil {
			return err
		}
		if status != "approved" && status != "active" && status != "under_review" {
			return platform.StateError{Resource: "site", Current: status, Target: "monitoring"}
		}
		c.BaselineVersion = siteVersion
		_, err := tx.Exec(ctx, `INSERT INTO monitoring_campaigns(id,site_id,cycle_key,status,baseline_version,version,created_at,updated_at) VALUES($1,$2,$3,'planned',$4,1,$5,$5)`, c.ID, siteID, cycleKey, siteVersion, now)
		if err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "campaign.planned", "campaign", c.ID, map[string]any{"site_id": siteID, "cycle": cycleKey})
	})
	return c, err
}

func (s *Service) Start(ctx context.Context, campaignID string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleFieldOfficer)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var siteID, organizationID, status string
		if err := tx.QueryRow(ctx, `SELECT c.site_id,s.organization_id,c.status FROM monitoring_campaigns c JOIN sites s ON s.id=c.site_id WHERE c.id=$1 FOR UPDATE OF c`, campaignID).Scan(&siteID, &organizationID, &status); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, organizationID); err != nil {
			return err
		}
		if status != "planned" {
			return platform.StateError{Resource: "campaign", Current: status, Target: "collecting"}
		}
		var other int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM monitoring_campaigns WHERE site_id=$1 AND status='collecting' AND id<>$2`, siteID, campaignID).Scan(&other); err != nil {
			return err
		}
		if other > 0 {
			return platform.ErrConflict
		}
		tag, err := tx.Exec(ctx, `UPDATE monitoring_campaigns SET status='collecting',started_at=$3,version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, campaignID, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		return s.audit.Record(ctx, tx, actor, "campaign.started", "campaign", campaignID, map[string]any{"site_id": siteID})
	})
}

func (s *Service) AddObservations(ctx context.Context, campaignID string, observations []Observation) error {
	actor, err := platform.RequireRole(ctx, platform.RoleFieldOfficer)
	if err != nil {
		return err
	}
	if len(observations) == 0 || len(observations) > 500 {
		return platform.FieldError{Field: "observations", Message: "between 1 and 500 observations required"}
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var organizationID, status string
		if err := tx.QueryRow(ctx, `SELECT s.organization_id,c.status FROM monitoring_campaigns c JOIN sites s ON s.id=c.site_id WHERE c.id=$1 FOR UPDATE OF c`, campaignID).Scan(&organizationID, &status); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, organizationID); err != nil {
			return err
		}
		if status != "collecting" {
			return platform.StateError{Resource: "campaign", Current: status, Target: "collect observations"}
		}
		allowed := map[string]bool{"vegetation": true, "soil": true, "water": true, "biodiversity": true, "dust": true}
		for i := range observations {
			o := &observations[i]
			if !allowed[o.Kind] || o.Unit == "" || o.MeasuredAt.IsZero() {
				return platform.FieldError{Field: "observations", Message: "kind, measurement time and unit are required"}
			}
			if o.ID == "" {
				o.ID = s.ids.New("obs")
			}
			if o.Quality == "" {
				o.Quality = "raw"
			}
			_, err := tx.Exec(ctx, `INSERT INTO observations(id,campaign_id,kind,measured_at,value,unit,quality,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, o.ID, campaignID, o.Kind, o.MeasuredAt, o.Value, o.Unit, o.Quality, now)
			if err != nil {
				return store.Translate(err)
			}
		}
		return s.audit.Record(ctx, tx, actor, "campaign.observations_added", "campaign", campaignID, map[string]any{"batch_size": len(observations)})
	})
}

func (s *Service) Submit(ctx context.Context, campaignID string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleFieldOfficer)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	var organizationID, status string
	var count int
	if err := s.store.DB().QueryRow(ctx, `SELECT s.organization_id,c.status FROM monitoring_campaigns c JOIN sites s ON s.id=c.site_id WHERE c.id=$1`, campaignID).Scan(&organizationID, &status); err != nil {
		return store.Translate(err)
	}
	if err := platform.RequireOrganization(actor, organizationID); err != nil {
		return err
	}
	if status != "collecting" {
		return platform.StateError{Resource: "campaign", Current: status, Target: "submitted"}
	}
	if err := s.store.DB().QueryRow(ctx, `SELECT count(*) FROM observations WHERE campaign_id=$1 AND quality<>'rejected'`, campaignID).Scan(&count); err != nil {
		return err
	}
	if count < 3 {
		return platform.FieldError{Field: "observations", Message: "at least three usable observations are required"}
	}
	tag, err := s.store.DB().Exec(ctx, `UPDATE monitoring_campaigns SET status='submitted',submitted_at=$3,version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, campaignID, expectedVersion, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return platform.ErrConflict
	}
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.audit.Record(ctx, tx, actor, "campaign.submitted", "campaign", campaignID, map[string]any{"observation_count": count}); err != nil {
			return err
		}
		_, err := s.outbox.Enqueue(ctx, tx, "campaign.submitted", campaignID, map[string]any{"campaign_id": campaignID})
		return err
	})
}

func (s *Service) Get(ctx context.Context, id string) (Campaign, error) {
	if err := access.RequireCampaign(ctx, s.store.DB(), id); err != nil {
		return Campaign{}, err
	}
	var c Campaign
	err := s.store.DB().QueryRow(ctx, `SELECT id,site_id,cycle_key,status,baseline_version,started_at,submitted_at,version,created_at,updated_at FROM monitoring_campaigns WHERE id=$1`, id).Scan(&c.ID, &c.SiteID, &c.CycleKey, &c.Status, &c.BaselineVersion, &c.StartedAt, &c.SubmittedAt, &c.Version, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, platform.ErrNotFound
	}
	return c, err
}

func (s *Service) ListObservations(ctx context.Context, campaignID string) ([]Observation, error) {
	if err := access.RequireCampaign(ctx, s.store.DB(), campaignID); err != nil {
		return nil, err
	}
	rows, err := s.store.DB().Query(ctx, `SELECT id,kind,measured_at,value,unit,quality FROM observations WHERE campaign_id=$1 ORDER BY measured_at,id`, campaignID)
	if err != nil {
		return nil, fmt.Errorf("query observations: %w", err)
	}
	defer rows.Close()
	result := make([]Observation, 0)
	for rows.Next() {
		if err := platform.CheckContext(ctx); err != nil {
			return nil, err
		}
		var o Observation
		if err := rows.Scan(&o.ID, &o.Kind, &o.MeasuredAt, &o.Value, &o.Unit, &o.Quality); err != nil {
			return nil, err
		}
		result = append(result, o)
	}
	return result, rows.Err()
}
