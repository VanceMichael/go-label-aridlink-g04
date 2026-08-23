package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func NewRouter(deps Dependencies) http.Handler {
	router := chi.NewRouter()
	router.Use(securityHeaders)
	router.Use(requestIDs(deps.IDs))
	router.Use(recoverer(deps.Logger))
	router.Use(accessLog(deps.Logger, deps.Clock))
	router.Use(middleware.RealIP)
	router.Use(middleware.Timeout(defaultRequestTimeout))

	router.Get("/healthz", healthHandler())
	router.Get("/readyz", readyHandler(deps.Store))
	router.Post("/v1/auth/login", loginHandler(deps.Auth))

	router.Group(func(api chi.Router) {
		api.Use(authenticate(deps.Auth))
		api.Use(idempotentRequests(deps.Idempotency))
		api.Post("/v1/auth/logout", logoutHandler(deps.Auth))
		api.Post("/v1/organizations", createOrganizationHandler(deps.Auth))
		api.Post("/v1/organizations/{organizationID}/users", createUserHandler(deps.Auth))

		api.Post("/v1/programs", createProgramHandler(deps.Programs))
		api.Get("/v1/programs/{programID}", getProgramHandler(deps.Programs))
		api.Get("/v1/programs/{programID}/overview", programOverviewHandler(deps.Queries))
		api.Post("/v1/programs/{programID}/partnerships", addPartnershipHandler(deps.Programs))
		api.Post("/v1/programs/{programID}/activate", activateProgramHandler(deps.Programs))
		api.Post("/v1/programs/{programID}/close", closeProgramHandler(deps.Programs))

		api.Post("/v1/sites", createSiteHandler(deps.Sites))
		api.Get("/v1/sites/{siteID}", getSiteHandler(deps.Sites))
		api.Get("/v1/programs/{programID}/sites", listSitesHandler(deps.Sites))
		api.Post("/v1/sites/{siteID}/approve", approveSiteHandler(deps.Sites))
		api.Post("/v1/sites/{siteID}/archive", archiveSiteHandler(deps.Sites))

		api.Post("/v1/monitoring/campaigns", planCampaignHandler(deps.Monitoring))
		api.Get("/v1/monitoring/campaigns/{campaignID}", getCampaignHandler(deps.Monitoring))
		api.Post("/v1/monitoring/campaigns/{campaignID}/start", startCampaignHandler(deps.Monitoring))
		api.Post("/v1/monitoring/campaigns/{campaignID}/observations", addObservationsHandler(deps.Monitoring))
		api.Post("/v1/monitoring/campaigns/{campaignID}/submit", submitCampaignHandler(deps.Monitoring))

		api.Post("/v1/intervention/plans", createPlanHandler(deps.Intervention))
		api.Post("/v1/intervention/plans/{planID}/approve", approvePlanHandler(deps.Intervention))
		api.Post("/v1/intervention/work-orders/claim", claimWorkHandler(deps.Intervention))
		api.Get("/v1/intervention/work-orders/{workOrderID}", getWorkOrderHandler(deps.Intervention))
		api.Post("/v1/intervention/work-orders/{workOrderID}/renew", renewWorkHandler(deps.Intervention))
		api.Post("/v1/intervention/work-orders/{workOrderID}/start", startWorkHandler(deps.Intervention))
		api.Post("/v1/intervention/work-orders/{workOrderID}/complete", completeWorkHandler(deps.Intervention))

		api.Post("/v1/evidence/bundles", createEvidenceHandler(deps.Evidence))
		api.Get("/v1/evidence/bundles/{bundleID}", getEvidenceHandler(deps.Evidence))
		api.Post("/v1/evidence/bundles/{bundleID}/items", addEvidenceItemsHandler(deps.Evidence))
		api.Post("/v1/evidence/bundles/{bundleID}/seal", sealEvidenceHandler(deps.Evidence))
		api.Post("/v1/evidence/bundles/{bundleID}/reviews", assignReviewHandler(deps.Reviews))
		api.Get("/v1/reviews/{reviewID}", getReviewHandler(deps.Reviews))
		api.Post("/v1/reviews/{reviewID}/conclude", concludeReviewHandler(deps.Reviews))

		api.Post("/v1/grants/milestones", createMilestoneHandler(deps.Grants))
		api.Post("/v1/grants/milestones/{milestoneID}/eligible", eligibleMilestoneHandler(deps.Grants))
		api.Post("/v1/grants/milestones/{milestoneID}/reserve", reserveMilestoneHandler(deps.Grants))
		api.Post("/v1/grants/milestones/{milestoneID}/disburse", disburseMilestoneHandler(deps.Grants))

		api.Post("/v1/alerts", createAlertHandler(deps.Alerts))
		api.Get("/v1/alerts/{alertID}", getAlertHandler(deps.Alerts))
		api.Post("/v1/alerts/{alertID}/publish", publishAlertHandler(deps.Alerts))
		api.Post("/v1/alerts/{alertID}/acknowledge", acknowledgeAlertHandler(deps.Alerts))

		api.Post("/v1/technology/transfers", proposeTechnologyHandler(deps.Technology))
		api.Get("/v1/technology/transfers/{transferID}", getTechnologyHandler(deps.Technology))
		api.Post("/v1/technology/transfers/{transferID}/approve", approveTechnologyHandler(deps.Technology))
		api.Post("/v1/technology/transfers/{transferID}/deploy", deployTechnologyHandler(deps.Technology))

		api.Post("/v1/training/cohorts", scheduleCohortHandler(deps.Training))
		api.Get("/v1/training/cohorts/{cohortID}", getCohortHandler(deps.Training))
		api.Post("/v1/training/cohorts/{cohortID}/enrollment", openEnrollmentHandler(deps.Training))
		api.Post("/v1/training/cohorts/{cohortID}/registrations", registerCohortHandler(deps.Training))
		api.Post("/v1/training/cohorts/{cohortID}/start", startCohortHandler(deps.Training))
		api.Post("/v1/training/cohorts/{cohortID}/attendance", attendanceHandler(deps.Training))
		api.Post("/v1/training/cohorts/{cohortID}/complete", completeCohortHandler(deps.Training))
	})
	return router
}
