package httpapi

import (
	"net/http"
	"strings"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/auth"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
	"github.com/go-chi/chi/v5"
)

func loginHandler(service *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input auth.Credentials
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		result, err := service.Login(r.Context(), input)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func logoutHandler(service *auth.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
		if len(parts) != 2 {
			writeError(w, r, platform.ErrUnauthorized)
			return
		}
		if err := service.Logout(r.Context(), strings.TrimSpace(parts[1])); err != nil {
			writeError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func createOrganizationHandler(service *auth.Service) http.HandlerFunc {
	type request struct {
		Name        string `json:"name"`
		CountryCode string `json:"country_code"`
		Kind        string `json:"kind"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.CreateOrganization(r.Context(), input.Name, input.CountryCode, input.Kind)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}

func createUserHandler(service *auth.Service) http.HandlerFunc {
	type request struct {
		Email    string        `json:"email"`
		Password string        `json:"password"`
		Role     platform.Role `json:"role"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var input request
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, r, err)
			return
		}
		created, err := service.CreateUser(r.Context(), chi.URLParam(r, "organizationID"), input.Email, input.Password, input.Role)
		if err != nil {
			writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
	}
}
