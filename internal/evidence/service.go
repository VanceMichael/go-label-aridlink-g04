package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/access"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/audit"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/outbox"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/jackc/pgx/v5"
)

type Bundle struct {
	ID          string     `json:"id"`
	SiteID      string     `json:"site_id"`
	CampaignID  string     `json:"campaign_id,omitempty"`
	WorkOrderID string     `json:"work_order_id,omitempty"`
	Revision    int        `json:"revision"`
	Status      string     `json:"status"`
	Digest      string     `json:"digest,omitempty"`
	SealedAt    *time.Time `json:"sealed_at,omitempty"`
	Version     int64      `json:"version"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type Item struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	ObjectKey string    `json:"object_key"`
	Checksum  string    `json:"checksum"`
	CreatedAt time.Time `json:"created_at"`
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

func (s *Service) Create(ctx context.Context, siteID, campaignID, workOrderID string) (Bundle, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleFieldOfficer)
	if err != nil {
		return Bundle{}, err
	}
	now := s.clock.Now()
	bundle := Bundle{ID: s.ids.New("evd"), SiteID: siteID, CampaignID: campaignID, WorkOrderID: workOrderID, Status: "draft", Version: 1, CreatedAt: now, UpdatedAt: now}
	create := func() error {
		return s.store.WithTx(ctx, func(tx pgx.Tx) error {
			var organizationID string
			if err := tx.QueryRow(ctx, `SELECT organization_id FROM sites WHERE id=$1 FOR UPDATE`, siteID).Scan(&organizationID); err != nil {
				return store.Translate(err)
			}
			if err := platform.RequireOrganization(actor, organizationID); err != nil {
				return err
			}
			if campaignID != "" {
				var linkedSite string
				if err := tx.QueryRow(ctx, `SELECT site_id FROM monitoring_campaigns WHERE id=$1`, campaignID).Scan(&linkedSite); err != nil {
					return store.Translate(err)
				}
				if linkedSite != siteID {
					return platform.ErrConflict
				}
			}
			if workOrderID != "" {
				var linkedSite string
				if err := tx.QueryRow(ctx, `SELECT site_id FROM work_orders WHERE id=$1`, workOrderID).Scan(&linkedSite); err != nil {
					return store.Translate(err)
				}
				if linkedSite != siteID {
					return platform.ErrConflict
				}
			}
			if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision),0)+1 FROM evidence_bundles WHERE site_id=$1`, siteID).Scan(&bundle.Revision); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO evidence_bundles(id,site_id,campaign_id,work_order_id,revision,status,version,created_at,updated_at)
			VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5,'draft',1,$6,$6)`, bundle.ID, siteID, campaignID, workOrderID, bundle.Revision, now)
			if err != nil {
				return store.Translate(err)
			}
			return s.audit.Record(ctx, tx, actor, "evidence.created", "evidence_bundle", bundle.ID, map[string]any{"revision": bundle.Revision})
		})
	}
	for attempt := 0; attempt < 3; attempt++ {
		err = create()
		if err == nil || !errors.Is(err, platform.ErrConflict) || ctx.Err() != nil {
			break
		}
	}
	return bundle, err
}

func (s *Service) AddItems(ctx context.Context, bundleID string, items []Item) error {
	actor, err := platform.RequireRole(ctx, platform.RoleFieldOfficer)
	if err != nil {
		return err
	}
	if len(items) == 0 || len(items) > 100 {
		return platform.FieldError{Field: "items", Message: "between 1 and 100 evidence items required"}
	}
	now := s.clock.Now()
	return s.store.WithTx(ctx, func(tx pgx.Tx) error {
		var status, organizationID string
		if err := tx.QueryRow(ctx, `SELECT b.status,s.organization_id FROM evidence_bundles b JOIN sites s ON s.id=b.site_id WHERE b.id=$1 FOR UPDATE OF b`, bundleID).Scan(&status, &organizationID); err != nil {
			return store.Translate(err)
		}
		if err := platform.RequireOrganization(actor, organizationID); err != nil {
			return err
		}
		if status != "draft" {
			return platform.StateError{Resource: "evidence", Current: status, Target: "add item"}
		}
		allowed := map[string]bool{"observation": true, "remote_asset": true, "field_note": true, "completion_record": true}
		for i := range items {
			item := &items[i]
			if !allowed[item.Kind] || item.ObjectKey == "" || len(item.Checksum) < 16 {
				return platform.FieldError{Field: "evidence_item", Message: "valid kind, object key and checksum required"}
			}
			item.ID = s.ids.New("itm")
			item.CreatedAt = now
			_, err := tx.Exec(ctx, `INSERT INTO evidence_items(id,bundle_id,kind,object_key,checksum,created_at) VALUES($1,$2,$3,$4,$5,$6)`, item.ID, bundleID, item.Kind, item.ObjectKey, item.Checksum, now)
			if err != nil {
				return store.Translate(err)
			}
		}
		return s.audit.Record(ctx, tx, actor, "evidence.items_added", "evidence_bundle", bundleID, map[string]any{"count": len(items)})
	})
}

func (s *Service) Seal(ctx context.Context, bundleID string, expectedVersion int64) (string, error) {
	actor, err := platform.RequireRole(ctx, platform.RoleFieldOfficer)
	if err != nil {
		return "", err
	}
	now := s.clock.Now()
	var digest string
	var status, organizationID string
	if err := s.store.DB().QueryRow(ctx, `SELECT b.status,s.organization_id FROM evidence_bundles b JOIN sites s ON s.id=b.site_id WHERE b.id=$1`, bundleID).Scan(&status, &organizationID); err != nil {
		return "", store.Translate(err)
	}
	if err := platform.RequireOrganization(actor, organizationID); err != nil {
		return "", err
	}
	if status != "draft" {
		return "", platform.StateError{Resource: "evidence", Current: status, Target: "sealed"}
	}
	rows, err := s.store.DB().Query(ctx, `SELECT kind,object_key,checksum FROM evidence_items WHERE bundle_id=$1 ORDER BY kind,object_key,checksum`, bundleID)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0)
	for rows.Next() {
		var kind, key, checksum string
		if err := rows.Scan(&kind, &key, &checksum); err != nil {
			rows.Close()
			return "", err
		}
		parts = append(parts, kind+":"+key+":"+checksum)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", err
	}
	rows.Close()
	if len(parts) == 0 {
		return "", platform.FieldError{Field: "evidence", Message: "cannot seal an empty bundle"}
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	digest = hex.EncodeToString(sum[:])
	tag, err := s.store.DB().Exec(ctx, `UPDATE evidence_bundles SET status='sealed',digest=$3,sealed_at=$4,version=version+1,updated_at=$4 WHERE id=$1 AND version=$2`, bundleID, expectedVersion, digest, now)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() != 1 {
		return "", platform.ErrConflict
	}
	err = s.store.WithTx(ctx, func(tx pgx.Tx) error {
		if err := s.audit.Record(ctx, tx, actor, "evidence.sealed", "evidence_bundle", bundleID, map[string]any{"digest": digest, "items": len(parts)}); err != nil {
			return err
		}
		_, err := s.outbox.Enqueue(ctx, tx, "evidence.sealed", bundleID, map[string]any{"bundle_id": bundleID, "digest": digest})
		return err
	})
	return digest, err
}

func (s *Service) Get(ctx context.Context, id string) (Bundle, error) {
	if err := access.RequireEvidence(ctx, s.store.DB(), id); err != nil {
		return Bundle{}, err
	}
	var b Bundle
	err := s.store.DB().QueryRow(ctx, `SELECT id,site_id,COALESCE(campaign_id,''),COALESCE(work_order_id,''),revision,status,COALESCE(digest,''),sealed_at,version,created_at,updated_at FROM evidence_bundles WHERE id=$1`, id).Scan(&b.ID, &b.SiteID, &b.CampaignID, &b.WorkOrderID, &b.Revision, &b.Status, &b.Digest, &b.SealedAt, &b.Version, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Bundle{}, platform.ErrNotFound
	}
	if err != nil {
		return Bundle{}, fmt.Errorf("get evidence: %w", err)
	}
	return b, nil
}
