package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
)

type Entry struct {
	ID             string         `json:"id"`
	OrganizationID string         `json:"organization_id"`
	ActorID        string         `json:"actor_id"`
	Action         string         `json:"action"`
	ResourceType   string         `json:"resource_type"`
	ResourceID     string         `json:"resource_id"`
	Details        map[string]any `json:"details"`
	CreatedAt      time.Time      `json:"created_at"`
}

type Writer struct {
	ids   platform.IDGenerator
	clock platform.Clock
}

func NewWriter(ids platform.IDGenerator, clock platform.Clock) *Writer {
	return &Writer{ids: ids, clock: clock}
}

func (w *Writer) Record(ctx context.Context, db store.DBTX, actor platform.Actor, action, resourceType, resourceID string, details map[string]any) error {
	if err := platform.CheckContext(ctx); err != nil {
		return err
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("marshal audit details: %w", err)
	}
	_, err = db.Exec(ctx, `INSERT INTO audit_entries
		(id, organization_id, actor_id, action, resource_type, resource_id, details, created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
		w.ids.New("aud"), nullable(actor.OrganizationID), nullable(actor.UserID), action, resourceType, resourceID, payload, w.clock.Now())
	if err != nil {
		return fmt.Errorf("record audit entry: %w", store.Translate(err))
	}
	return nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func ListForResource(ctx context.Context, db store.DBTX, resourceType, resourceID string, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := db.Query(ctx, `SELECT id, COALESCE(organization_id,''), COALESCE(actor_id,''), action,
		resource_type, resource_id, details, created_at
		FROM audit_entries WHERE resource_type=$1 AND resource_id=$2
		ORDER BY created_at, id LIMIT $3`, resourceType, resourceID, limit)
	if err != nil {
		return nil, fmt.Errorf("query audit entries: %w", store.Translate(err))
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		var entry Entry
		var payload []byte
		if err := rows.Scan(&entry.ID, &entry.OrganizationID, &entry.ActorID, &entry.Action,
			&entry.ResourceType, &entry.ResourceID, &payload, &entry.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit entry: %w", err)
		}
		if err := json.Unmarshal(payload, &entry.Details); err != nil {
			return nil, fmt.Errorf("decode audit entry %s: %w", entry.ID, err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit entries: %w", err)
	}
	return entries, nil
}
