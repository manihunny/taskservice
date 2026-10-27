package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"taskservice/internal/auth"
	"taskservice/internal/cache"
	"taskservice/internal/domain"
	"taskservice/internal/store"
)

type Service struct {
	store *store.Store
	cache *cache.TaskCache
	jwt   *auth.JWT
	log   *slog.Logger
	now   func() time.Time
}

func New(st *store.Store, c *cache.TaskCache, j *auth.JWT, log *slog.Logger) *Service {
	return &Service{store: st, cache: c, jwt: j, log: log, now: func() time.Time { return time.Now().UTC() }}
}

// memberRole скрывает существование чужих команд: не участник получает 404, а не 403.
func memberRole(ctx context.Context, r *store.Repo, teamID, userID int64) (domain.Role, error) {
	role, err := r.MemberRole(ctx, teamID, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return "", domain.NewError(domain.ErrNotFound.Status, domain.ErrNotFound.Code, "team not found")
	}
	return role, err
}

func (s *Service) Register(ctx context.Context, email, password, name string) (*domain.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	name = strings.TrimSpace(name)
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		return nil, domain.Invalid("invalid email")
	}
	if len(password) < 8 || len(password) > 72 {
		return nil, domain.Invalid("password must be 8-72 characters")
	}
	if name == "" {
		return nil, domain.Invalid("name is required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &domain.User{Email: email, Name: name, PasswordHash: string(hash), CreatedAt: s.now()}
	if err := s.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (string, time.Time, error) {
	u, err := s.store.UserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
	if errors.Is(err, domain.ErrNotFound) {
		return "", time.Time{}, domain.ErrInvalidCredentials
	}
	if err != nil {
		return "", time.Time{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return "", time.Time{}, domain.ErrInvalidCredentials
	}
	return s.jwt.Issue(u.ID)
}

func (s *Service) Authenticate(token string) (int64, error) { return s.jwt.Parse(token) }

func (s *Service) CreateTeam(ctx context.Context, actor int64, name string) (*domain.TeamWithRole, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 255 {
		return nil, domain.Invalid("name is required (max 255 characters)")
	}
	t := domain.Team{Name: name, CreatedBy: actor, CreatedAt: s.now()}
	err := s.store.InTx(ctx, func(r *store.Repo) error {
		if err := r.CreateTeam(ctx, &t); err != nil {
			return err
		}
		return r.AddMember(ctx, t.ID, actor, domain.RoleOwner)
	})
	if err != nil {
		return nil, err
	}
	return &domain.TeamWithRole{Team: t, Role: domain.RoleOwner}, nil
}

func (s *Service) ListTeams(ctx context.Context, actor int64) ([]domain.TeamWithRole, error) {
	return s.store.ListTeams(ctx, actor)
}

func (s *Service) Invite(ctx context.Context, actor, teamID int64, email string, role domain.Role) (*domain.TeamMember, error) {
	if role == "" {
		role = domain.RoleMember
	}
	m := &domain.TeamMember{TeamID: teamID, Role: role}
	err := s.store.InTx(ctx, func(r *store.Repo) error {
		actorRole, err := memberRole(ctx, r, teamID, actor)
		if err != nil {
			return err
		}
		if err := CanInvite(actorRole, role); err != nil {
			return err
		}
		u, err := r.UserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
		if errors.Is(err, domain.ErrNotFound) {
			return domain.NewError(domain.ErrNotFound.Status, domain.ErrNotFound.Code, "user not found")
		}
		if err != nil {
			return err
		}
		m.UserID = u.ID
		return r.AddMember(ctx, teamID, u.ID, role)
	})
	if err != nil {
		return nil, err
	}
	return m, nil
}

func (s *Service) ChangeRole(ctx context.Context, actor, teamID, userID int64, role domain.Role) (*domain.TeamMember, error) {
	err := s.store.InTx(ctx, func(r *store.Repo) error {
		actorRole, err := memberRole(ctx, r, teamID, actor)
		if err != nil {
			return err
		}
		current, err := r.MemberRole(ctx, teamID, userID)
		if err != nil {
			return err
		}
		if err := CanChangeRole(actorRole, current, role); err != nil {
			return err
		}
		return r.SetMemberRole(ctx, teamID, userID, role)
	})
	if err != nil {
		return nil, err
	}
	return &domain.TeamMember{TeamID: teamID, UserID: userID, Role: role}, nil
}

// ensureAssignee: назначать можно только участника той же команды.
func ensureAssignee(ctx context.Context, r *store.Repo, teamID int64, assignee *int64) error {
	if assignee == nil {
		return nil
	}
	_, err := r.MemberRole(ctx, teamID, *assignee)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Invalid("assignee must be a member of the team")
	}
	return err
}

func (s *Service) CreateTask(ctx context.Context, actor int64, in domain.NewTask) (*domain.Task, error) {
	in.Title = strings.TrimSpace(in.Title)
	if err := validateTitle(in.Title); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = domain.StatusTodo
	}
	if !in.Status.Valid() {
		return nil, domain.Invalid("status must be one of todo, in_progress, done")
	}

	now := s.now()
	t := &domain.Task{
		TeamID: in.TeamID, Title: in.Title, Description: in.Description, Status: in.Status,
		CreatedBy: actor, AssigneeID: in.AssigneeID, CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if t.Status == domain.StatusDone {
		t.ClosedAt = &now
	}

	err := s.store.InTx(ctx, func(r *store.Repo) error {
		if _, err := memberRole(ctx, r, in.TeamID, actor); err != nil {
			return err
		}
		if err := ensureAssignee(ctx, r, in.TeamID, in.AssigneeID); err != nil {
			return err
		}
		if err := r.InsertTask(ctx, t); err != nil {
			return err
		}
		return r.InsertHistory(ctx, t.ID, actor, domain.Changes{
			"title":       {New: t.Title},
			"description": {New: t.Description},
			"status":      {New: t.Status},
			"assignee_id": {New: t.AssigneeID},
		}, now)
	})
	if err != nil {
		return nil, err
	}
	s.cache.Invalidate(ctx, t.TeamID)
	return t, nil
}

func (s *Service) UpdateTask(ctx context.Context, actor, taskID int64, upd domain.TaskUpdate) (*domain.Task, error) {
	var (
		result  *domain.Task
		changed bool
	)
	err := s.store.InTx(ctx, func(r *store.Repo) error {
		// FOR UPDATE сериализует конкурентные апдейты: дифф и история строятся от актуальной строки
		cur, err := r.GetTask(ctx, taskID, true)
		if err != nil {
			return err
		}
		role, err := r.MemberRole(ctx, cur.TeamID, actor)
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if upd.Version != nil && *upd.Version != cur.Version {
			return domain.ErrVersionConflict
		}

		next, changes, err := ApplyTaskUpdate(cur, upd, s.now())
		if err != nil {
			return err
		}
		// Права проверяем и для пустого апдейта, чтобы он не стал способом читать чужие задачи
		if err := AuthorizeTaskChange(role, actor, cur, changes); err != nil {
			return err
		}
		if len(changes) == 0 {
			result = cur
			return nil
		}
		if _, ok := changes["assignee_id"]; ok {
			if err := ensureAssignee(ctx, r, cur.TeamID, next.AssigneeID); err != nil {
				return err
			}
		}
		if err := r.UpdateTask(ctx, next, cur.Version); err != nil {
			return err
		}
		if err := r.InsertHistory(ctx, cur.ID, actor, changes, next.UpdatedAt); err != nil {
			return err
		}
		result, changed = next, true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if changed {
		s.cache.Invalidate(ctx, result.TeamID)
	}
	return result, nil
}

func (s *Service) ListTasks(ctx context.Context, actor int64, f domain.TaskFilter) (*domain.TaskPage, error) {
	if _, err := memberRole(ctx, s.store.Repo, f.TeamID, actor); err != nil {
		return nil, err
	}

	ver, cacheOK := s.cache.Version(ctx, f.TeamID)
	key := cache.ListKey(f, ver)
	if cacheOK {
		if b, ok := s.cache.Get(ctx, key); ok {
			var page domain.TaskPage
			if err := json.Unmarshal(b, &page); err == nil {
				return &page, nil
			}
		}
	}

	items, total, err := s.store.ListTasks(ctx, f)
	if err != nil {
		return nil, err
	}
	page := &domain.TaskPage{Items: items, Total: total, Limit: f.Limit, Offset: f.Offset}
	if cacheOK {
		if b, err := json.Marshal(page); err == nil {
			s.cache.Set(ctx, key, b)
		}
	}
	return page, nil
}

// taskForMember отдаёт задачу только участнику её команды, иначе 404.
func (s *Service) taskForMember(ctx context.Context, actor, taskID int64) (*domain.Task, error) {
	t, err := s.store.GetTask(ctx, taskID, false)
	if err != nil {
		return nil, err
	}
	if _, err := s.store.MemberRole(ctx, t.TeamID, actor); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Service) TaskHistory(ctx context.Context, actor, taskID int64) ([]domain.HistoryEntry, error) {
	if _, err := s.taskForMember(ctx, actor, taskID); err != nil {
		return nil, err
	}
	return s.store.ListHistory(ctx, taskID)
}

func (s *Service) AddComment(ctx context.Context, actor, taskID int64, content string) (*domain.Comment, error) {
	content = strings.TrimSpace(content)
	if content == "" || len(content) > 10000 {
		return nil, domain.Invalid("content is required (max 10000 bytes)")
	}
	if _, err := s.taskForMember(ctx, actor, taskID); err != nil {
		return nil, err
	}
	c := &domain.Comment{TaskID: taskID, UserID: actor, Content: content, CreatedAt: s.now()}
	if err := s.store.InsertComment(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) ListComments(ctx context.Context, actor, taskID int64) ([]domain.Comment, error) {
	if _, err := s.taskForMember(ctx, actor, taskID); err != nil {
		return nil, err
	}
	return s.store.ListComments(ctx, taskID)
}

func (s *Service) TeamStats(ctx context.Context, actor, teamID int64) (*domain.TeamStats, error) {
	role, err := memberRole(ctx, s.store.Repo, teamID, actor)
	if err != nil {
		return nil, err
	}
	if !CanViewStats(role) {
		return nil, domain.ErrForbidden
	}
	return s.store.TeamStats(ctx, teamID, s.now())
}
