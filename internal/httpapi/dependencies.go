package httpapi

import (
	"log/slog"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/alert"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/grant"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/idempotency"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/monitoring"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/program"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/query"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/review"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/site"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/store"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/technology"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/training"
)

type Dependencies struct {
	Logger       *slog.Logger
	Clock        platform.Clock
	IDs          platform.IDGenerator
	Store        *store.Store
	Idempotency  *idempotency.Service
	Auth         *auth.Service
	Programs     *program.Service
	Queries      *query.Service
	Sites        *site.Service
	Monitoring   *monitoring.Service
	Intervention *intervention.Service
	Evidence     *evidence.Service
	Reviews      *review.Service
	Grants       *grant.Service
	Alerts       *alert.Service
	Technology   *technology.Service
	Training     *training.Service
}
