package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/actor"
	"github.com/nthw-dev/user-management-api/internal/adapter/inbound/http/respond"
	"github.com/nthw-dev/user-management-api/internal/app"
	"github.com/nthw-dev/user-management-api/internal/domain/user"
)

type userHandler struct {
	users app.UserUseCase
	log   *slog.Logger
}

// Create creates a user as an already-authenticated caller — unlike /auth/register, which is public.
//
//	@Summary		Create a user
//	@Description	A duplicate email is caught by MongoDB's unique index rather than by a pre-check, so there is no TOCTOU window
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			payload	body		createUserRequest	true	"The new user's details"
//	@Success		201		{object}	respond.DataEnvelope{data=userResponse}
//	@Header			201		{string}	Location				"/api/v1/users/{id}"
//	@Failure		400		{object}	respond.ErrorEnvelope	"MALFORMED_JSON"
//	@Failure		401		{object}	respond.ErrorEnvelope	"UNAUTHORIZED"
//	@Failure		409		{object}	respond.ErrorEnvelope	"EMAIL_TAKEN"
//	@Failure		413		{object}	respond.ErrorEnvelope	"PAYLOAD_TOO_LARGE"
//	@Failure		422		{object}	respond.ErrorEnvelope	"VALIDATION_ERROR"
//	@Router			/api/v1/users [post]
func (h *userHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decode(r, &req); err != nil {
		respond.Error(w, r, h.log, err)
		return
	}

	u, err := h.users.Create(r.Context(), req.toInput())
	if err != nil {
		respond.Error(w, r, h.log, err)
		return
	}

	w.Header().Set("Location", "/api/v1/users/"+u.ID)
	respond.JSON(w, http.StatusCreated, toUserResponse(u), nil)
}

// Get reads a single user by id.
//
//	@Summary	Read a single user
//	@Tags		users
//	@Security	BearerAuth
//	@Produce	json
//	@Param		id	path		string	true	"The user's id (24 hex characters)"
//	@Success	200	{object}	respond.DataEnvelope{data=userResponse}
//	@Failure	401	{object}	respond.ErrorEnvelope	"UNAUTHORIZED"
//	@Failure	404	{object}	respond.ErrorEnvelope	"USER_NOT_FOUND"
//	@Router		/api/v1/users/{id} [get]
func (h *userHandler) Get(w http.ResponseWriter, r *http.Request) {
	u, err := h.users.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respond.Error(w, r, h.log, err)
		return
	}
	respond.JSON(w, http.StatusOK, toUserResponse(u), nil)
}

// List returns users with keyset pagination — send the next_cursor you receive back on the following round.
//
//	@Summary		List users
//	@Description	Paged by cursor rather than offset, so the data volume does not slow the query down, and no row is duplicated or missed when a write lands in between
//	@Tags			users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			limit	query		int		false	"Items per page"	default(20)
//	@Param			cursor	query		string	false	"The next_cursor value from the previous page"
//	@Param			query	query		string	false	"Search by name or email"
//	@Success		200		{object}	respond.DataEnvelope{data=[]userResponse,meta=listMeta}
//	@Failure		401		{object}	respond.ErrorEnvelope	"UNAUTHORIZED"
//	@Failure		422		{object}	respond.ErrorEnvelope	"VALIDATION_ERROR"
//	@Router			/api/v1/users [get]
func (h *userHandler) List(w http.ResponseWriter, r *http.Request) {
	filter, err := listFilterFrom(r)
	if err != nil {
		respond.Error(w, r, h.log, err)
		return
	}

	page, err := h.users.List(r.Context(), filter)
	if err != nil {
		respond.Error(w, r, h.log, err)
		return
	}

	items := make([]userResponse, 0, len(page.Users))
	for i := range page.Users {
		items = append(items, toUserResponse(&page.Users[i]))
	}
	respond.JSON(w, http.StatusOK, items, toListMeta(page))
}

// Update edits a subset of fields — a field that was not sent is left untouched. Only the caller's own account.
//
//	@Summary		Update a user
//	@Description	Every field in the body is optional; send only the ones you want to change. A caller may only change their own account — any other id answers 403 FORBIDDEN, whether or not it exists
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string				true	"The user's id (24 hex characters)"
//	@Param			payload	body		updateUserRequest	true	"The fields to change"
//	@Success		200		{object}	respond.DataEnvelope{data=userResponse}
//	@Failure		400		{object}	respond.ErrorEnvelope	"MALFORMED_JSON"
//	@Failure		401		{object}	respond.ErrorEnvelope	"UNAUTHORIZED"
//	@Failure		403		{object}	respond.ErrorEnvelope	"FORBIDDEN"
//	@Failure		404		{object}	respond.ErrorEnvelope	"USER_NOT_FOUND"
//	@Failure		409		{object}	respond.ErrorEnvelope	"EMAIL_TAKEN"
//	@Failure		413		{object}	respond.ErrorEnvelope	"PAYLOAD_TOO_LARGE"
//	@Failure		422		{object}	respond.ErrorEnvelope	"VALIDATION_ERROR"
//	@Router			/api/v1/users/{id} [patch]
func (h *userHandler) Update(w http.ResponseWriter, r *http.Request) {
	var req updateUserRequest
	if err := decode(r, &req); err != nil {
		respond.Error(w, r, h.log, err)
		return
	}

	u, err := h.users.Update(r.Context(), actor.ID(r.Context()), chi.URLParam(r, "id"), req.toInput())
	if err != nil {
		respond.Error(w, r, h.log, err)
		return
	}
	respond.JSON(w, http.StatusOK, toUserResponse(u), nil)
}

// Delete removes a user — deleting twice yields a 404, because that row really is gone. Only the caller's own account.
//
//	@Summary		Delete a user
//	@Description	Removes the account and revokes every refresh token it holds. A caller may only delete their own account — any other id answers 403 FORBIDDEN
//	@Tags			users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path	string	true	"The user's id (24 hex characters)"
//	@Success		204	"Deleted; no response body"
//	@Failure		401	{object}	respond.ErrorEnvelope	"UNAUTHORIZED"
//	@Failure		403	{object}	respond.ErrorEnvelope	"FORBIDDEN"
//	@Failure		404	{object}	respond.ErrorEnvelope	"USER_NOT_FOUND"
//	@Router			/api/v1/users/{id} [delete]
func (h *userHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.users.Delete(r.Context(), actor.ID(r.Context()), chi.URLParam(r, "id")); err != nil {
		respond.Error(w, r, h.log, err)
		return
	}
	respond.NoContent(w)
}

// listFilterFrom only pulls values out of the query string. Whether a limit is in range, and what the default is,
// are decisions of the use case — the one thing that is a transport concern is that "limit" has to be a number at all.
// A limit that was not sent stays nil, so the use case can tell it apart from one sent as zero.
func listFilterFrom(r *http.Request) (app.ListFilter, error) {
	q := r.URL.Query()
	f := app.ListFilter{Cursor: q.Get("cursor"), Query: q.Get("query")}

	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return f, user.ErrValidation{Field: "limit", Reason: "must be an integer"}
		}
		f.Limit = &n
	}
	return f, nil
}
