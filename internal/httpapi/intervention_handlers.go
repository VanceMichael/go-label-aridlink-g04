package httpapi

import (
	"net/http"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/intervention"
	"github.com/go-chi/chi/v5"
)

func createPlanHandler(service *intervention.Service) http.HandlerFunc {
	type request struct {
		SiteID             string                  `json:"site_id"`
		Title              string                  `json:"title"`
		EstimatedCostCents int64                   `json:"estimated_cost_cents"`
		WorkOrders         []intervention.WorkSpec `json:"work_orders"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		plan, orders, err := service.CreatePlan(r.Context(), input.SiteID, input.Title, input.EstimatedCostCents, input.WorkOrders)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"plan": plan, "work_orders": orders})
	}
}
func approvePlanHandler(service *intervention.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.ApprovePlan(r.Context(), chi.URLParam(r, "planID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func claimWorkHandler(service *intervention.Service) http.HandlerFunc {
	type request struct {
		OwnerToken   string `json:"owner_token"`
		LeaseSeconds int    `json:"lease_seconds"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		claimed, err := service.Claim(r.Context(), input.OwnerToken, time.Duration(input.LeaseSeconds)*time.Second)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, claimed)
	}
}
func getWorkOrderHandler(service *intervention.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.GetOrder(r.Context(), chi.URLParam(r, "workOrderID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}
func renewWorkHandler(service *intervention.Service) http.HandlerFunc {
	type request struct {
		OwnerToken   string `json:"owner_token"`
		LeaseSeconds int    `json:"lease_seconds"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Renew(r.Context(), chi.URLParam(r, "workOrderID"), input.OwnerToken, time.Duration(input.LeaseSeconds)*time.Second); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func startWorkHandler(service *intervention.Service) http.HandlerFunc {
	type request struct {
		OwnerToken string `json:"owner_token"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Start(r.Context(), chi.URLParam(r, "workOrderID"), input.OwnerToken); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func completeWorkHandler(service *intervention.Service) http.HandlerFunc {
	type request struct {
		OwnerToken       string `json:"owner_token"`
		Summary          string `json:"summary"`
		EvidenceBundleID string `json:"evidence_bundle_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Complete(r.Context(), chi.URLParam(r, "workOrderID"), input.OwnerToken, input.Summary, input.EvidenceBundleID); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
