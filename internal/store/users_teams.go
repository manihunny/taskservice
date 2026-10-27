package store

import (
	"context"
	"database/sql"
	"errors"

	"taskservice/internal/domain"
)

func (r *Repo) CreateUser(ctx context.Context, u *domain.User) error {
	res, err := r.q.ExecContext(ctx,
		"INSERT INTO users (email, password_hash, name, created_at) VALUES (?, ?, ?, ?)",
		u.Email, u.PasswordHash, u.Name, u.CreatedAt)
	if isDuplicate(err) {
		return domain.ErrEmailTaken
	}
	if err != nil {
		return err
	}
	u.ID, err = res.LastInsertId()
	return err
}

func (r *Repo) userBy(ctx context.Context, where string, arg any) (*domain.User, error) {
	var u domain.User
	err := r.q.QueryRowContext(ctx,
		"SELECT id, email, password_hash, name, created_at FROM users WHERE "+where, arg).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return &u, err
}

func (r *Repo) UserByEmail(ctx context.Context, email string) (*domain.User, error) {
	return r.userBy(ctx, "email = ?", email)
}

func (r *Repo) UserByID(ctx context.Context, id int64) (*domain.User, error) {
	return r.userBy(ctx, "id = ?", id)
}

func (r *Repo) CreateTeam(ctx context.Context, t *domain.Team) error {
	res, err := r.q.ExecContext(ctx,
		"INSERT INTO teams (name, created_by, created_at) VALUES (?, ?, ?)",
		t.Name, t.CreatedBy, t.CreatedAt)
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

func (r *Repo) AddMember(ctx context.Context, teamID, userID int64, role domain.Role) error {
	_, err := r.q.ExecContext(ctx,
		"INSERT INTO team_members (team_id, user_id, role) VALUES (?, ?, ?)", teamID, userID, role)
	if isDuplicate(err) {
		return domain.ErrAlreadyMember
	}
	return err
}

func (r *Repo) SetMemberRole(ctx context.Context, teamID, userID int64, role domain.Role) error {
	_, err := r.q.ExecContext(ctx,
		"UPDATE team_members SET role = ? WHERE team_id = ? AND user_id = ?", role, teamID, userID)
	return err
}

// MemberRole возвращает ErrNotFound, если пользователь не состоит в команде.
func (r *Repo) MemberRole(ctx context.Context, teamID, userID int64) (domain.Role, error) {
	var role domain.Role
	err := r.q.QueryRowContext(ctx,
		"SELECT role FROM team_members WHERE team_id = ? AND user_id = ?", teamID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	return role, err
}

func (r *Repo) ListTeams(ctx context.Context, userID int64) ([]domain.TeamWithRole, error) {
	rows, err := r.q.QueryContext(ctx, `
		SELECT t.id, t.name, t.created_by, t.created_at, m.role
		FROM teams t
		JOIN team_members m ON m.team_id = t.id
		WHERE m.user_id = ?
		ORDER BY t.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	teams := []domain.TeamWithRole{}
	for rows.Next() {
		var t domain.TeamWithRole
		if err := rows.Scan(&t.ID, &t.Name, &t.CreatedBy, &t.CreatedAt, &t.Role); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}
