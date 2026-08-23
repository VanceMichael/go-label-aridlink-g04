package review

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

type Review struct {
	ID            string    `json:"id"`
	BundleID      string    `json:"bundle_id"`
	ReviewerID    string    `json:"reviewer_id"`
	Status        string    `json:"status"`
	Conclusion    string    `json:"conclusion,omitempty"`
	Round         int       `json:"round"`
	BundleVersion int64     `json:"bundle_version"`
	Version       int64     `json:"version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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

func (s *Service) Assign(ctx context.Context, bundleID, reviewerID string) (Review, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleProgramManager)
	if err != nil {
		return Review{}, err
	}
	now := s.clock.Now()
	var result Review
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, ownerID string
		var version int64
		if err := tx.QueryRow(ctx, `SELECT b.status,p.owner_organization_id,b.version FROM evidence_bundles b JOIN sites s ON s.id=b.site_id JOIN programs p ON p.id=s.program_id WHERE b.id=$1 FOR UPDATE OF b`, bundleID).Scan(&status, &ownerID, &version); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, ownerID); err != nil {
			return err
		}
		if status != "sealed" && status != "in_review" {
			return platform.StateError{Resource: "evidence", Current: status, Target: "in_review"}
		}
		var role platform.Role
		if err := tx.QueryRow(ctx, `SELECT role FROM users WHERE id=$1 AND active=true`, reviewerID).Scan(&role); err != nil {
			return store.Translate(err)
		}
		if role != platform.RoleTechnicalReviewer {
			return platform.ErrForbidden
		}
		var round int
		if err := tx.QueryRow(ctx, `SELECT COALESCE(max(round),0)+1 FROM reviews WHERE bundle_id=$1`, bundleID).Scan(&round); err != nil {
			return err
		}
		if status == "sealed" {
			if _, err := tx.Exec(ctx, `UPDATE evidence_bundles SET status='in_review',version=version+1,updated_at=$2 WHERE id=$1`, bundleID, now); err != nil {
				return err
			}
			version++
		}
		result = Review{ID: s.ids.New("rev"), BundleID: bundleID, ReviewerID: reviewerID, Status: "assigned", Round: round, BundleVersion: version, Version: 1, CreatedAt: now, UpdatedAt: now}
		if _, err := tx.Exec(ctx, `INSERT INTO reviews(id,bundle_id,reviewer_id,round,status,bundle_version,version,created_at,updated_at) VALUES($1,$2,$3,$4,'assigned',$5,1,$6,$6)`, result.ID, bundleID, reviewerID, round, version, now); err != nil {
			return store.Translate(err)
		}
		return s.audit.Record(ctx, tx, actor, "review.assigned", "evidence_bundle", bundleID, map[string]any{"review_id": result.ID, "reviewer_id": reviewerID, "round": round})
	})
	return result, err
}

func (s *Service) Conclude(ctx context.Context, reviewID, decision, conclusion string, expectedVersion int64) error {
	actor, err := platform.RequireRole(ctx, platform.RoleTechnicalReviewer)
	if err != nil {
		return err
	}
	if decision != "accepted" && decision != "rejected" {
		return platform.FieldError{Field: "decision", Message: "accepted or rejected required"}
	}
	if conclusion == "" {
		return platform.FieldError{Field: "conclusion", Message: "review conclusion required"}
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var r Review
		if err := tx.QueryRow(ctx, `SELECT id,bundle_id,reviewer_id,round,status,COALESCE(conclusion,''),bundle_version,version,created_at,updated_at FROM reviews WHERE id=$1 FOR UPDATE`, reviewID).Scan(&r.ID, &r.BundleID, &r.ReviewerID, &r.Round, &r.Status, &r.Conclusion, &r.BundleVersion, &r.Version, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return store.Translate(err)
		}
		if r.ReviewerID != actor.UserID {
			return platform.ErrForbidden
		}
		if r.Status != "assigned" && r.Status != "in_progress" {
			return platform.StateError{Resource: "review", Current: r.Status, Target: decision}
		}
		var bundleVersion int64
		var bundleStatus string
		if err := tx.QueryRow(ctx, `SELECT version,status FROM evidence_bundles WHERE id=$1 FOR UPDATE`, r.BundleID).Scan(&bundleVersion, &bundleStatus); err != nil {
			return err
		}
		if bundleStatus != "in_review" || bundleVersion != r.BundleVersion {
			return platform.ErrConflict
		}
		tag, err := tx.Exec(ctx, `UPDATE reviews SET status=$3,conclusion=$4,version=version+1,updated_at=$5 WHERE id=$1 AND version=$2`, reviewID, expectedVersion, decision, conclusion, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return platform.ErrConflict
		}
		_, err = tx.Exec(ctx, `UPDATE evidence_bundles SET status=$2,version=version+1,updated_at=$3 WHERE id=$1 AND version=$4`, r.BundleID, decision, now, bundleVersion)
		if err != nil {
			return err
		}
		if err := s.audit.Record(ctx, tx, actor, "review.concluded", "evidence_bundle", r.BundleID, map[string]any{"review_id": reviewID, "decision": decision}); err != nil {
			return err
		}
		_, err = s.outbox.Enqueue(ctx, tx, "review."+decision, r.BundleID, map[string]any{"bundle_id": r.BundleID, "review_id": reviewID})
		return err
	})
}

func (s *Service) Get(ctx context.Context, id string) (Review, error) {
	if err := access.RequireReview(ctx, s.store.DB(), id); err != nil {
		return Review{}, err
	}
	var r Review
	err := s.store.DB().QueryRow(ctx, `SELECT id,bundle_id,reviewer_id,round,status,COALESCE(conclusion,''),bundle_version,version,created_at,updated_at FROM reviews WHERE id=$1`, id).Scan(&r.ID, &r.BundleID, &r.ReviewerID, &r.Round, &r.Status, &r.Conclusion, &r.BundleVersion, &r.Version, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Review{}, platform.ErrNotFound
	}
	return r, err
}
