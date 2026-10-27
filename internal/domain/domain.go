package domain

import (
	"encoding/json"
	"time"
)

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

func (r Role) Valid() bool {
	return r == RoleOwner || r == RoleAdmin || r == RoleMember
}

// IsManager - роли с полным доступом к задачам и аналитике команды.
func (r Role) IsManager() bool {
	return r == RoleOwner || r == RoleAdmin
}

type Status string

const (
	StatusTodo       Status = "todo"
	StatusInProgress Status = "in_progress"
	StatusDone       Status = "done"
)

var Statuses = []Status{StatusTodo, StatusInProgress, StatusDone}

func (s Status) Valid() bool {
	return s == StatusTodo || s == StatusInProgress || s == StatusDone
}

type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type Team struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedBy int64     `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type TeamWithRole struct {
	Team
	Role Role `json:"role"`
}

type TeamMember struct {
	TeamID int64 `json:"team_id"`
	UserID int64 `json:"user_id"`
	Role   Role  `json:"role"`
}

type Task struct {
	ID          int64      `json:"id"`
	TeamID      int64      `json:"team_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      Status     `json:"status"`
	CreatedBy   int64      `json:"created_by"`
	AssigneeID  *int64     `json:"assignee_id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	Version     int64      `json:"version"`
}

type TaskFilter struct {
	TeamID     int64
	Status     *Status
	AssigneeID *int64
	Limit      int
	Offset     int
}

type TaskPage struct {
	Items  []Task `json:"items"`
	Total  int    `json:"total"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type NewTask struct {
	TeamID      int64  `json:"team_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      Status `json:"status"`
	AssigneeID  *int64 `json:"assignee_id"`
}

// OptionalInt64 отличает отсутствующее поле от явного null (снять исполнителя).
type OptionalInt64 struct {
	Set   bool
	Value *int64
}

func (o *OptionalInt64) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Value = nil
		return nil
	}
	var v int64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	o.Value = &v
	return nil
}

type TaskUpdate struct {
	Title       *string       `json:"title"`
	Description *string       `json:"description"`
	Status      *Status       `json:"status"`
	AssigneeID  OptionalInt64 `json:"assignee_id"`
	// Ожидаемая версия для оптимистичной блокировки, необязательна
	Version *int64 `json:"version"`
}

type Change struct {
	Old any `json:"old"`
	New any `json:"new"`
}

type Changes map[string]Change

type HistoryEntry struct {
	ID        int64           `json:"id"`
	TaskID    int64           `json:"task_id"`
	ChangedBy int64           `json:"changed_by"`
	Changes   json.RawMessage `json:"changes"`
	CreatedAt time.Time       `json:"created_at"`
}

type Comment struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	UserID    int64     `json:"user_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type AssigneeStat struct {
	UserID      int64  `json:"user_id"`
	Name        string `json:"name"`
	ClosedTasks int64  `json:"closed_tasks"`
}

type TaskCommentCount struct {
	TaskID   int64 `json:"task_id"`
	Comments int64 `json:"comments"`
}

type TeamStats struct {
	TeamID              int64              `json:"team_id"`
	TasksByStatus       map[Status]int64   `json:"tasks_by_status"`
	TopAssignees30d     []AssigneeStat     `json:"top_assignees_30d"`
	AvgCloseTimeSeconds *float64           `json:"avg_close_time_seconds"`
	CommentsTotal       int64              `json:"comments_total"`
	CommentsByTask      []TaskCommentCount `json:"comments_by_task"`
}
