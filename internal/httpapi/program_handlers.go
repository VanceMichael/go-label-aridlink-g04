package httpapi

import (
	"net/http"
	"time"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/program"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/query"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/site"
	"github.com/go-chi/chi/v5"
)

type versionRequest struct {
	Version int64 `json:"version"`
}

func createProgramHandler(service *program.Service) http.HandlerFunc {
	type request struct {
		OwnerOrganizationID string    `json:"owner_organization_id"`
		Name                string    `json:"name"`
		StartsOn            time.Time `json:"starts_on"`
		EndsOn              time.Time `json:"ends_on"`
		BudgetCents         int64     `json:"budget_cents"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.Create(r.Context(), program.CreateInput{OwnerOrganizationID: input.OwnerOrganizationID, Name: input.Name, StartsOn: input.StartsOn, EndsOn: input.EndsOn, BudgetCents: input.BudgetCents})
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

func getProgramHandler(service *program.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.Get(r.Context(), chi.URLParam(r, "programID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}

func programOverviewHandler(service *query.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		overview, err := service.ProgramOverview(r.Context(), chi.URLParam(r, "programID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, overview)
	}
}

func addPartnershipHandler(service *program.Service) http.HandlerFunc {
	type request struct {
		OrganizationID string `json:"organization_id"`
		Role           string `json:"role"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.AddPartnership(r.Context(), chi.URLParam(r, "programID"), input.OrganizationID, input.Role)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

func activateProgramHandler(service *program.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		updated, err := service.Activate(r.Context(), chi.URLParam(r, "programID"), input.Version)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func closeProgramHandler(service *program.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Close(r.Context(), chi.URLParam(r, "programID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func createSiteHandler(service *site.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input site.CreateInput
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.Create(r.Context(), input)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

func getSiteHandler(service *site.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.Get(r.Context(), chi.URLParam(r, "siteID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, found)
	}
}

func listSitesHandler(service *site.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		found, err := service.ListByProgram(r.Context(), chi.URLParam(r, "programID"))
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": found})
	}
}

func approveSiteHandler(service *site.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		updated, err := service.Approve(r.Context(), chi.URLParam(r, "siteID"), input.Version)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	}
}

func archiveSiteHandler(service *site.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input versionRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		if err := service.Archive(r.Context(), chi.URLParam(r, "siteID"), input.Version); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
