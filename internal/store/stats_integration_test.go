package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"taskservice/internal/domain"
)

// Интеграционный тест на реальном MySQL. Запуск: docker compose --profile test run --rm tests
// или TEST_MYSQL_DSN=... go test ./internal/store/...
func TestTeamStatsIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}
	ctx := context.Background()
	if err := Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st, err := Open(ctx, dsn, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	suffix := time.Now().UnixNano()
	mkUser := func(name string) int64 {
		u := &domain.User{Email: fmt.Sprintf("%s-%d@test.local", name, suffix), Name: name, PasswordHash: "x", CreatedAt: now}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	alice, bob, carol, dave := mkUser("alice"), mkUser("bob"), mkUser("carol"), mkUser("dave")

	mkTeam := func(owner int64) int64 {
		tm := &domain.Team{Name: "team", CreatedBy: owner, CreatedAt: now}
		if err := st.CreateTeam(ctx, tm); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMember(ctx, tm.ID, owner, domain.RoleOwner); err != nil {
			t.Fatal(err)
		}
		return tm.ID
	}
	team, otherTeam := mkTeam(alice), mkTeam(dave)

	mkTask := func(teamID int64, status domain.Status, assignee *int64, created time.Time, closed *time.Time) int64 {
		tk := &domain.Task{TeamID: teamID, Title: "t", Status: status, CreatedBy: alice, AssigneeID: assignee,
			CreatedAt: created, UpdatedAt: created, ClosedAt: closed, Version: 1}
		if err := st.InsertTask(ctx, tk); err != nil {
			t.Fatal(err)
		}
		return tk.ID
	}
	closedTask := func(assignee int64, closedAgo, duration time.Duration) int64 {
		closed := now.Add(-closedAgo)
		return mkTask(team, domain.StatusDone, &assignee, closed.Add(-duration), &closed)
	}

	// bob: 3 закрытых за 30 дней, carol: 2, dave: 1, alice: 1 + 1 закрытая 40 дней назад (вне окна)
	t1 := closedTask(bob, 1*24*time.Hour, 1*time.Hour)
	closedTask(bob, 2*24*time.Hour, 3*time.Hour)
	closedTask(bob, 3*24*time.Hour, 2*time.Hour)
	closedTask(carol, 5*24*time.Hour, 4*time.Hour)
	closedTask(carol, 6*24*time.Hour, 6*time.Hour)
	closedTask(dave, 7*24*time.Hour, 2*time.Hour)
	closedTask(alice, 8*24*time.Hour, 2*time.Hour)
	closedTask(alice, 40*24*time.Hour, 4*time.Hour)
	t2 := mkTask(team, domain.StatusTodo, nil, now, nil)
	mkTask(team, domain.StatusInProgress, &carol, now, nil)

	// Чужая команда: не должна попасть в отчёт
	otherClosed := now.Add(-time.Hour)
	foreign := mkTask(otherTeam, domain.StatusDone, &dave, now.Add(-100*time.Hour), &otherClosed)

	for taskID, n := range map[int64]int{t1: 2, t2: 1, foreign: 5} {
		for range n {
			if err := st.InsertComment(ctx, &domain.Comment{TaskID: taskID, UserID: alice, Content: "c", CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
		}
	}

	stats, err := st.TeamStats(ctx, team, now)
	if err != nil {
		t.Fatalf("TeamStats: %v", err)
	}

	wantStatus := map[domain.Status]int64{domain.StatusTodo: 1, domain.StatusInProgress: 1, domain.StatusDone: 8}
	for s, n := range wantStatus {
		if stats.TasksByStatus[s] != n {
			t.Errorf("tasks_by_status[%s] = %d, want %d", s, stats.TasksByStatus[s], n)
		}
	}

	wantTop := []struct {
		id int64
		n  int64
	}{{bob, 3}, {carol, 2}}
	if len(stats.TopAssignees30d) != 3 {
		t.Fatalf("top assignees: got %d rows, want 3: %+v", len(stats.TopAssignees30d), stats.TopAssignees30d)
	}
	for i, w := range wantTop {
		got := stats.TopAssignees30d[i]
		if got.UserID != w.id || got.ClosedTasks != w.n {
			t.Errorf("top[%d] = %+v, want user %d with %d", i, got, w.id, w.n)
		}
	}
	// Третье место делят alice и dave по 1 задаче, выигрывает меньший id
	if third := stats.TopAssignees30d[2]; third.UserID != min(alice, dave) || third.ClosedTasks != 1 {
		t.Errorf("top[2] = %+v", third)
	}
	if stats.TopAssignees30d[0].Name != "bob" {
		t.Errorf("assignee name not joined: %+v", stats.TopAssignees30d[0])
	}

	// Среднее по 8 закрытым задачам команды: (1+3+2+4+6+2+2+4)/8 = 3 часа
	if stats.AvgCloseTimeSeconds == nil || *stats.AvgCloseTimeSeconds != 3*3600 {
		t.Errorf("avg close = %v, want %d", stats.AvgCloseTimeSeconds, 3*3600)
	}

	if stats.CommentsTotal != 3 {
		t.Errorf("comments_total = %d, want 3 (foreign team excluded)", stats.CommentsTotal)
	}
	if len(stats.CommentsByTask) != 2 || stats.CommentsByTask[0].TaskID != t1 || stats.CommentsByTask[0].Comments != 2 {
		t.Errorf("comments_by_task = %+v", stats.CommentsByTask)
	}
}
