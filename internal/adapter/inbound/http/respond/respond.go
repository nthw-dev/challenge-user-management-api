// Package respond writes every response of the REST API.
//
// What an error means is decided once, for every transport, in apierr; this package only decides the HTTP status
// each code maps to and the envelope it travels in — so a handler merely passes the error upward.
package respond

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/apierr"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/bearer"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/reqctx"
)

// Errors that can only arise at the HTTP layer, and therefore belong to neither the domain nor apierr.
var (
	ErrMalformedJSON = errors.New("the body sent is not valid JSON")
	ErrPayloadTooBig = errors.New("the body sent is larger than allowed")

	ErrRouteNotFound    = errors.New("the requested route was not found")
	ErrMethodNotAllowed = errors.New("this method is not supported on this route")
)

// statusOf is the whole of this transport's error table: one HTTP status per shared code.
var statusOf = map[apierr.Code]int{
	apierr.CodeValidation:         http.StatusUnprocessableEntity,
	apierr.CodeUserNotFound:       http.StatusNotFound,
	apierr.CodeEmailTaken:         http.StatusConflict,
	apierr.CodeInvalidCredentials: http.StatusUnauthorized,
	apierr.CodeUnauthorized:       http.StatusUnauthorized,
	apierr.CodeForbidden:          http.StatusForbidden,
	apierr.CodeInternal:           http.StatusInternalServerError,
}

type FieldIssue struct {
	Field string `json:"field"`
	Issue string `json:"issue"`
}

// ErrorBody, ErrorEnvelope and DataEnvelope are exported because the OpenAPI annotations
// reference them directly — so the spec swag generates has the same shape as the payload actually sent, rather than a copy that drifts on its own.
type ErrorBody struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Details []FieldIssue `json:"details,omitempty"`
}

type ErrorEnvelope struct {
	Error     ErrorBody `json:"error"`
	RequestID string    `json:"request_id,omitempty"`
}

type DataEnvelope struct {
	Data any `json:"data"`
	Meta any `json:"meta,omitempty"`
}

// JSON always writes a successful payload in {"data": ...} form.
func JSON(w http.ResponseWriter, status int, data any, meta any) {
	write(w, status, DataEnvelope{Data: data, Meta: meta})
}

// Plain writes a payload with no envelope — for the probes, whose readers expect a flat object.
func Plain(w http.ResponseWriter, status int, payload any) { write(w, status, payload) }

func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Error turns an error flowing up from the inner layers into a response, per the agreed table.
func Error(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	status, body := classify(err)

	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", bearer.Scheme+` realm="user-service"`)
	}
	if status >= http.StatusInternalServerError {
		// The error's details go into the log only; they are never sent back to the caller.
		// The caller gets a request_id to quote, which is enough to follow up without exposing the internal structure.
		log.LogAttrs(r.Context(), slog.LevelError, "unhandled_error",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("request_id", reqctx.RequestID(r.Context())),
			slog.Any("err", err),
		)
	}

	write(w, status, ErrorEnvelope{Error: body, RequestID: reqctx.RequestID(r.Context())})
}

func classify(err error) (int, ErrorBody) {
	// The HTTP-only failures first — they never reach the core, so apierr does not know them.
	switch {
	case errors.Is(err, ErrMalformedJSON):
		return http.StatusBadRequest, ErrorBody{Code: "MALFORMED_JSON", Message: err.Error()}
	case errors.Is(err, ErrPayloadTooBig):
		return http.StatusRequestEntityTooLarge, ErrorBody{Code: "PAYLOAD_TOO_LARGE", Message: err.Error()}
	case errors.Is(err, ErrRouteNotFound):
		return http.StatusNotFound, ErrorBody{Code: "NOT_FOUND", Message: err.Error()}
	case errors.Is(err, ErrMethodNotAllowed):
		return http.StatusMethodNotAllowed, ErrorBody{Code: "METHOD_NOT_ALLOWED", Message: err.Error()}
	}

	p := apierr.Classify(err)
	status, known := statusOf[p.Code]
	if !known {
		status = http.StatusInternalServerError
	}
	return status, ErrorBody{Code: string(p.Code), Message: p.Message, Details: fieldIssues(p.Fields)}
}

func fieldIssues(fields []apierr.FieldIssue) []FieldIssue {
	if len(fields) == 0 {
		return nil
	}
	out := make([]FieldIssue, 0, len(fields))
	for _, f := range fields {
		out = append(out, FieldIssue{Field: f.Field, Issue: f.Issue})
	}
	return out
}

func write(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)

	// If encoding fails here the header has already gone out and nothing can be fixed.
	// The only thing we can do is not bring the whole process down.
	_ = json.NewEncoder(w).Encode(payload)
}
