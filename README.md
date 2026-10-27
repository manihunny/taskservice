# Task Service

REST API для управления задачами внутри команд: ролевая модель, история изменений,
кеширование списка задач в Redis и SQL-отчёт по команде.

**Стек:** Go 1.26 (stdlib `net/http`), MySQL 8.4, Redis 7, Docker Compose, OpenAPI 3.

## Быстрый старт

```bash
cp .env.example .env        # при необходимости поменять пароли и JWT_SECRET
docker compose up -d --build
curl http://localhost:8080/healthz
```

- API: `http://localhost:8080/api/v1`
- Swagger UI: `http://localhost:8080/swagger/`
- OpenAPI-спека: [`docs/openapi.yaml`](docs/openapi.yaml) (отдаётся на `/swagger/openapi.yaml`)

Остановить и удалить данные: `docker compose down -v`.

### Локальный запуск приложения без Docker

MySQL и Redis в compose публикуют порты на `127.0.0.1` (`MYSQL_PORT`, `REDIS_PORT` из `.env`),
значения из `.env.example` уже указывают на них.

```bash
cp .env.example .env
docker compose up -d mysql redis
set -a; source .env; set +a
go run ./cmd/server
```

Если порты 3306/6379 заняты, поменяйте `MYSQL_PORT`, `REDIS_PORT` и `REDIS_ADDR` в `.env`.

## Конфигурация

Всё задаётся переменными окружения (см. [`.env.example`](.env.example)), хардкода нет.

| Переменная | По умолчанию | Описание |
|---|---|---|
| `HTTP_ADDR` | `:8080` | Адрес HTTP-сервера |
| `APP_PORT` | `8080` | Порт на хосте в docker compose |
| `MYSQL_HOST`, `MYSQL_PORT`, `MYSQL_DATABASE`, `MYSQL_USER`, `MYSQL_PASSWORD` | `localhost`, `3306`, `taskservice`, `root`, - | Подключение к MySQL. `MYSQL_PORT` также порт MySQL на хосте в compose |
| `MYSQL_DSN` | - | Полный DSN, если задан, перекрывает поля выше |
| `MYSQL_ROOT_PASSWORD` | - | Пароль root для контейнера MySQL |
| `REDIS_ADDR`, `REDIS_PASSWORD`, `REDIS_DB` | `localhost:6379`, -, `0` | Подключение к Redis |
| `REDIS_PORT` | `6379` | Порт Redis на хосте в compose |
| `CACHE_TTL` | `5m` | TTL кеша списка задач |
| `JWT_SECRET` | **обязательно** | Секрет подписи JWT (HS256) |
| `JWT_TTL` | `24h` | Время жизни токена |
| `RATE_LIMIT_RPS`, `RATE_LIMIT_BURST` | `20`, `40` | Ограничение частоты запросов на IP |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |

## Миграции

SQL-миграции лежат в [`migrations/`](migrations) и встроены в бинарник (`embed`).
Приложение применяет их при старте: таблица `schema_migrations` хранит применённые файлы,
`GET_LOCK` защищает от параллельного применения несколькими репликами.
Новая миграция - файл `NNN_name.sql` со следующим номером.

## Тесты

```bash
go test ./...                                   # unit-тесты прав доступа
docker compose --profile test run --rm tests    # все тесты, включая интеграционный тест SQL-отчёта на MySQL
```

Интеграционный тест (`internal/store/stats_integration_test.go`) использует отдельную БД
`taskservice_test` (создаётся при первой инициализации MySQL) и пропускается, если не задан `TEST_MYSQL_DSN`.
Запуск с хоста при поднятом `docker compose up -d mysql`:

```bash
TEST_MYSQL_DSN='root:<MYSQL_ROOT_PASSWORD>@tcp(127.0.0.1:3306)/taskservice_test?parseTime=true&loc=UTC' go test ./internal/store/...
```

## Примеры запросов

```bash
API=http://localhost:8080/api/v1

# регистрация и логин
curl -s -X POST $API/register -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.com","password":"secret123","name":"Alice"}'
curl -s -X POST $API/register -H 'Content-Type: application/json' \
  -d '{"email":"bob@example.com","password":"secret123","name":"Bob"}'
TOKEN=$(curl -s -X POST $API/login -H 'Content-Type: application/json' \
  -d '{"email":"alice@example.com","password":"secret123"}' | jq -r .access_token)
AUTH="Authorization: Bearer $TOKEN"

# команда и приглашение
curl -s -X POST $API/teams -H "$AUTH" -d '{"name":"Backend"}'
curl -s $API/teams -H "$AUTH"
curl -s -X POST $API/teams/1/invite -H "$AUTH" -d '{"email":"bob@example.com","role":"member"}'

# задачи
curl -s -X POST $API/tasks -H "$AUTH" -d '{"team_id":1,"title":"Login page","assignee_id":2}'
curl -s "$API/tasks?team_id=1&status=todo&assignee_id=2&limit=20&offset=0" -H "$AUTH"
curl -s -X PUT $API/tasks/1 -H "$AUTH" -d '{"status":"done","version":1}'
curl -s $API/tasks/1/history -H "$AUTH"

# комментарии
curl -s -X POST $API/tasks/1/comments -H "$AUTH" -d '{"content":"Looks good"}'
curl -s $API/tasks/1/comments -H "$AUTH"

# отчёт (owner/admin)
curl -s $API/teams/1/stats -H "$AUTH"
```

Ошибки возвращаются в едином формате:

```json
{"error": {"code": "forbidden", "message": "not enough permissions"}}
```

## Архитектура

```
cmd/server          точка входа, graceful shutdown
internal/config     конфигурация из env
internal/domain     сущности, роли, статусы, доменные ошибки
internal/store      MySQL: репозиторий, транзакции, миграции, SQL-отчёт
internal/cache      Redis-кеш списка задач
internal/auth       JWT
internal/service    бизнес-логика и проверки прав
internal/api        HTTP-хендлеры, auth, rate limit, логирование
migrations          SQL-миграции
docs                OpenAPI и Swagger UI
```

### Права доступа

Проверяются в сервисном слое (`internal/service/permissions.go`, покрыто unit-тестами).

| Действие | owner | admin | member |
|---|---|---|---|
| Видеть команду, задачи, историю, комментарии | да | да | да |
| Создать задачу, комментировать | да | да | да |
| Редактировать любую задачу | да | да | нет |
| Редактировать свою задачу (создатель) | да | да | title, description, status, assignee |
| Редактировать назначенную задачу (исполнитель) | да | да | только status |
| Приглашать | admin, member | только member | нет |
| Менять роли (`PUT /teams/{id}/members/{user_id}`) | да, кроме owner | нет | нет |
| Аналитика `/teams/{id}/stats` | да | да | нет |

- Не участник команды получает `404`, а не `403`: так не раскрывается существование чужих команд и задач.
- Исполнителем можно назначить только участника той же команды.
- Роль `owner` нельзя выдать приглашением или сменой роли, роль владельца неизменна.

### Транзакции и конкурентность

- Создание команды и добавление владельца - одна транзакция. Приглашение (проверка прав + вставка) - тоже.
- Создание/обновление задачи и запись в `task_history` - одна транзакция.
- Обновление: `SELECT ... FOR UPDATE` блокирует строку задачи, поэтому параллельные апдейты
  выполняются последовательно и дифф для истории строится от актуального состояния.
  Дополнительно `UPDATE ... WHERE version = ?` и необязательное поле `version` в запросе:
  клиент, работавший со старой версией, получит `409 version_conflict`, а не перезапишет чужие изменения.
- История хранит старое и новое значение каждого изменённого поля, автора и время.
  Создание задачи тоже пишется в историю (`old: null`).

### Кеш

- Кешируется ответ `GET /tasks` с TTL `CACHE_TTL` (5 минут). Ключ: `tasks:team:{id}:v{версия}:{hash фильтров}`.
- Любое изменение задач команды делает `INCR tasks:team:{id}:ver` после коммита -
  все старые ключи становятся недостижимыми и истекают по TTL. Без `KEYS`/`SCAN`.
- Версия читается до запроса в БД, поэтому данные, прочитанные параллельно с изменением,
  сохраняются под старой версией и не будут отданы.
- Недоступность Redis не ломает API: запросы идут в MySQL, ошибки пишутся в лог.

### SQL-отчёт

`GET /api/v1/teams/{id}/stats` - один запрос с CTE (`internal/store/stats.go`):
задачи по статусам, топ-3 исполнителя по закрытым задачам за 30 дней, среднее время
закрытия (`TIMESTAMPDIFF`), количество комментариев по задачам. Используются `JOIN`,
`GROUP BY`, агрегаты и работа с датами; везде фильтр по `team_id`.

### Дополнительно

- Rate limiting на IP (token bucket, `golang.org/x/time/rate`).
- Структурированные JSON-логи (`log/slog`).
- Unit-тесты на бизнес-логику прав доступа.
