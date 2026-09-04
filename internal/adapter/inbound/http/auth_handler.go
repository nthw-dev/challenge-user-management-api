package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/respond"
	"github.com/nthw-dev/user-management-api/internal/app"
)

type authHandler struct {
	auth  app.AuthUseCase
	users app.UserUseCase
	log   *slog.Logger
}

// Register is public — it is the way in for the very first user, before anyone has issued them a token.
//
//	@Summary		Sign up
//	@Description	Creates a new user from a name, an email and a password, then returns the user — no token is returned; get one from /auth/login
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			payload	body		createUserRequest	true	"The signup details"
//	@Success		201		{object}	respond.DataEnvelope{data=userResponse}
//	@Header			201		{string}	Location				"/api/v1/users/{id}"
//	@Failure		400		{object}	respond.ErrorEnvelope	"MALFORMED_JSON"
//	@Failure		409		{object}	respond.ErrorEnvelope	"EMAIL_TAKEN"
//	@Failure		413		{object}	respond.ErrorEnvelope	"PAYLOAD_TOO_LARGE"
//	@Failure		422		{object}	respond.ErrorEnvelope	"VALIDATION_ERROR"
//	@Router			/api/v1/auth/register [post]
func (h *authHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decode(r, &req); err != nil {
		respond.Error(w, r, h.log, err)
		return
	}

	// The same use case as POST /users — the only difference is that this route needs no token.
	u, err := h.users.Create(r.Context(), req.toInput())
	if err != nil {
		respond.Error(w, r, h.log, err)
		return
	}

	// Signing up does not return a token straight away; issuing tokens happens in one place, /auth/login,
	// which is easier to audit and to rate-limit.
	w.Header().Set("Location", "/api/v1/users/"+u.ID)
	respond.JSON(w, http.StatusCreated, toUserResponse(u), nil)
}

// Login exchanges an email and password for an access token and a refresh token.
//
//	@Summary		Log in
//	@Description	An email not in the system, a wrong password, and a malformed email all answer identically: 401 INVALID_CREDENTIALS
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			payload	body		loginRequest	true	"Email and password"
//	@Success		200		{object}	respond.DataEnvelope{data=sessionResponse}
//	@Failure		400		{object}	respond.ErrorEnvelope	"MALFORMED_JSON"
//	@Failure		401		{object}	respond.ErrorEnvelope	"INVALID_CREDENTIALS"
//	@Router			/api/v1/auth/login [post]
func (h *authHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decode(r, &req); err != nil {
		respond.Error(w, r, h.log, err)
		return
	}

	session, err := h.auth.Login(r.Context(), req.toInput())
	if err != nil {
		respond.Error(w, r, h.log, err)
		return
	}
	respond.JSON(w, http.StatusOK, toSessionResponse(session), nil)
}

// Refresh rotates one refresh token into a new one, along with a new access token.
//
//	@Summary		Rotate a refresh token
//	@Description	Every rotation invalidates the old token — presenting an already-rotated token wipes every session belonging to that user
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			payload	body		refreshRequest	true	"The most recent refresh token"
//	@Success		200		{object}	respond.DataEnvelope{data=sessionResponse}
//	@Failure		400		{object}	respond.ErrorEnvelope	"MALFORMED_JSON"
//	@Failure		401		{object}	respond.ErrorEnvelope	"UNAUTHORIZED"
//	@Router			/api/v1/auth/refresh [post]
func (h *authHandler) Refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decode(r, &req); err != nil {
		respond.Error(w, r, h.log, err)
		return
	}

	session, err := h.auth.Refresh(r.Context(), req.RefreshToken)
	if err != nil {
		respond.Error(w, r, h.log, err)
		return
	}
	respond.JSON(w, http.StatusOK, toSessionResponse(session), nil)
}
