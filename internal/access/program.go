package access

import (
	"context"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
)

func RequireProgram(ctx context.Context, db store.DBTX, programID string) error {
	actor, err := platform.ActorFrom(ctx)
	if err != nil {
		return err
	}
	if actor.Role == platform.RolePlatformAdmin {
		return nil
	}
	var allowed bool
	err = db.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM programs p
		WHERE p.id=$1 AND (p.owner_organization_id=$2 OR EXISTS(
			SELECT 1 FROM partnerships x
			WHERE x.program_id=p.id AND x.organization_id=$2 AND x.status IN ('invited','active')))
	)`, programID, actor.OrganizationID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return platform.ErrForbidden
	}
	return nil
}

func RequireSite(ctx context.Context, db store.DBTX, siteID string) error {
	var programID string
	if err := db.QueryRow(ctx, `SELECT program_id FROM sites WHERE id=$1`, siteID).Scan(&programID); err != nil {
		return store.Translate(err)
	}
	return RequireProgram(ctx, db, programID)
}

func RequireCampaign(ctx context.Context, db store.DBTX, campaignID string) error {
	var siteID string
	if err := db.QueryRow(ctx, `SELECT site_id FROM monitoring_campaigns WHERE id=$1`, campaignID).Scan(&siteID); err != nil {
		return store.Translate(err)
	}
	return RequireSite(ctx, db, siteID)
}

func RequireWorkOrder(ctx context.Context, db store.DBTX, workOrderID string) error {
	var siteID string
	if err := db.QueryRow(ctx, `SELECT site_id FROM work_orders WHERE id=$1`, workOrderID).Scan(&siteID); err != nil {
		return store.Translate(err)
	}
	return RequireSite(ctx, db, siteID)
}

func RequireEvidence(ctx context.Context, db store.DBTX, bundleID string) error {
	var siteID string
	if err := db.QueryRow(ctx, `SELECT site_id FROM evidence_bundles WHERE id=$1`, bundleID).Scan(&siteID); err != nil {
		return store.Translate(err)
	}
	return RequireSite(ctx, db, siteID)
}

func RequireReview(ctx context.Context, db store.DBTX, reviewID string) error {
	var bundleID string
	if err := db.QueryRow(ctx, `SELECT bundle_id FROM reviews WHERE id=$1`, reviewID).Scan(&bundleID); err != nil {
		return store.Translate(err)
	}
	return RequireEvidence(ctx, db, bundleID)
}
