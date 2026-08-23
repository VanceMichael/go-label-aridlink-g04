package query

import (
	"context"
	"fmt"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/jackc/pgx/v5"
)

type ProgramOverview struct {
	ProgramID           string         `json:"program_id"`
	ProgramName         string         `json:"program_name"`
	ProgramStatus       string         `json:"program_status"`
	BudgetCents         int64          `json:"budget_cents"`
	HeldBudgetCents     int64          `json:"held_budget_cents"`
	DisbursedCents      int64          `json:"disbursed_cents"`
	SiteStates          map[string]int `json:"site_states"`
	CampaignStates      map[string]int `json:"campaign_states"`
	WorkOrderStates     map[string]int `json:"work_order_states"`
	EvidenceStates      map[string]int `json:"evidence_states"`
	MilestoneStates     map[string]int `json:"milestone_states"`
	PublishedAlertCount int            `json:"published_alert_count"`
	PendingReviewCount  int            `json:"pending_review_count"`
	GeneratedAt         time.Time      `json:"generated_at"`
}

type Service struct {
	store *store.Store
	clock platform.Clock
}

func NewService(st *store.Store, clock platform.Clock) *Service {
	return &Service{store: st, clock: clock}
}

func (s *Service) ProgramOverview(ctx context.Context, programID string) (ProgramOverview, error) {
	actor, err := platform.ActorFrom(ctx)
	if err != nil {
		return ProgramOverview{}, err
	}
	result := ProgramOverview{ProgramID: programID, SiteStates: map[string]int{}, CampaignStates: map[string]int{},
		WorkOrderStates: map[string]int{}, EvidenceStates: map[string]int{}, MilestoneStates: map[string]int{}, GeneratedAt: s.clock.Now()}
	queryCtx := context.WithoutCancel(ctx)
	err = s.store.WithTx(queryCtx, func(tx pgx.Tx) error {
		var ownerID string
		if err := tx.QueryRow(queryCtx, `SELECT owner_organization_id,name,status,budget_cents FROM programs WHERE id=$1`, programID).
			Scan(&ownerID, &result.ProgramName, &result.ProgramStatus, &result.BudgetCents); err != nil {
			return store.Translate(err)
		}
		if actor.Role != platform.RolePlatformAdmin && actor.OrganizationID != ownerID {
			var member bool
			if err := tx.QueryRow(queryCtx, `SELECT EXISTS(SELECT 1 FROM partnerships WHERE program_id=$1 AND organization_id=$2 AND status='active')`, programID, actor.OrganizationID).Scan(&member); err != nil {
				return err
			}
			if !member {
				return platform.ErrForbidden
			}
		}
		if err := scanStates(queryCtx, tx, `SELECT status,count(*) FROM sites WHERE program_id=$1 GROUP BY status`, programID, result.SiteStates); err != nil {
			return err
		}
		if err := scanStates(queryCtx, tx, `SELECT c.status,count(*) FROM monitoring_campaigns c JOIN sites s ON s.id=c.site_id WHERE s.program_id=$1 GROUP BY c.status`, programID, result.CampaignStates); err != nil {
			return err
		}
		if err := scanStates(queryCtx, tx, `SELECT w.status,count(*) FROM work_orders w JOIN sites s ON s.id=w.site_id WHERE s.program_id=$1 GROUP BY w.status`, programID, result.WorkOrderStates); err != nil {
			return err
		}
		if err := scanStates(queryCtx, tx, `SELECT b.status,count(*) FROM evidence_bundles b JOIN sites s ON s.id=b.site_id WHERE s.program_id=$1 GROUP BY b.status`, programID, result.EvidenceStates); err != nil {
			return err
		}
		if err := scanStates(queryCtx, tx, `SELECT status,count(*) FROM grant_milestones WHERE program_id=$1 GROUP BY status`, programID, result.MilestoneStates); err != nil {
			return err
		}
		return tx.QueryRow(queryCtx, `SELECT
			COALESCE((SELECT sum(amount_cents) FROM budget_reservations WHERE program_id=$1 AND status='held'),0),
			COALESCE((SELECT sum(amount_cents) FROM budget_reservations WHERE program_id=$1 AND status='consumed'),0),
			(SELECT count(*) FROM alerts WHERE program_id=$1 AND status='published'),
			(SELECT count(*) FROM reviews r JOIN evidence_bundles b ON b.id=r.bundle_id JOIN sites s ON s.id=b.site_id WHERE s.program_id=$1 AND r.status IN ('assigned','in_progress'))`, programID).
			Scan(&result.HeldBudgetCents, &result.DisbursedCents, &result.PublishedAlertCount, &result.PendingReviewCount)
	})
	if err != nil {
		return ProgramOverview{}, fmt.Errorf("load program overview: %w", err)
	}
	return result, nil
}

func scanStates(ctx context.Context, tx pgx.Tx, statement, programID string, target map[string]int) error {
	rows, err := tx.Query(ctx, statement, programID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := platform.CheckContext(ctx); err != nil {
			return err
		}
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return err
		}
		target[state] = count
	}
	return rows.Err()
}
