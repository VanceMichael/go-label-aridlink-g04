package intervention

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

type WorkSpec struct {
	Title string `json:"title"`
}

type Plan struct {
	ID                 string    `json:"id"`
	SiteID             string    `json:"site_id"`
	Title              string    `json:"title"`
	Status             string    `json:"status"`
	EstimatedCostCents int64     `json:"estimated_cost_cents"`
	Version            int64     `json:"version"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type WorkOrder struct {
	ID             string     `json:"id"`
	PlanID         string     `json:"plan_id"`
	SiteID         string     `json:"site_id"`
	Title          string     `json:"title"`
	Status         string     `json:"status"`
	OwnerToken     string     `json:"owner_token,omitempty"`
	ResultSummary  string     `json:"result_summary,omitempty"`
	SequenceNo     int        `json:"sequence_no"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at,omitempty"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
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

func (s *Service) CreatePlan(ctx context.Context, siteID, title string, cost int64, specs []WorkSpec) (Plan, []WorkOrder, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return Plan{}, nil, err
	}
	if title == "" || cost <= 0 || len(specs) == 0 {
		return Plan{}, nil, platform.FieldError{Field: "plan", Message: "title, positive cost and work orders required"}
	}
	now := s.clock.Now()
	plan := Plan{ID: s.ids.New("pln"), SiteID: siteID, Title: title, Status: "draft", EstimatedCostCents: cost, Version: 1, CreatedAt: now, UpdatedAt: now}
	orders := make([]WorkOrder, 0, len(specs))
	for i, spec := range specs {
		if spec.Title == "" {
			return Plan{}, nil, platform.FieldError{Field: "work_order.title", Message: "title required"}
		}
		orders = append(orders, WorkOrder{ID: s.ids.New("wrk"), PlanID: plan.ID, SiteID: siteID, SequenceNo: i + 1, Title: spec.Title, Status: "scheduled", Version: 1, CreatedAt: now, UpdatedAt: now})
	}
	_, err = s.store.DB().Exec(ctx, `INSERT INTO intervention_plans(id,site_id,title,status,estimated_cost_cents,version,created_at,updated_at) VALUES($1,$2,$3,'draft',$4,1,$5,$5)`, plan.ID, siteID, title, cost, now)
	if err != nil {
		return Plan{}, nil, store.Translate(err)
	}
	for _, order := range orders {
		if _, err := s.store.DB().Exec(ctx, `INSERT INTO work_orders(id,plan_id,site_id,sequence_no,title,status,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,'scheduled',1,$6,$6)`, order.ID, plan.ID, siteID, order.SequenceNo, order.Title, now); err != nil {
			return plan, orders, store.Translate(err)
		}
	}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var ownerOrganizationID, status string
		if err := tx.QueryRow(ctx, `SELECT p.owner_organization_id,s.status FROM sites s JOIN programs p ON p.id=s.program_id WHERE s.id=$1 FOR UPDATE OF s`, siteID).Scan(&ownerOrganizationID, &status); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerOrganizationID); err != nil {
			return err
		}
		if status != "approved" && status != "active" {
			return platform.StateError{Resource: "site", Current: status, Target: "intervention planning"}
		}
		if err := s.audit.Record(ctx, tx, actor, "plan.created", "plan", plan.ID, map[string]any{"orders": len(orders), "cost": cost}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "plan.created", plan.ID, map[string]any{"plan_id": plan.ID, "site_id": siteID})
		return err
	})
	return plan, orders, err
}

func (s *Service) ApprovePlan(ctx context.Context, planID string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, ownerOrganizationID string
		if err := tx.QueryRow(ctx, `SELECT p.status,pr.owner_organization_id FROM intervention_plans p JOIN sites s ON s.id=p.site_id JOIN programs pr ON pr.id=s.program_id WHERE p.id=$1 FOR UPDATE OF p`, planID).Scan(&status, &ownerOrganizationID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerOrganizationID); err != nil {
			return err
		}
		if status != "draft" {
			return platform.StateError{Resource: "plan", Current: status, Target: "approved"}
		}
		tag, err := tx.Exec(ctx, `UPDATE intervention_plans SET status='approved',version=version+1,updated_at=$3 WHERE id=$1 AND version=$2`, planID, expectedVersion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		return s.audit.Record(ctx, tx, actor, "plan.approved", "plan", planID, map[string]any{"version": expectedVersion + 1})
	})
}

func (s *Service) Claim(ctx context.Context, workerToken string, lease time.Duration) (WorkOrder, error) {
	if workerToken == "" || lease <= 0 {
		return WorkOrder{}, platform.FieldError{Field: "lease", Message: "worker token and lease required"}
	}
	now := s.clock.Now()
	var order WorkOrder
	err := s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `SELECT w.id FROM work_orders w JOIN intervention_plans p ON p.id=w.plan_id WHERE p.status IN ('approved','running') AND (w.status='scheduled' OR (w.status='claimed' AND w.lease_expires_at<=$1)) ORDER BY w.created_at,w.sequence_no FOR UPDATE OF w SKIP LOCKED LIMIT 1`, now).Scan(&id)
		if err != nil {
			return store.Translate(err)
		}
		leaseUntil := now.Add(lease)
		err = tx.QueryRow(ctx, `UPDATE work_orders SET status='claimed',owner_token=$2,lease_expires_at=$3,version=version+1,updated_at=$4 WHERE id=$1 RETURNING id,plan_id,site_id,sequence_no,title,status,COALESCE(owner_token,''),lease_expires_at,COALESCE(result_summary,''),version,created_at,updated_at`, id, workerToken, leaseUntil, now).Scan(&order.ID, &order.PlanID, &order.SiteID, &order.SequenceNo, &order.Title, &order.Status, &order.OwnerToken, &order.LeaseExpiresAt, &order.ResultSummary, &order.Version, &order.CreatedAt, &order.UpdatedAt)
		return err
	})
	return order, err
}

func (s *Service) Renew(ctx context.Context, orderID, owner string, lease time.Duration) error {
	now := s.clock.Now()
	tag, err := s.store.DB().Exec(ctx, `UPDATE work_orders SET lease_expires_at=$4,updated_at=$3,version=version+1 WHERE id=$1 AND owner_token=$2 AND status IN ('claimed','running') AND lease_expires_at>$3`, orderID, owner, now, now.Add(lease))
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return platform.ErrLeaseLost
	}
	return nil
}

func (s *Service) Start(ctx context.Context, orderID, owner string) error {
	now := s.clock.Now()
	tag, err := s.store.DB().Exec(ctx, `UPDATE work_orders SET status='running',version=version+1,updated_at=$3 WHERE id=$1 AND owner_token=$2 AND status='claimed' AND lease_expires_at>$3`, orderID, owner, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return platform.ErrLeaseLost
	}
	return nil
}

func (s *Service) Complete(ctx context.Context, orderID, owner, summary, evidenceBundleID string) error {
	if summary == "" || evidenceBundleID == "" {
		return platform.FieldError{Field: "completion", Message: "summary and sealed evidence required"}
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status string
		var expires time.Time
		var token, siteID string
		if err := tx.QueryRow(ctx, `SELECT status,owner_token,lease_expires_at,site_id FROM work_orders WHERE id=$1 FOR UPDATE`, orderID).Scan(&status, &token, &expires, &siteID); err != nil {
			return store.Translate(err)
		}
		if status != "running" || token != owner || !expires.After(now) {
			return platform.ErrLeaseLost
		}
		var evidenceStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM evidence_bundles WHERE id=$1 AND site_id=$2`, evidenceBundleID, siteID).Scan(&evidenceStatus); err != nil {
			return store.Translate(err)
		}
		if evidenceStatus != "sealed" && evidenceStatus != "accepted" {
			return platform.StateError{Resource: "evidence", Current: evidenceStatus, Target: "completion"}
		}
		_, err := tx.Exec(ctx, `UPDATE work_orders SET status='completed',result_summary=$2,owner_token=NULL,lease_expires_at=NULL,version=version+1,updated_at=$3 WHERE id=$1`, orderID, summary, now)
		if err != nil {
			return err
		}
		actor := platform.Actor{UserID: "worker:" + owner}
		if err := s.audit.Record(ctx, tx, actor, "work_order.completed", "work_order", orderID, map[string]any{"evidence_bundle_id": evidenceBundleID}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "work_order.completed", orderID, map[string]any{"order_id": orderID, "site_id": siteID})
		return err
	})
}

func (s *Service) GetOrder(ctx context.Context, id string) (WorkOrder, error) {
	if err := access.RequireWorkOrder(ctx, s.store.DB(), id); err != nil {
		return WorkOrder{}, err
	}
	var o WorkOrder
	err := s.store.DB().QueryRow(ctx, `SELECT id,plan_id,site_id,sequence_no,title,status,COALESCE(owner_token,''),lease_expires_at,COALESCE(result_summary,''),version,created_at,updated_at FROM work_orders WHERE id=$1`, id).Scan(&o.ID, &o.PlanID, &o.SiteID, &o.SequenceNo, &o.Title, &o.Status, &o.OwnerToken, &o.LeaseExpiresAt, &o.ResultSummary, &o.Version, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WorkOrder{}, platform.ErrNotFound
	}
	if err != nil {
		return WorkOrder{}, fmt.Errorf("get work order: %w", err)
	}
	return o, nil
}
