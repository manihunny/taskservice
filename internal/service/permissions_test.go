package service

import (
	"errors"
	"testing"
	"time"

	"taskservice/internal/domain"
)

func ptr[T any](v T) *T { return &v }

func TestCanInvite(t *testing.T) {
	cases := []struct {
		actor, target domain.Role
		ok            bool
	}{
		{domain.RoleOwner, domain.RoleMember, true},
		{domain.RoleOwner, domain.RoleAdmin, true},
		{domain.RoleOwner, domain.RoleOwner, false},
		{domain.RoleAdmin, domain.RoleMember, true},
		{domain.RoleAdmin, domain.RoleAdmin, false},
		{domain.RoleAdmin, domain.RoleOwner, false},
		{domain.RoleMember, domain.RoleMember, false},
	}
	for _, c := range cases {
		if err := CanInvite(c.actor, c.target); (err == nil) != c.ok {
			t.Errorf("CanInvite(%s, %s) = %v, want ok=%v", c.actor, c.target, err, c.ok)
		}
	}
}

func TestCanChangeRole(t *testing.T) {
	if err := CanChangeRole(domain.RoleOwner, domain.RoleMember, domain.RoleAdmin); err != nil {
		t.Errorf("owner should promote member: %v", err)
	}
	if err := CanChangeRole(domain.RoleOwner, domain.RoleOwner, domain.RoleMember); err == nil {
		t.Error("owner role must be immutable")
	}
	if err := CanChangeRole(domain.RoleAdmin, domain.RoleMember, domain.RoleAdmin); err == nil {
		t.Error("admin must not change roles")
	}
	if err := CanChangeRole(domain.RoleOwner, domain.RoleMember, domain.RoleOwner); err == nil {
		t.Error("owner role must not be granted")
	}
}

func TestCanViewStats(t *testing.T) {
	if !CanViewStats(domain.RoleOwner) || !CanViewStats(domain.RoleAdmin) || CanViewStats(domain.RoleMember) {
		t.Error("stats must be visible only to owner and admin")
	}
}

func TestAuthorizeTaskChange(t *testing.T) {
	const creator, assignee, other = 1, 2, 3
	task := &domain.Task{ID: 10, TeamID: 1, Title: "t", Status: domain.StatusTodo, CreatedBy: creator, AssigneeID: ptr(int64(assignee))}

	statusOnly := domain.Changes{"status": {}, "closed_at": {}}
	reassign := domain.Changes{"assignee_id": {}}
	retitle := domain.Changes{"title": {}}

	cases := []struct {
		name    string
		role    domain.Role
		actor   int64
		changes domain.Changes
		ok      bool
	}{
		{"owner edits any task", domain.RoleOwner, other, retitle, true},
		{"admin edits any task", domain.RoleAdmin, other, reassign, true},
		{"creator changes title", domain.RoleMember, creator, retitle, true},
		{"creator reassigns", domain.RoleMember, creator, reassign, true},
		{"assignee changes status", domain.RoleMember, assignee, statusOnly, true},
		{"assignee cannot reassign", domain.RoleMember, assignee, reassign, false},
		{"assignee cannot retitle", domain.RoleMember, assignee, retitle, false},
		{"stranger member cannot edit", domain.RoleMember, other, statusOnly, false},
		{"stranger member noop is forbidden", domain.RoleMember, other, domain.Changes{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := AuthorizeTaskChange(c.role, c.actor, task, c.changes)
			if (err == nil) != c.ok {
				t.Fatalf("got %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestApplyTaskUpdate(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	cur := &domain.Task{ID: 1, Title: "old", Description: "d", Status: domain.StatusTodo, AssigneeID: ptr(int64(5)), Version: 3}

	t.Run("done sets closed_at and records changes", func(t *testing.T) {
		next, ch, err := ApplyTaskUpdate(cur, domain.TaskUpdate{Status: ptr(domain.StatusDone), Title: ptr("old")}, now)
		if err != nil {
			t.Fatal(err)
		}
		if next.ClosedAt == nil || !next.ClosedAt.Equal(now) {
			t.Fatalf("closed_at not set: %v", next.ClosedAt)
		}
		if _, ok := ch["title"]; ok {
			t.Error("unchanged title must not be in changes")
		}
		if ch["status"].Old != domain.StatusTodo || ch["status"].New != domain.StatusDone {
			t.Errorf("bad status change: %+v", ch["status"])
		}
	})

	t.Run("explicit null unassigns", func(t *testing.T) {
		next, ch, err := ApplyTaskUpdate(cur, domain.TaskUpdate{AssigneeID: domain.OptionalInt64{Set: true}}, now)
		if err != nil {
			t.Fatal(err)
		}
		if next.AssigneeID != nil {
			t.Fatal("assignee must be cleared")
		}
		if _, ok := ch["assignee_id"]; !ok {
			t.Fatal("assignee change must be recorded")
		}
	})

	t.Run("absent assignee is untouched", func(t *testing.T) {
		_, ch, err := ApplyTaskUpdate(cur, domain.TaskUpdate{}, now)
		if err != nil || len(ch) != 0 {
			t.Fatalf("expected no changes, got %v, %v", ch, err)
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		_, _, err := ApplyTaskUpdate(cur, domain.TaskUpdate{Status: ptr(domain.Status("bogus"))}, now)
		var de *domain.Error
		if !errors.As(err, &de) || de.Code != "validation_error" {
			t.Fatalf("expected validation error, got %v", err)
		}
	})

	t.Run("empty title rejected", func(t *testing.T) {
		if _, _, err := ApplyTaskUpdate(cur, domain.TaskUpdate{Title: ptr("  ")}, now); err == nil {
			t.Fatal("expected error")
		}
	})
}
