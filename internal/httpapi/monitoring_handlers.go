package httpapi

import (
	"net/http"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/monitoring"
	"github.com/go-chi/chi/v5"
)

func planCampaignHandler(service *monitoring.Service) http.HandlerFunc {
	type request struct {
		SiteID   string `json:"site_id"`
		CycleKey string `json:"cycle_key"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.Plan(r.Context(), input.SiteID, input.CycleKey)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}
func getCampaignHandler(service *monitoring.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.Get(r.Context(), chi.URLParam(r, "campaignID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}
func startCampaignHandler(service *monitoring.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Start(r.Context(), chi.URLParam(r, "campaignID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func addObservationsHandler(service *monitoring.Service) http.HandlerFunc {
	type request struct {
		Items []monitoring.Observation `json:"items"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.AddObservations(r.Context(), chi.URLParam(r, "campaignID"), input.Items); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func submitCampaignHandler(service *monitoring.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Submit(r.Context(), chi.URLParam(r, "campaignID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
