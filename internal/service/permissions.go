package service

import (
	"strings"
	"time"
	"unicode/utf8"

	"taskservice/internal/domain"
)

// CanInvite: владелец приглашает админов и участников, админ - только участников.
// Роль owner через приглашение не выдаётся никому.
func CanInvite(actor, target domain.Role) error {
	if !actor.IsManager() {
		return domain.ErrForbidden
	}
	switch target {
	case domain.RoleMember:
		return nil
	case domain.RoleAdmin:
		if actor == domain.RoleOwner {
			return nil
		}
		return domain.ErrForbidden
	default:
		return domain.Invalid("role must be admin or member")
	}
}

// CanChangeRole: менять роли может только владелец, роль владельца неизменна.
func CanChangeRole(actor, current, target domain.Role) error {
	if actor != domain.RoleOwner || current == domain.RoleOwner {
		return domain.ErrForbidden
	}
	if target != domain.RoleAdmin && target != domain.RoleMember {
		return domain.Invalid("role must be admin or member")
	}
	return nil
}

func CanViewStats(role domain.Role) bool { return role.IsManager() }

// AuthorizeTaskChange проверяет права на конкретный набор изменённых полей.
func AuthorizeTaskChange(role domain.Role, actorID int64, cur *domain.Task, changes domain.Changes) error {
	if role.IsManager() {
		return nil
	}
	if cur.CreatedBy == actorID {
		return nil
	}
	if cur.AssigneeID != nil && *cur.AssigneeID == actorID {
		for field := range changes {
			// closed_at - производное от статуса, сам по себе не редактируется
			if field != "status" && field != "closed_at" {
				return domain.NewError(domain.ErrForbidden.Status, domain.ErrForbidden.Code,
					"assignee can change only task status")
			}
		}
		return nil
	}
	return domain.ErrForbidden
}

// ApplyTaskUpdate возвращает новое состояние задачи и только реально изменившиеся поля.
func ApplyTaskUpdate(cur *domain.Task, upd domain.TaskUpdate, now time.Time) (*domain.Task, domain.Changes, error) {
	next := *cur
	changes := domain.Changes{}

	if upd.Title != nil {
		title := strings.TrimSpace(*upd.Title)
		if err := validateTitle(title); err != nil {
			return nil, nil, err
		}
		if title != cur.Title {
			changes["title"] = domain.Change{Old: cur.Title, New: title}
			next.Title = title
		}
	}
	if upd.Description != nil && *upd.Description != cur.Description {
		changes["description"] = domain.Change{Old: cur.Description, New: *upd.Description}
		next.Description = *upd.Description
	}
	if upd.Status != nil {
		if !upd.Status.Valid() {
			return nil, nil, domain.Invalid("status must be one of todo, in_progress, done")
		}
		if *upd.Status != cur.Status {
			changes["status"] = domain.Change{Old: cur.Status, New: *upd.Status}
			next.Status = *upd.Status
			if next.Status == domain.StatusDone {
				next.ClosedAt = &now
			} else {
				next.ClosedAt = nil
			}
			changes["closed_at"] = domain.Change{Old: cur.ClosedAt, New: next.ClosedAt}
		}
	}
	if upd.AssigneeID.Set && !sameID(upd.AssigneeID.Value, cur.AssigneeID) {
		changes["assignee_id"] = domain.Change{Old: cur.AssigneeID, New: upd.AssigneeID.Value}
		next.AssigneeID = upd.AssigneeID.Value
	}

	if len(changes) > 0 {
		next.UpdatedAt = now
	}
	return &next, changes, nil
}

func validateTitle(title string) error {
	if title == "" {
		return domain.Invalid("title is required")
	}
	if utf8.RuneCountInString(title) > 255 {
		return domain.Invalid("title must be at most 255 characters")
	}
	return nil
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
