CREATE TABLE users (
    id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    email         VARCHAR(255)    NOT NULL,
    password_hash VARCHAR(255)    NOT NULL,
    name          VARCHAR(255)    NOT NULL,
    created_at    DATETIME(6)     NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_users_email (email)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE teams (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    name       VARCHAR(255)    NOT NULL,
    created_by BIGINT UNSIGNED NOT NULL,
    created_at DATETIME(6)     NOT NULL,
    PRIMARY KEY (id),
    KEY idx_teams_created_by (created_by),
    CONSTRAINT fk_teams_created_by FOREIGN KEY (created_by) REFERENCES users (id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE team_members (
    id      BIGINT UNSIGNED                  NOT NULL AUTO_INCREMENT,
    team_id BIGINT UNSIGNED                  NOT NULL,
    user_id BIGINT UNSIGNED                  NOT NULL,
    role    ENUM ('owner', 'admin', 'member') NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uq_team_members_team_user (team_id, user_id),
    KEY idx_team_members_user (user_id),
    CONSTRAINT fk_team_members_team FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE,
    CONSTRAINT fk_team_members_user FOREIGN KEY (user_id) REFERENCES users (id) ON DELETE CASCADE
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE tasks (
    id          BIGINT UNSIGNED                     NOT NULL AUTO_INCREMENT,
    team_id     BIGINT UNSIGNED                     NOT NULL,
    title       VARCHAR(255)                        NOT NULL,
    description TEXT                                NOT NULL,
    status      ENUM ('todo', 'in_progress', 'done') NOT NULL,
    created_by  BIGINT UNSIGNED                     NOT NULL,
    assignee_id BIGINT UNSIGNED                     NULL,
    created_at  DATETIME(6)                         NOT NULL,
    updated_at  DATETIME(6)                         NOT NULL,
    closed_at   DATETIME(6)                         NULL,
    version     BIGINT UNSIGNED                     NOT NULL DEFAULT 1,
    PRIMARY KEY (id),
    KEY idx_tasks_team_status (team_id, status),
    KEY idx_tasks_team_assignee (team_id, assignee_id),
    KEY idx_tasks_team_closed (team_id, closed_at),
    CONSTRAINT fk_tasks_team FOREIGN KEY (team_id) REFERENCES teams (id) ON DELETE CASCADE,
    CONSTRAINT fk_tasks_created_by FOREIGN KEY (created_by) REFERENCES users (id),
    CONSTRAINT fk_tasks_assignee FOREIGN KEY (assignee_id) REFERENCES users (id) ON DELETE SET NULL
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE task_history (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    task_id    BIGINT UNSIGNED NOT NULL,
    changed_by BIGINT UNSIGNED NOT NULL,
    changes    JSON            NOT NULL,
    created_at DATETIME(6)     NOT NULL,
    PRIMARY KEY (id),
    KEY idx_task_history_task (task_id),
    CONSTRAINT fk_task_history_task FOREIGN KEY (task_id) REFERENCES tasks (id) ON DELETE CASCADE,
    CONSTRAINT fk_task_history_user FOREIGN KEY (changed_by) REFERENCES users (id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;

CREATE TABLE task_comments (
    id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    task_id    BIGINT UNSIGNED NOT NULL,
    user_id    BIGINT UNSIGNED NOT NULL,
    content    TEXT            NOT NULL,
    created_at DATETIME(6)     NOT NULL,
    PRIMARY KEY (id),
    KEY idx_task_comments_task (task_id),
    CONSTRAINT fk_task_comments_task FOREIGN KEY (task_id) REFERENCES tasks (id) ON DELETE CASCADE,
    CONSTRAINT fk_task_comments_user FOREIGN KEY (user_id) REFERENCES users (id)
) ENGINE = InnoDB DEFAULT CHARSET = utf8mb4 COLLATE = utf8mb4_0900_ai_ci;
