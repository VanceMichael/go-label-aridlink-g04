package httpapi

import (
	"net/http"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/evidence"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/review"
	"github.com/go-chi/chi/v5"
)

func createEvidenceHandler(service *evidence.Service) http.HandlerFunc {
	type request struct {
		SiteID      string `json:"site_id"`
		CampaignID  string `json:"campaign_id"`
		WorkOrderID string `json:"work_order_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.Create(r.Context(), input.SiteID, input.CampaignID, input.WorkOrderID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}
func getEvidenceHandler(service *evidence.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.Get(r.Context(), chi.URLParam(r, "bundleID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}
func addEvidenceItemsHandler(service *evidence.Service) http.HandlerFunc {
	type request struct {
		Items []evidence.Item `json:"items"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.AddItems(r.Context(), chi.URLParam(r, "bundleID"), input.Items); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func sealEvidenceHandler(service *evidence.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		digest, err := service.Seal(r.Context(), chi.URLParam(r, "bundleID"), input.Version)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"digest": digest})
	}
}
func assignReviewHandler(service *review.Service) http.HandlerFunc {
	type request struct {
		ReviewerID string `json:"reviewer_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.Assign(r.Context(), chi.URLParam(r, "bundleID"), input.ReviewerID)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}
func getReviewHandler(service *review.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.Get(r.Context(), chi.URLParam(r, "reviewID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}
func concludeReviewHandler(service *review.Service) http.HandlerFunc {
	type request struct {
		Decision   string `json:"decision"`
		Conclusion string `json:"conclusion"`
		Version    int64  `json:"version"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Conclude(r.Context(), chi.URLParam(r, "reviewID"), input.Decision, input.Conclusion, input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
