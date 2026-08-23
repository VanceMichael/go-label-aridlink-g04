package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/VanceMichael/go-base-aridlink-g04/internal/idempotency"
	"github.com/VanceMichael/go-base-aridlink-g04/internal/platform"
)

const maxIdempotentBody = 1 << 20

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header)}
}

func (w *bufferedResponse) Header() http.Header { return w.header }

func (w *bufferedResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *bufferedResponse) Write(payload []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(payload)
}

func (w *bufferedResponse) flush(target http.ResponseWriter) {
	for key, values := range w.header {
		for _, value := range values {
			target.Header().Add(key, value)
		}
	}
	status := w.status
	if status == 0 {
		status = http.StatusNoContent
	}
	target.WriteHeader(status)
	if w.body.Len() > 0 {
		_, _ = target.Write(w.body.Bytes())
	}
}

func idempotentRequests(service *idempotency.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if service == nil || !isMutation(r.Method) || r.URL.Path == "/v1/auth/logout" {
				next.ServeHTTP(w, r)
				return
			}
			requestKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
			if requestKey == "" {
				next.ServeHTTP(w, r)
				return
			}
			actor, err := platform.ActorFrom(r.Context())
			if err != nil {
				writeError(w, r, err)
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotentBody+1))
			if err != nil {
				writeError(w, r, platform.FieldError{Field: "body", Message: "could not read request"})
				return
			}
			if len(body) > maxIdempotentBody {
				writeError(w, r, platform.FieldError{Field: "body", Message: "request exceeds idempotency limit"})
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			claim, err := service.Begin(r.Context(), idempotency.Key{OrganizationID: actor.OrganizationID,
				Method: r.Method, Path: r.URL.Path, RequestKey: requestKey}, idempotency.HashRequest(body))
			if err != nil {
				writeError(w, r, err)
				return
			}
			if claim.Replay != nil {
				w.Header().Set("Idempotency-Replayed", "true")
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(claim.Replay.Status)
				_, _ = w.Write(claim.Replay.Body)
				return
			}
			buffer := newBufferedResponse()
			next.ServeHTTP(buffer, r)
			status := buffer.status
			if status == 0 {
				status = http.StatusNoContent
			}
			if status >= http.StatusOK && status < http.StatusMultipleChoices {
				responseBody := buffer.body.Bytes()
				if len(bytes.TrimSpace(responseBody)) == 0 {
					responseBody = []byte(`{}`)
				}
				if !json.Valid(responseBody) {
					responseBody, _ = json.Marshal(map[string]string{"result": string(responseBody)})
				}
				if r.URL.Path == "/v1/programs" && json.Valid(responseBody) {
					var normalized any
					if err := json.Unmarshal(responseBody, &normalized); err == nil {
						responseBody, _ = json.Marshal(normalized)
					}
				}
				if err := service.Complete(r.Context(), claim, idempotency.Result{Status: status, Body: responseBody}); err != nil {
					writeError(w, r, err)
					return
				}
			} else {
				_ = service.Fail(r.Context(), claim, platform.ErrConflict)
			}
			buffer.flush(w)
		})
	}
}

func isMutation(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
