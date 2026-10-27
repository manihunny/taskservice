package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"taskservice/docs"
	"taskservice/internal/domain"
	"taskservice/internal/service"
)

const maxBodyBytes = 1 << 20

type Handler struct {
	svc *service.Service
	log *slog.Logger
}

func NewRouter(ctx context.Context, svc *service.Service, log *slog.Logger, rps float64, burst int, ready func(context.Context) error) http.Handler {
	h := &Handler{svc: svc, log: log}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := ready(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /swagger/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(docs.OpenAPI)
	})
	mux.HandleFunc("GET /swagger/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(docs.SwaggerUI)
	})

	mux.HandleFunc("POST /api/v1/register", h.register)
	mux.HandleFunc("POST /api/v1/login", h.login)

	mux.Handle("POST /api/v1/teams", h.authenticated(h.createTeam))
	mux.Handle("GET /api/v1/teams", h.authenticated(h.listTeams))
	mux.Handle("POST /api/v1/teams/{id}/invite", h.authenticated(h.invite))
	mux.Handle("PUT /api/v1/teams/{id}/members/{user_id}", h.authenticated(h.changeRole))
	mux.Handle("GET /api/v1/teams/{id}/stats", h.authenticated(h.teamStats))

	mux.Handle("POST /api/v1/tasks", h.authenticated(h.createTask))
	mux.Handle("GET /api/v1/tasks", h.authenticated(h.listTasks))
	mux.Handle("PUT /api/v1/tasks/{id}", h.authenticated(h.updateTask))
	mux.Handle("GET /api/v1/tasks/{id}/history", h.authenticated(h.taskHistory))
	mux.Handle("POST /api/v1/tasks/{id}/comments", h.authenticated(h.addComment))
	mux.Handle("GET /api/v1/tasks/{id}/comments", h.authenticated(h.listComments))

	limiter := newIPLimiter(ctx, rps, burst)
	return h.logging(limiter.middleware(mux))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		de = domain.NewError(http.StatusInternalServerError, "internal_error", "internal server error")
	}
	writeJSON(w, de.Status, map[string]*domain.Error{"error": de})
}

// fail логирует только неожиданные ошибки: доменные - штатный ответ клиенту.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		h.log.ErrorContext(r.Context(), "request failed", "err", err, "path", r.URL.Path, "user_id", userID(r.Context()))
	}
	writeError(w, err)
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return domain.Invalid("invalid JSON body: " + err.Error())
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, domain.Invalid("invalid " + name)
	}
	return id, nil
}

func queryInt(r *http.Request, name string, def, min, max int64) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < min || n > max {
		return 0, domain.Invalid("invalid " + name)
	}
	return n, nil
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name,omitempty"`
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	u, err := h.svc.Register(r.Context(), in.Email, in.Password, in.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	token, exp, err := h.svc.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": token, "token_type": "Bearer", "expires_at": exp.UTC()})
}

func (h *Handler) createTeam(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	t, err := h.svc.CreateTeam(r.Context(), userID(r.Context()), in.Name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) listTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.svc.ListTeams(r.Context(), userID(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, teams)
}

func (h *Handler) invite(w http.ResponseWriter, r *http.Request) {
	teamID, err := pathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Email string      `json:"email"`
		Role  domain.Role `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.svc.Invite(r.Context(), userID(r.Context()), teamID, in.Email, in.Role)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (h *Handler) changeRole(w http.ResponseWriter, r *http.Request) {
	teamID, err := pathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	memberID, err := pathID(r, "user_id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Role domain.Role `json:"role"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	m, err := h.svc.ChangeRole(r.Context(), userID(r.Context()), teamID, memberID, in.Role)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (h *Handler) teamStats(w http.ResponseWriter, r *http.Request) {
	teamID, err := pathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	st, err := h.svc.TeamStats(r.Context(), userID(r.Context()), teamID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Handler) createTask(w http.ResponseWriter, r *http.Request) {
	var in domain.NewTask
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if in.TeamID <= 0 {
		h.fail(w, r, domain.Invalid("team_id is required"))
		return
	}
	t, err := h.svc.CreateTask(r.Context(), userID(r.Context()), in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) listTasks(w http.ResponseWriter, r *http.Request) {
	teamID, err := queryInt(r, "team_id", 0, 1, 1<<62)
	if err == nil && teamID == 0 {
		err = domain.Invalid("team_id is required")
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	f := domain.TaskFilter{TeamID: teamID}
	if s := r.URL.Query().Get("status"); s != "" {
		st := domain.Status(s)
		if !st.Valid() {
			h.fail(w, r, domain.Invalid("invalid status"))
			return
		}
		f.Status = &st
	}
	if r.URL.Query().Has("assignee_id") {
		a, err := queryInt(r, "assignee_id", 0, 1, 1<<62)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		f.AssigneeID = &a
	}
	limit, err := queryInt(r, "limit", 20, 1, 100)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	offset, err := queryInt(r, "offset", 0, 0, 1_000_000)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	f.Limit, f.Offset = int(limit), int(offset)

	page, err := h.svc.ListTasks(r.Context(), userID(r.Context()), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (h *Handler) updateTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in domain.TaskUpdate
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	t, err := h.svc.UpdateTask(r.Context(), userID(r.Context()), id, in)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) taskHistory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.svc.TaskHistory(r.Context(), userID(r.Context()), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Handler) addComment(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	c, err := h.svc.AddComment(r.Context(), userID(r.Context()), id, in.Content)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) listComments(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	res, err := h.svc.ListComments(r.Context(), userID(r.Context()), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
