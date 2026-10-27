package store

import (
	"context"
	"database/sql"
	"time"

	"taskservice/internal/domain"
)

// Все метрики одним запросом: CTE на каждую метрику, затем UNION ALL в общий набор строк kind/name/id/val.
// "Сейчас" передаётся параметром, чтобы окно в 30 дней было детерминированным в тестах.
const teamStatsQuery = `
WITH
status_counts AS (
    SELECT t.status, COUNT(*) AS cnt
    FROM tasks t
    WHERE t.team_id = ?
    GROUP BY t.status
),
top_assignees AS (
    SELECT t.assignee_id, COUNT(*) AS closed_cnt
    FROM tasks t
    WHERE t.team_id = ?
      AND t.status = 'done'
      AND t.assignee_id IS NOT NULL
      AND t.closed_at >= DATE_SUB(?, INTERVAL 30 DAY)
    GROUP BY t.assignee_id
    ORDER BY closed_cnt DESC, t.assignee_id
    LIMIT 3
),
close_time AS (
    SELECT AVG(TIMESTAMPDIFF(SECOND, t.created_at, t.closed_at)) AS avg_sec
    FROM tasks t
    WHERE t.team_id = ? AND t.status = 'done' AND t.closed_at IS NOT NULL
),
comment_counts AS (
    SELECT t.id AS task_id, COUNT(c.id) AS cnt
    FROM tasks t
    JOIN task_comments c ON c.task_id = t.id
    WHERE t.team_id = ?
    GROUP BY t.id
)
SELECT kind, name, id, val FROM (
    SELECT 'status' AS kind, CAST(s.status AS CHAR(255)) AS name, CAST(NULL AS SIGNED) AS id, CAST(s.cnt AS DECIMAL(20, 4)) AS val
    FROM status_counts s
    UNION ALL
    SELECT 'top_assignee', u.name, a.assignee_id, a.closed_cnt
    FROM top_assignees a
    JOIN users u ON u.id = a.assignee_id
    UNION ALL
    SELECT 'avg_close', NULL, NULL, ct.avg_sec
    FROM close_time ct
    UNION ALL
    SELECT 'comments', NULL, cc.task_id, cc.cnt
    FROM comment_counts cc
) report
ORDER BY kind, val DESC, id`

func (r *Repo) TeamStats(ctx context.Context, teamID int64, now time.Time) (*domain.TeamStats, error) {
	rows, err := r.q.QueryContext(ctx, teamStatsQuery, teamID, teamID, now, teamID, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	st := &domain.TeamStats{
		TeamID:          teamID,
		TasksByStatus:   make(map[domain.Status]int64, len(domain.Statuses)),
		TopAssignees30d: []domain.AssigneeStat{},
		CommentsByTask:  []domain.TaskCommentCount{},
	}
	for _, s := range domain.Statuses {
		st.TasksByStatus[s] = 0
	}

	for rows.Next() {
		var (
			kind string
			name sql.NullString
			id   sql.NullInt64
			val  sql.NullFloat64
		)
		if err := rows.Scan(&kind, &name, &id, &val); err != nil {
			return nil, err
		}
		switch kind {
		case "status":
			st.TasksByStatus[domain.Status(name.String)] = int64(val.Float64)
		case "top_assignee":
			st.TopAssignees30d = append(st.TopAssignees30d, domain.AssigneeStat{
				UserID: id.Int64, Name: name.String, ClosedTasks: int64(val.Float64),
			})
		case "avg_close":
			if val.Valid {
				v := val.Float64
				st.AvgCloseTimeSeconds = &v
			}
		case "comments":
			cnt := int64(val.Float64)
			st.CommentsByTask = append(st.CommentsByTask, domain.TaskCommentCount{TaskID: id.Int64, Comments: cnt})
			st.CommentsTotal += cnt
		}
	}
	return st, rows.Err()
}
