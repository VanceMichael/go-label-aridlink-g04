package httpapi

import (
	"net/http"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/grant"
	"github.com/go-chi/chi/v5"
)

func createMilestoneHandler(service *grant.Service) http.HandlerFunc {
	type request struct {
		ProgramID        string `json:"program_id"`
		SiteID           string `json:"site_id"`
		RequiredBundleID string `json:"required_bundle_id"`
		Title            string `json:"title"`
		AmountCents      int64  `json:"amount_cents"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.CreateMilestone(r.Context(), input.ProgramID, input.SiteID, input.RequiredBundleID, input.Title, input.AmountCents)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}
func eligibleMilestoneHandler(service *grant.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.MarkEligible(r.Context(), chi.URLParam(r, "milestoneID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func reserveMilestoneHandler(service *grant.Service) http.HandlerFunc {
	type request struct {
		TTLSeconds int `json:"ttl_seconds"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		reserved, err := service.Reserve(r.Context(), chi.URLParam(r, "milestoneID"), time.Duration(input.TTLSeconds)*time.Second)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, reserved)
	}
}
func disburseMilestoneHandler(service *grant.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.Disburse(r.Context(), chi.URLParam(r, "milestoneID")); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
