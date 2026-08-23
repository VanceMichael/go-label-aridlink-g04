package httpapi

import (
	"net/http"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/alert"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/technology"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/training"
	"github.com/go-chi/chi/v5"
)

func createAlertHandler(service *alert.Service) http.HandlerFunc {
	type request struct {
		ProgramID string    `json:"program_id"`
		Kind      string    `json:"kind"`
		Severity  string    `json:"severity"`
		StartsAt  time.Time `json:"starts_at"`
		EndsAt    time.Time `json:"ends_at"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.Create(r.Context(), input.ProgramID, input.Kind, input.Severity, input.StartsAt, input.EndsAt)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}
func getAlertHandler(service *alert.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.Get(r.Context(), chi.URLParam(r, "alertID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}
func publishAlertHandler(service *alert.Service) http.HandlerFunc {
	type request struct {
		SiteIDs []string `json:"site_ids"`
		Version int64    `json:"version"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Publish(r.Context(), chi.URLParam(r, "alertID"), input.SiteIDs, input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func acknowledgeAlertHandler(service *alert.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := service.Acknowledge(r.Context(), chi.URLParam(r, "alertID")); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func proposeTechnologyHandler(service *technology.Service) http.HandlerFunc {
	type request struct {
		ProgramID         string `json:"program_id"`
		SiteID            string `json:"site_id"`
		Title             string `json:"title"`
		TechnologyVersion string `json:"technology_version"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.Propose(r.Context(), input.ProgramID, input.SiteID, input.Title, input.TechnologyVersion)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}
func getTechnologyHandler(service *technology.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.Get(r.Context(), chi.URLParam(r, "transferID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}
func approveTechnologyHandler(service *technology.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Approve(r.Context(), chi.URLParam(r, "transferID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func deployTechnologyHandler(service *technology.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Deploy(r.Context(), chi.URLParam(r, "transferID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func scheduleCohortHandler(service *training.Service) http.HandlerFunc {
	type request struct {
		ProgramID string    `json:"program_id"`
		Title     string    `json:"title"`
		Capacity  int       `json:"capacity"`
		StartsAt  time.Time `json:"starts_at"`
		EndsAt    time.Time `json:"ends_at"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.Schedule(r.Context(), input.ProgramID, input.Title, input.Capacity, input.StartsAt, input.EndsAt)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}
func getCohortHandler(service *training.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.Get(r.Context(), chi.URLParam(r, "cohortID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}
func openEnrollmentHandler(service *training.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.OpenEnrollment(r.Context(), chi.URLParam(r, "cohortID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func registerCohortHandler(service *training.Service) http.HandlerFunc {
	type request struct {
		UserID string `json:"user_id"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Register(r.Context(), chi.URLParam(r, "cohortID"), input.UserID); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func startCohortHandler(service *training.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Start(r.Context(), chi.URLParam(r, "cohortID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func attendanceHandler(service *training.Service) http.HandlerFunc {
	type request struct {
		Records []training.Attendance `json:"records"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.RecordAttendance(r.Context(), chi.URLParam(r, "cohortID"), input.Records); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func completeCohortHandler(service *training.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Complete(r.Context(), chi.URLParam(r, "cohortID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
