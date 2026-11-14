# aCTFb — ТЗ для Codex на продолжение разработки

> Версия: 4.0 — составлено после просмотра **двух приложенных архивов** `aCTFb-main.zip` и `proxier-main.zip` (12.11.2026). Это не спецификация проекта Proxier. **aCTFb — целевой продукт и исходный код. Proxier — визуальный референс фронтенда и референс принципа деплоя через manifest/templates/versions, но не его бизнес-логики или SSH/Docker-реализации.**

## 0. Роль и обязательный порядок действий

Ты — главный разработчик и координатор Codex. Реализуй aCTFb внутри **существующего** Go-монорепозитория. Задачи, которые можно выполнить независимо, **реально делегируй субагентам на модели `ali/deepseek-v4.1-flash`**; сам отвечай за архитектуру, совместимость, интеграцию, тесты и итоговое качество. Не симулируй деятельность субагентов текстом и не заявляй, что DeepSeek использован, пока не убедишься, что он действительно выбран в дочерних сессиях.

Сначала:
1. Прочитай текущий код, зависимости, миграции, API и дизайн-референс из раздела 1.
2. Проверь, в каком состоянии реально находится рабочее дерево относительно прилагаемого аудита. Архив — лишь зафиксированная версия, приоритет у фактического локального исходника.
3. Зафиксируй реальные пробелы и зависимости в коротком `docs/implementation-plan.md` с галочками *implemented / partial / missing / blocked* и конкретными командами проверки.
4. Устрани блокирующие ошибки сборки, запуска и миграций. Лишь после этого расширяй функциональность.
5. Работай вертикальными срезами: **миграция → репозиторий → сервис → HTTP/API → UI → тест**. Не заменяй реализацию презентацией, макетом, псевдокодом или заглушкой.

Если рантайм, внешняя интеграция либо модель недоступны — сообщи ограничение явно, пометь критерий невыполненным, сделай все независимые части. Не придумывай результаты тестирования.

## 1. Источники истины

### 1.1. Целевой проект — aCTFb

- Репозиторий: `https://github.com/nrf24l01/aCTFb`.
- Актуальное рабочее дерево, `go.mod`, `cmd/`, `internal/`, `migrations/` и существующие компоненты.
- **Канонический API-контракт на момент архива: `docs/swagger.yaml`** (Swagger/OpenAPI 2.0, `basePath: /api/v2`). Он включает **111 путей, 153 HTTP-операции, 349 definitions**. Реализованные в `internal/generated/openapi/models/` модели — сгенерированные типы, **не бизнес-реализация**.
- `docs/architecture.puml` — исходная схема инфраструктуры (могут быть неточности, исправляй и поддерживай её по факту).
- Документ с замыслом aCTFb: платформа для обучения ИБ, задачи Task-Based, индивидуальный Attack+Defense, Pentest-лаборатории по запросу, возможность индивидуальных и командных соревнований.

**Сохраняй существующие имена из API:** `Board`, `Module`, `Task`, `Submission`, `Instance`, `Audience`, `AccessGroup`, `Team`, `Integration`. Нельзя без причины переименовывать всё в `Challenge`, вводить параллельный `/api/v3` или создавать новый несовместимый формат ответов.

### 1.2. Дизайн и концепция deployment templates — Proxier

- Источник: приложенный архив `proxier-main.zip`, **`docs/ui/design/README.md`**, **`docs/ui/design/tokens.css`** и `docs/ui/design/screens/*.dc.html`.
- Наиболее полезные экранные референсы для композиции: `RP-Dashboard-ok`, `RP-Sign-in`, `RP-Servers`, `RP-Server-overview`, `RP-Job`, `RP-Activity`, `RP-Phone-server` — читать как примеры *внешнего вида*, не логики.
- Дополнительная ссылка автора: `https://raw.githubusercontent.com/nrf24l01/proxier/refs/heads/feat/server-addresses/docs/ui/design/README.md`. Если файлов из архива и этой ветки отличаются, записать расхождение, не смешивать бездумно.

**Никакой предметной бизнес-функциональности Proxier переносить нельзя:** proxy, VPN, subscriptions, роутеры, серверные стеки, скрипты, адреса, Cloudflare, Telegram и их API не относятся к aCTFb. Исключение: отдельно заимствовать паттерн **manifest + files / draft / validate / preview / publish / plan / deploy**, см. обязательное дополнение `docs/deployment-templates.md` и `docs/runtime-instances.md`. Не переносить SSH bootstrap и Docker Compose как универсальный runtime. Не переносить архитектуру Proxier: aCTFb остаётся на Go/Echo/Postgres/NATS/Redis/S3. Внешний вид переосмыслить под учащихся и администраторов aCTFb.

## 2. Проверенное состояние архива aCTFb и технический долг P0

Проверено по содержимому `aCTFb-main.zip`:

| Компонент | Что действительно найдено | Вывод |
|---|---|---|
| `cmd/backend/main.go` | Только вызов `config.LoadConfig()`, `cfg` дальше не используется | HTTP-сервер и инициализация зависимостей не реализованы; возможная ошибка компиляции из-за неиспользуемой переменной |
| `internal/httpapi/router.go` | Единственная строка `package httpapi` | Нет маршрутизации, middleware и реального API |
| `internal/users/http/handler.go` | Незавершённый `RegisterUser`, переменные не используются, нет `return` для успешной ветки | Обработчик не компилируется |
| `internal/users/service.go` | Операции Create/Get/CheckPassword/UpdatePassword, запросы email verification | Это заготовка бизнес-слоя, **не законченная авторизация** |
| `migrations/001_init.sql` | В `CREATE TABLE users` лишняя запятая после `email_send_at` | Миграция некорректна |
| `internal/users/service.go` ↔ SQL | INSERT использует `email_secret`, тогда как миграция и SELECT используют `email_2fa_secret` | Регистрация в текущем виде не согласована со схемой |
| `internal/users/service.go` | `us.logger.Info(...)`, но `logger` конструктором не установлен | Возможна nil-pointer panic при запросе подтверждения почты |
| `cmd/notification`, `internal/notifications`, `internal/platform/nats`, `api/proto/emails/email.proto` | Реальный каркас асинхронной отправки verification email, JetStream durable worker | Сохранить и довести до интеграционного теста, не переписывать |
| `internal/platform/{postgres,redis,s3,crypto,email}` | Значительный код инфраструктурных клиентов и криптографии | Повторно использовать, проверить ошибки, жизненный цикл и конфигурацию |
| `config.dev.yml` | В архиве задан только `web_app`; остальные секции зависят от env или остаются пустыми | Проверить обязательные параметры и безопасные dev-defaults, включая Argon2id |
| `docker-compose.dev.yaml` | Backend, PostgreSQL 18, Redis 7, NATS 2 с JetStream, MinIO и init bucket | Сохранить инфраструктуру, исправить способ запуска приложения/миграций и добавлять сервисы по мере необходимости |
| frontend / board / module / instance runner | Соответствующего прикладного кода нет | Их необходимо реализовать, а не считать готовыми из-за OpenAPI-моделей |
| `*_test.go` | В архиве не найдены | Потребуются новые тесты |

Версия Go в `go.mod` — `1.27.1`; Echo v5, pgx v5, Goose, NATS, Redis v9, AWS S3 SDK и т. д. **В среде подготовки ТЗ была Go 1.23.2**, поэтому `go test ./...` не запускался: команда остановилась на требовании версии. Это **не** доказательство успешной или неуспешной компиляции на Go 1.27.1. Codex обязан проверить сборку в правильном toolchain и исправить ошибки.

**Первый критерий приёмки P0:** `go test ./...` (включая компиляцию), `go vet ./...`, успешно применённые Goose migrations к пустой PostgreSQL, работающий backend с `/health` или аналогом, реальная регистрация/вход и проверка роли `user/admin` через HTTP. Приведи старт приложения к полноценному lifecycle: signal cancellation, корректное закрытие pgx/NATS/Redis, логирование, health/readiness.

Если миграция `001_init.sql` уже применялась в реальных базах, не изменяй её задним числом без выяснения истории: создай новую совместимую миграцию. В чистой dev-базе её можно исправить в соответствии с принятым в проекте процессом.

## 3. Функциональная модель и пределы существующего API

**Board** — учебное мероприятие/соревнование; может включать несколько **Module** одного и разных типов. Режим участия (`INDIVIDUAL`/`TEAM`), регистрация (`OPEN`/`INVITE_ONLY`/`CLOSED`), видимость, сроки, рейтинг и доступы задаются согласно Swagger.

**Module** — часть Board, модуль типа `TASKS`, `ATTACK_DEFENSE` или `PENTEST`. Может иметь собственное расписание и целевую аудиторию; `allowedTaskTypes` определяет допустимые механики.

**Task** — задача в TASKS-модуле. По Swagger: `FLAG`, `MANUAL`, `INSTANCE` (для ATTACK_DEFENSE предусмотрены `EXPLOIT`, `PATCH`, для PENTEST — `PENTEST_REPORT`; последние НЕ объявлять обычными `/tasks`-ресурсами).

**Audience** — аудитория доступа на конкретной Board; **AccessGroup** — административно управляемая группа пользователей; **Team** — команда участников. Не смешивать эти разные сущности и механизмы допуска.

**Submission** — проверяемая попытка, её вердикт, отзыв, баллы и вложения. Повторная сдача и ретраи не должны повторно начислять баллы; PostgreSQL остаётся источником истины, Redis может ускорять рейтинг.

**Роли по исходному API:** только глобальные `user` и `admin` (см. `Me`, `AdminUser`); это исходное состояние, а не достаточная целевая модель. **Целевое требование — scoped RBAC** (`board_owner`, `board_editor`, `reviewer`) с явным расширением Swagger/БД, см. раздел 4B. Не добавлять `teacher` как ещё одну глобальную суперроль.

### 3.1. TASKS: классический Task-Based

- Board → Module(`TASKS`) → Task → Submit flag/answer → Verdict → Score/Progress.
- Статические задания с описанием, баллами, категорией и S3-вложениями.
- `FLAG`: проверка флага сервером, безопасное хранение/сравнение, ограничения попыток, отсутствие двойного начисления.
- `MANUAL`: отправка решения/файлов, просмотр и оценка администратором.
- `INSTANCE`: запуск индивидуального или командного инстанса, показ endpoints/TTL/status, остановка/пересоздание в рамках разрешений.
- Состояния и интерфейсы выполнять в соответствии с маршрутами `/boards/{boardUuid}/modules/{moduleUuid}/tasks/...` и `/admin/boards/.../tasks/...` из OpenAPI.

### 3.2. ATTACK_DEFENSE: индивидуальный Attack+Defense

**Не путать с классическим командным Attack/Defense.** Участнику выдают репозиторий исходного уязвимого сервиса и отдельный репозиторий/ветку для эксплойтов. Он пишет эксплойты, пушит их, затем исправляет уязвимости в репозитории исходного сервиса и пушит патчи. Система проверяет каждое изменение автоматически и показывает результат.

- Использовать уже описанные в Swagger `ADConfig`, `ADRepositoryAssignment`, `ADRun`, vulnerabilities и маршруты `/attack-defense/...`, `/webhooks/git/{repositoryBindingUuid}`, `/admin/.../attack-defense/...`.
- Git-провайдеры подключаются через существующую концепцию `Integration` (`GITHUB`, `GITLAB`, `GITEA`); выбор конкретного провайдера определяется настройкой, не зашивать один вариант.
- Проверять подпись webhook, учитывать delivery ID / commit SHA, не запускать дубликаты; хранить статусы `QUEUED/RUNNING/PASSED/FAILED/ERROR/CANCELED` и допустимые артефакты.
- Проводить проверку эксплойтов и патчей **только в изолированном runner**, с ограниченными ресурсами и сроком работы. Проверка патча включает тест на устранение уязвимости и регрессионные тесты работоспособности.
- UI показывает два репозитория, коммиты, историю запусков, вердикты, логи, прогресс отдельно по exploit/patch. Не раскрывать секретные критерии и служебные токены.

### 3.3. PENTEST: лаборатория по запросу

По **исходному** Swagger `PentestConfig` связан с `proxmoxIntegrationUuid`, `nodes` типа **VM**, политикой reset, лимитом resets и `submission`. **Требование изменено:** расширить контракт согласно `docs/deployment-templates.md` и `docs/runtime-instances.md`: логический VM не зависит от Proxmox и может исполняться через KubeVirt; CONTAINER может исполняться через Docker/Podman/Kubernetes. В дальнейшем Pentest может содержать VM и CONTAINER, если их безопасная совместная сеть реализована. Независимый TTL Pentest не добавлять: срок стенда задаётся эффективным окончанием модуля/Board.

- Учащийся на странице модуля нажимает **Запустить лабораторию** → backend проверяет доступ → асинхронный worker создаёт изолированные ВМ → UI показывает состояние, доступные endpoint'ы, ошибки и прогресс.
- Реализовать reset/destroy/cleanup только для разрешённых действий и в соответствии с **обновлённым** OpenAPI. Сбой Proxmox не должен оставлять бесхозные ВМ; статус и ресурсы согласуются идемпотентно.
- Ученик сдаёт флаг/текст/файлы/отчёт в рамках описанной `SubmissionPolicy`; администратор может оценивать ручные работы.
- **Разница между замыслом и текущим контрактом:** в описании продукта есть поэтапная сдача нескольких флагов внутри одного пентеста, но в архивном `PentestConfig` нет очевидной общей модели отдельных «шагов». Сначала реализуй существующий контракт; затем предложи минимальное расширение Swagger/схемы для упорядоченных проверяемых шагов, если сценарий без него не покрыт. Не вводи его молча и не ломай существующие endpoints.

## 4. Архитектура — продолжение существующего проекта

- Ядро: **Go 1.27.1, Echo v5**, компоновка `cmd/backend` и `internal/`.
- DB: **PostgreSQL / pgxpool / Goose**. Все авторитетные результаты, ownership, score, статусы runs/instances — в транзакциях, с idempotency и уникальными ограничениями.
- Redis: кеш, ускорение рейтинга, rate limits по мере необходимости; не заменять им учёт начислений.
- NATS JetStream + protobuf: длительные задачи (письма, создание среды, запуск проверки), retry, durable consumers, Ack/Nak/Term, предсказуемая обработка повторов. Сохранять текущий EMAIL_TASKS worker.
- S3-compatible: приватные объекты, presigned upload/download **только в контексте авторизованного ресурса**. Не создавать generic upload API и не проксировать файлы через backend — это явно зафиксировано в Swagger.
- Integrations: Git и универсальные VM/CONTAINER targets (`driver=proxmox|kubevirt|docker|podman|kubernetes`); Swagger необходимо явно расширить. Секреты не в логах и не в клиентских ответах. Использовать `docs/deployment-templates.md` и `docs/runtime-instances.md` как источник требований к развёртыванию.
- Инстансы TASKS и Pentest: логические виды ресурса `CONTAINER` / `VM`, взаимозаменяемые драйверы. Общий декларативный шаблон `manifest.yaml + files`, immutable versions, preview/diff и idempotent worker. В качестве первого рабочего среза реализовать Docker/CONTAINER и Proxmox/VM; текущие Proxmox-specific ограничения в Swagger — предмет явной миграции, а не вечные требования.
- Workers: отдельные процессы/entrypoints внутри этого же монорепозитория; **не дробить всё на микросервисы без необходимости**. HTTP-backend не выполняет недоверенный код и не управляет Docker socket от имени ученика.
- UI: отдельный frontend внутри репозитория, **Vue 3 + TypeScript + Vite** (рекомендуемый стек для отсутствующего frontend); допустим Tailwind, если не мешает точному воспроизведению дизайн-токенов. Не переводить backend на другой язык ради дизайна.

Сначала восстановить корректную цепочку:

```text
Browser / Vue
     │  HTTP JSON /api/v2
     ▼
Go Echo backend ───────────► PostgreSQL
     ├─────────────────────► Redis
     ├─────────────────────► S3-compatible storage (presigned URLs)
     └──► NATS JetStream ──► notification worker
                         ├──► instance worker ──► Docker/Proxmox integrations
                         └──► A+D checker worker ──► isolated sandbox + Git integration
```

Проверить и поправить `docs/architecture.puml`: там в архиве есть несогласованные имена `Frontend`/`frontend`; диаграмма должна корректно рендериться.


## 4A. Обязательная подсистема развёртывания

**Прочитай [`docs/deployment-templates.md`](docs/deployment-templates.md) и [`docs/runtime-instances.md`](docs/runtime-instances.md).** Это текущие нормативные документы для DeploymentTemplate, VM/CONTAINER, драйверов Proxmox/KubeVirt/Docker/Podman/Kubernetes, расширения Swagger, манифеста, интеграций, версионирования, preview/plan, rollout и idempotent worker. Они сохраняют инварианты удалённого TZ-снимка: kind ≠ driver, immutable versions, dry-run без инфраструктуры, capability errors вместо silent fallback и запрет небезопасных provider options. При конфликте старых формулировок о Proxmox-only Pentest, `templateVmid` или Docker-only runtimes с этими документами приоритет у документов.

## 4B. Разграничение прав: RBAC со scope + контекстные политики (обязательное расширение API)

> **Состояние репозитория:** в исходном `docs/swagger.yaml` глобальные роли `user` и `admin`, а операции `/api/v2/admin/*` помечены `x-required-roles: [ADMIN]`. `AccessGroup` — средство доступа к Board; `Audience` — аудитория модуля; `Team.captainUuid` — капитан команды. Ни одно из них не заменяет делегированное администрирование. Описанное далее — **новое требование**, требующее явной миграции Swagger, БД и клиентов. Не выдавать желаемое за существующую реализацию.

### 4B.1. Что требуется

Платформа должна поддерживать раздельное управление несколькими учебными мероприятиями: создатель/преподаватель одной Board не получает права на другие Board или глобальные настройки. Права задаются не только именем роли, но и **областью действия** (scope) и **типом операции** (permission). Один пользователь одновременно может быть владельцем Board A, проверяющим в модуле Board B и участником Board C.

Базовые роли:

| Роль | Scope | Возможности | Не разрешено |
|---|---|---|---|
| `user` | global | Вход и собственный профиль; получение разрешённого доступа к Board | Администрирование |
| `admin` | global | Управление пользователями, глобальными интеграциями, опубликованными шаблонами, Board, аудитом, правами | Никаких неаудируемых действий; запрет обходить runtime safety policies без явного операционного исключения |
| `board_owner` | одна Board | Полное управление содержимым и расписанием своей Board, делегирование `board_editor` и `reviewer`, управление участниками/аудиториями, запуск административных действий своих стендов | Глобальные пользователи, чужие Board, чтение секретов интеграций, изменение провайдеров/политик безопасности, выдача `admin`/`board_owner` |
| `board_editor` | одна Board, опционально Module | Создание/изменение разрешённых Module и Task, редактирование условий/вложений, просмотр результатов для своих Module | Настройка глобальных интеграций, изменение ACL, выдача ролей, доступ к чужим Board/Module, публикация небезопасных deployment templates |
| `reviewer` | одна Board или конкретный Module | Просмотр назначенных сдач и файлов, ручная проверка, вердикт и корректировка оценки в разрешённом scope | Редактирование заданий, изменение инфраструктуры, массовый пересчёт чужих баллов, выдача ролей |
| `participant` | конкретная Board | Чтение доступных материалов, сдача работ, свои инстансы и результаты | Чужие работы, администрирование |
| `team_captain` | Team внутри Board | Приглашение/удаление участников, передача капитанства, если правила Board разрешают | Не является административной ролью Board; не видит чужие секреты и результаты вне команды |

`participant` — состояние членства в Board, а не глобальная системная роль; `team_captain` — полномочие из модели Team и правил Board. Не превращать их в новый глобальный enum ролей. `board_owner`, `board_editor`, `reviewer` — **назначаемые scoped role bindings**. Существующие `user/admin` остаются глобальными для обратной совместимости.

**Принцип минимальных полномочий:** не делать `board_owner` синонимом `admin`. Администратор может делегировать владение Board. Владелец может выдавать только `board_editor`/`reviewer` внутри своей Board; transfer ownership — отдельная операция, доступная глобальному admin (или специальный защищённый workflow после отдельного согласования). Роль `board_editor`, назначенная на Module, действует **только** в этом Module.

### 4B.2. Permission-based API

Определить централизованный каталог permission-кодов (не SQL-строки по именам HTTP-маршрутов):

```text
platform.users.manage       platform.access_groups.manage
platform.integrations.manage platform.audit.read
platform.deployment_templates.publish
board.create                board.read              board.update
board.publish               board.delete            board.staff.manage
board.participants.manage   board.audiences.manage  board.rating.manage
module.create              module.update           module.delete
module.tasks.manage         module.submissions.read module.submissions.review
module.instances.admin      deployment_templates.use
submission.create_own       submission.read_own
instance.start_own          instance.manage_own
team.manage_own
```

Для новых расширений допускаются namespaced permissions, например `module.forensics.artifacts.review`; **все** они объявляются при регистрации расширения, но сами расширения не выдают их пользователю. Карта `role → permissions` задаётся в ядре и тестируется; произвольные пользовательские роли и редактор permission mapping не требуются для первого релиза.

Матрица характерных операций (помимо проверки времени, аудитории, состояния и владельца ресурса):

| Операция | admin | board_owner | board_editor | reviewer | participant |
|---|:---:|:---:|:---:|:---:|:---:|
| Управлять аккаунтами и интеграциями | ✓ | — | — | — | — |
| Создать новую Board | ✓ | — | — | — | — |
| Настроить свою Board, сроки и публикацию | ✓ | ✓ | — | — | — |
| Создавать/редактировать Task и Module в своём scope | ✓ | ✓ | ✓ | — | — |
| Проверять ручные решения своего scope | ✓ | ✓ | по отдельному grant | ✓ | — |
| Управлять редакторами и проверяющими Board | ✓ | ✓ | — | — | — |
| Публиковать инфраструктурный DeploymentTemplate | ✓ | — | — | — | — |
| Привязать **уже одобренный** шаблон к своей Board/Module | ✓ | ✓ | по отдельному grant | — | — |
| Смотреть свои материалы/сдавать решение | по участию | по участию | по участию | по участию | ✓ |
| Управлять собственным разрешённым Instance | по участию | по участию | по участию | по участию | ✓ |

Права на **чтение чужих submissions** и **ручную оценку** разделены. Проверяющий не должен по умолчанию видеть не относящиеся к нему работы. Доступ к полным flag-значениям, паролям, env/secrets, Git access tokens, Proxmox/K8s credentials не следует из `board.update`, `module.tasks.manage` или доступа к редактору.

### 4B.3. Как хранить и проверять

- Сохранить существующую глобальную модель ролей и участников Board. Добавить `board_role_bindings(user_uuid, board_uuid, role, granted_by_uuid, created_at, expires_at?)` и `module_role_bindings(user_uuid, board_uuid, module_uuid, role, granted_by_uuid, created_at, expires_at?)`; ограничения/FK/уникальность и проверка принадлежности Module соответствующей Board обязательны. При желании допустима одна таблица с scope, но без потери целостности FK.
- Роли `board_owner`, `board_editor`, `reviewer` не должны попадать в глобальное поле `Me.roles` как будто они действуют везде. Добавить контекстное представление: `/api/v2/boards/{boardUuid}/me/permissions` (permissions текущего пользователя, при необходимости — по Module) и назначенные роли в соответствующем admin endpoint.
- В `docs/swagger.yaml` заменить для scoped административных операций однобитное `x-required-roles: [ADMIN]` на явный `x-required-permissions` + scope resource; **сохранить** `x-required-roles: [ADMIN]` на глобальных действиях (`/admin/users`, `/admin/access-groups`, `/admin/integrations`, глобальный аудит). Путь `/admin/boards/{boardUuid}/...` может быть доступен scoped-роли: префикс `/admin` не означает только суперпользователя. Версионировать и документировать изменение; generated models обновлять генератором.
- Нужны CRUD endpoints управления делегированными ролями Board/Module, доступные `admin`/уполномоченному `board_owner`; коды назначения/отзыва/срока действия и ошибка отказа в выдаче более сильной роли.
- `Authorize(actor, permission, resourceCtx)` — единая служба в Go, использованная HTTP middleware **и** бизнес-сервисами; только middleware недостаточно. Сначала authenticated principal, затем разрешённый action, затем фактический scope/ownership. Запрещены проверки только по присутствию UI-кнопки, URL или присланному клиентом `ownerUuid`.
- `AccessGroup`, `Audience`, Board participation, Team и доступность по времени — **ABAC/контекстные ограничения** поверх RBAC: проверять их независимо. `AccessGroup` не предоставляет права `board.update` или `module.submissions.review`.
- Все списки и записи SQL должны фильтроваться по scope, а не только по проверенному UUID в URL. Не допускать IDOR при подмене `boardUuid/moduleUuid/taskUuid/submissionUuid/instanceUuid/fileUuid`; ответ 403/404 выбирать последовательно без утечки информации о закрытых ресурсах.
- Scoped grants должны проверяться против актуальной БД/корректно инвалидируемого кеша, а не только против долгоживущих JWT claims; отзыв доступа действует без необходимости ждать истечения access token. В jobs передавать нормализованный авторизованный context и idempotency, а worker при выполнении проверяет утверждённое действие/ресурсную принадлежность; не исполнять произвольные команды из client payload.
- Аудит: кто выдал/отозвал роль, кто опубликовал Board, кто просмотрел/изменил оценку, кто привязал deployment template, кто запустил/reset/destroy инстанс; redaction секретов и корреляция с trace/job ID.

**Обязательные negative tests:** редактор Board A не меняет Board B; reviewer Module X не читает submissions Module Y; капитан не получает admin API; участник не читает чужие отчёты/S3 URL; отзыв grants немедленно закрывает доступ; обращение к дочернему ресурсу с подменённым parent UUID отклоняется; администратор не получает результаты чужого обучающего пользователя через `me`-маршруты без предусмотренной серверной процедуры.

## 4C. Настоящая система плагинов — CTFd-style addons (обязательное требование)

> **Исправление предыдущей трактовки:** речь идёт об устанавливаемых **сторонних аддонах**, как в CTFd, а не о compile-time Go-модулях. Полный и нормативный контракт: **`docs/plugin-system.md` и `docs/plugin-developer-guide.md`**. Следующие пункты — обязательное краткое изложение; при конфликте требований по плагинам руководствоваться отдельным документом.

1. **Addon != Board Module.** Board Module — сущность для TASKS/PENTEST/ATTACK_DEFENSE. Addon — пакет кода, который устанавливают отдельно от aCTFb; он может добавить свою страницу, backend API, настройки, события, checker, runtime driver или новый тип Board Module. Возможен addon, никак не связанный с Board (например, достижения, сертификаты, вебхуки).
2. **Без пересборки ядра и frontend.** Установка уже собранного плагина через `plugins/<id>/manifest.yaml` плюс OCI image/исполняемый процесс и, при необходимости, готовые frontend assets. Плагин не обязан находиться в основном Go-монорепозитории. Для MVP допустим restart/reload plugin manager после установки. Запрещено считать выполнением требования добавление Go source packages, import в `main.go` и новую сборку бинарника.
3. **Процессная граница.** Go не имеет подходящего безопасного общего механизма runtime-import сторонних пакетов. Использовать Plugin Manager + независимые plugin processes/containers через versioned gRPC Plugin API v1 (либо один обоснованно выбранный HTTP protocol). Не использовать `plugin.Open` как универсальную plugin architecture. Crash плагина не валит core.
4. **Extension points:** `pages/menus/widgets`, namespaced API, события NATS JetStream, types of challenges/checkers, types of Board Modules, RuntimeDriver (`VM`/`CONTAINER`), integrations, settings/private storage. Встроенные TASKS/PENTEST/ATTACK_DEFENSE и Docker/Proxmox могут оставаться нативными; дополнительные типы подключаются через совместимый API.
5. **RBAC и capability security.** Только global admin устанавливает, обновляет и включает плагины. Requested capabilities проверяются до enable, permissions namespace уникальный и не выдаётся сам собой. Плагины не обходят центральный `Authorize()`, не читают root DB tables, не могут самостоятельно начислять баллы или получать неограниченный Docker socket. Внешний plugin backend использует отдельную service identity.
6. **UI runtime.** Frontend узнаёт включённые плагины из backend registry и показывает пункты меню/страницы по effective permissions. Для MVP встроить plugin UI через sandboxed iframe с отдельным origin + constrained bridge. Trusted ESM UI возможен только отдельной явно опасной опцией, где оператор принимает риск выполнения same-origin JS; он не является sandbox.
7. **Lifecycle:** discover/validate/install/enable/disable/upgrade/remove + statuses/health/logs, version compatibility, migrations/own data, error handling; выключение или падение плагина не ломает платформу и не уничтожает его данные.
8. **Тест, который доказывает работу:** собрать aCTFb один раз, затем установить `example.hello` и `example.score_events` как отдельные плагины, увидеть страницу/админ-настройку, API и событие в работающей системе **без `go build`/`npm build` core**. Провести negative RBAC, process crash, disabled plugin, upgrade/migration, replay events tests.

**Не путать с разделом 4B:** RBAC — часть ядра; плагины могут лишь объявлять собственные namespaced permissions и пользоваться серверной авторизацией. Не путать с Deployment RuntimeDriverRegistry: драйвер — capability, которое может предоставлять установленный addon, но не целая система плагинов.

**Обязательные артефакты:** `docs/plugin-system.md`, `docs/plugin-developer-guide.md`, стабильный `Plugin API v1`, admin UI «Plugins», два внешних examples, интеграционный тест установки после сборки.

## 5. Frontend: адаптация **реального** дизайна Proxier

Прочитай `proxier/docs/ui/design/README.md`, `tokens.css` и макеты. Не ограничивайся словами «тёмная админка»; сохранить конкретную грамматику Proxier, но интерфейс полностью переименовать и перекомпоновать под aCTFb.

### 5.1. Визуальная система (из Proxier)

- Только тёмная тема: **чёрный фон `#000000`**, поверхность `#191724`, разделители `#26233a`; цвет текста `#e0def4`, secondary `#908caa`, muted `#6e6a86`.
- Шрифт **IBM Plex Mono**. Desktop основной текст **13px/20px**, телефон **14px/22px**, meta **12px**, заголовок секции **11px uppercase + tracking .08em**, заголовок страницы **22px/28px**.
- Основа — **плоские состыкованные блоки и линии 1px**, без декоративных карточек, градиентов, огромных теней и произвольных ярких акцентов. Верхняя панель **48px**, sidebar **212px**, строка заголовка области **36px**, базовый gutter **16px**, нижняя панель подсказок клавиш **28px**.
- **Цвет несёт состояние, а не украшение**. Healthy = pine `#31748f` + круг; предупреждение = gold `#f6c177` + треугольник; failure = love `#eb6f92` + квадрат; processing = светлая пульсация; unknown/queued = контурное кольцо; paused = foam `#9ccfd8`; disabled/expired = muted. В Proxier `iris #c4a7e7` отведён специфическому состоянию blocked; **в aCTFb определить собственное непротиворечивое значение для iris либо не использовать**. Никогда не передавать смысл только цветом: рядом текст и форма маркера.
- Таблицы: одна строка на объект, без горизонтальных полос-разделителей между каждой строкой, hover на `--base`, выделенная строка на `--overlay` с `›` слева. Колонки имеют min-width; на узких экранах **горизонтальный скролл самой таблицы**, никакого ломания слов в столбцах.
- Одна заметная primary action на экран (светлая кнопка); вторичные кнопки — прозрачные с границей. Опасные операции — с красной обводкой и подтверждением, при безвозвратных действиях ввод имени.
- Severity banner: полоса на всю ширину проблемного состояния, с указанием причины, времени и реального действия; не использовать декоративные alert-карточки.
- Поиск `/`, действия `:`, справка `?`, навигация `j/k`, `enter`, `esc`; внутри input/editor шорткаты не перехватывать. **Нижняя строка подсказок** отражает доступные действия. Для aCTFb привязать маршруты и действия заново (Board, Tasks, Progress, Admin, Jobs), не копировать сочетания про VPN и серверы.
- Desktop-first по стилю, но **полноценное использование студентом на телефоне**: просмотр задач, сдача флага, статус инстанса и запуск стенда должны работать на мобильном экране (Proxier ориентируется на мобильные уведомления — это ограничение для aCTFb **не наследовать**). Под шириной ~760px скрыть sidebar/keys, добавить адекватное мобильное меню и увеличенные touch targets.
- Логи в ограниченном самостоятельном scroll box, `Follow`/`Expand`; длинные идентификаторы и URL показывать моноширинно с copy; секреты скрывать по умолчанию.
- Состояния пусто/загрузка/ошибка/нет доступа и реальные успешные/неуспешные действия. Доступность: видимый focus, корректные названия кнопок, клавиатура, reduced motion.

Не копируй имена `.pc-*` как единую глобальную зависимость, если проще создать собственные `actfb-*` компоненты с теми же токенами. Не копируй напрямую сотни HTML-макетов Proxier; выполни адаптацию системных компонентов.

### 5.2. Карта экранов aCTFb

**Общая оболочка:** `aCTFb`/логотип слева, поиск, активные jobs (проверки/инстансы), имя аккаунта, sign out; sidebar с группами *Обучение*, *Соревнования*, *Администрирование* (разделы по фактическим правам). Блок страницы: `h1`, строка статуса, области с заголовками, действия справа. Главный dashboard — доступные борды, текущие учебные модули, последние сдачи, активные лаборатории/проверки.

**Ученик (`user`):** вход/регистрация/подтверждение email; Boards list; Board detail (модули, сроки, правила, доступ); Module list/detail; Tasks list (category/points/status/filter); Task detail (условие, файлы, send flag/manual, verdict/history, instance controls); Pentest (start/progress/endpoints/report); Attack+Defense (Git repos, commits, exploitation/patch results); scoreboard, team, profile.

**Административные экраны по эффективным permissions (`admin` или назначенные `board_owner`/`board_editor`/`reviewer`):** Boards, Modules, Audiences, Teams, Task editor, review, active instances и job log строго в пределах разрешённого scope. Глобальные Users, AccessGroups, Integrations, audit и публикация DeploymentTemplate — только `admin`. Приоритет реальному API; не строить несуществующие эндпоинты, создавая ложную интерактивность.

**Сопоставление визуальных референсов:**

| Proxier: ориентир | aCTFb: новая страница |
|---|---|
| Dashboard (counts + statuses) | обзор активных борд, прогресса, инстансов |
| Servers/Services list (фильтр + rows) | каталог Boards / Tasks / Users |
| Server overview (status bands + tabs) | Board / Module / Task detail |
| Job / Activity (progress + log) | run в Attack+Defense, provisioning инстанса, audit |
| New server form + side summary | создание Board / Task / настройки стенда |
| Sign-in | aCTFb auth |
| Phone server screens | мобильная задача / мобильная лаборатория |

### 5.3. Работающий UI, а не прототип

Реальные HTTP-запросы по `/api/v2`, типизированный клиент из Swagger или вручную с проверкой контракта. Загрузка, ошибки и разрешения. Storybook/mock fixtures допустимы для разработки компонентов, **не вместо** реального end-to-end. Не рисовать выдуманные результаты соревнования как данные пользователя. На странице должен быть честный empty state, если объектов нет.

## 6. Реализация в безопасном и эффективном порядке

### P0 — развернуть и собрать уже существующее

- Стабилизировать Go 1.27.1 toolchain, Makefile, Dockerfile.dev, Compose.
- Исправить `backend/main.go`, пустой router, незавершённый registration handler, SQL migration, несовпадающее поле email secret, nil logger, отсутствие полной config validation.
- Создать HTTP router, auth service/handlers по `/api/v2/auth/*`, `/me`, JWT/refresh/role middleware по уже существующей спецификации, без хранения секретов в логах.
- Поднять `/health` и `/ready` (технические endpoints могут быть вне публичного Swagger, описать отдельно).
- Закончить путь email verification через NATS → notification worker → SMTP (для dev допустим Mailpit), обработку ошибок и корректное завершение worker.
- Написать unit и интеграционные тесты, миграцию на чистую БД, минимальный CI.

**Готово, когда:** `docker compose ... up --build`, health/ready, миграции и регистрация/вход/подтверждение email проходят реальные запросы; no fake success.

### P1 — базовые учебные сценарии

- БД и сервисы для Board / Module / Task / Participation / Audience / AccessGroup / Team / Submission / Score. Сразу внедрить scoped RBAC из раздела 4B; предусмотреть extension points из 4C и отдельного Plugin System ТЗ. Builtin providers могут быть компилируемыми, но третий-party addon должен устанавливаться без пересборки. Не закладывать маршруты, жёстко привязанные к admin-only и закрытому enum ModuleType.
- Начать с конкретных CRUD/чтения по существующим путям Swagger; не обязательно реализовать сразу все 153 операции, но каждый готовый route должен соответствовать контракту.
- Контроль доступа/расписания и индивидуальный режим; scoped-роли для владельца/редактора/проверяющего и отрицательные тесты двух независимых Board; поддержка команд — в соответствии с API, но не усложнять первый вертикальный срез.
- Vue-оболочка, авторизация, список Boards, Board details, Task list/detail, отправка статического FLAG и результат.

**Готово, когда:** администратор создаёт Board + TASKS Module + FLAG Task, пользователь вступает, сдаёт флаг, получает баллы, видит историю и рейтинг; повторная сдача не начисляет повторно.

### P2 — S3, универсальные deployment templates и контейнерный MVP

- Scoped presigned file endpoints из Swagger, ownership и срок действия ссылок, без общего `/upload`.
- Сначала дополнение `docs/deployment-templates.md` и `docs/runtime-instances.md`: модели DeploymentTemplate/TemplateVersion/Binding, проверка и версия manifest + files, capability validation, dry-run/preview, runtime registry. Затем асинхронный instance worker с Docker/CONTAINER (Docker API только в доверенном worker на выделенном узле), лимиты и network isolation.
- Статусы lifecycle по **реальному enum `InstanceStatus`** (`PROVISIONING`, `RUNNING`, `RESETTING`, `DESTROYING`, `ERROR`, `EXPIRED`), контроль TTL, идемпотентность, cleanup и reconciliation после рестартов.
- Рабочий UI статусов/адресов, кнопок start/reset/stop только по допустимым операциям.

**Готово, когда:** пользователь запускает учебный контейнер, получает endpoint, администратор видит инстанс, TTL и остановка освобождают ресурсы.

### P3 — Pentest на универсальной VM и альтернативные драйверы

- Внести миграцию OpenAPI/БД: `VM`/`CONTAINER` — логический kind, Proxmox/KubeVirt/Docker/Podman/Kubernetes — драйверы. Настройка `PROXMOX` как совместимый legacy alias, не native VMID в общей модели.
- Реальный Proxmox VM worker, создание изолированных VM из generic VM source, допустимые endpoints, lifecycle/reset/cleanup, лимиты, защита от чужих стендов.
- Далее реализовать Podman, Kubernetes, KubeVirt как заменяемые адаптеры с явными capabilities; не добавлять пустые заглушки. Смешанные Pentest VM/CONTAINER не разрешать, пока нет безопасной общей сети.
- User/admin Pentest UI и отправка отчёта по `SubmissionPolicy`.
- Проверить и отдельно согласовать расширение для **поэтапной сдачи нескольких флагов**, если этого нет в текущем API.

**Готово, когда:** ученик запускает персональную VM через Proxmox, а смена провайдера на KubeVirt возможна на уровне интеграции после появления драйвера — без переписывания common VM manifest. Все ресурсы корректно очищаются.

### P4 — Attack+Defense

- Git Integrations, assignment репозиториев, проверка вебхуков, durable jobs, checkout фиксированного SHA.
- Изолированная проверка эксплойта и патча, ресурсные лимиты, запрет host socket и секретов в sandbox, отдельные verdicts, ограниченный просмотр логов.
- UI для Git-связей, коммитов, run history, result и администрирования проблем.

**Готово, когда:** участник пушит exploit и patch, каждый push приводит к ровно одной логической проверке с отображаемым результатом даже при повторной доставке события.

### P5 — качество и завершение

- Единые компоненты дизайна на всех экранах, мобильные формы; клавиатурные сценарии.
- Интеграционные тесты DB/NATS/S3/runners (в тестовом окружении), фронтенд E2E; smoke-тест на развернутом Compose.
- Negative security tests: эскалация scoped-ролей, отключение module provider, подмена scope UUID, доступ к чужому инстансу, утечка signed URLs, двойное начисление баллов, непроверенный webhook, runner timeout, ошибка/перезапуск worker, нештатный cleanup.
- README для локального запуска, переменные окружения/секреты, актуальные OpenAPI и PlantUML, краткий operator runbook.

## 7. Модель работы субагентов DeepSeek v4.1 Flash

**Обязательно использовать настоящие Codex subagents.** Рекомендованная настройка в `.codex/config.toml` доверенного репозитория:

```toml
[agents]
enabled = true
default_subagent_model = "ali/deepseek-v4.1-flash"
max_concurrent_threads_per_session = 4
```

Задавать агентам отдельные, **непересекающиеся по редактируемым файлам** обязанности. В ранних этапах: `api-auditor` (Swagger audit, read-only), `backend-worker` (один пакет/feature), `ui-worker` (frontend после согласования API), `test-reviewer` (тесты и безопасность). Позднее: `instance-worker`, `pentest-worker`, `ad-checker-worker` — не запускать одновременно на изменении общих библиотек и схемы БД.

Пример файла `.codex/agents/deepseek_worker.toml`:

```toml
name = "deepseek_worker"
description = "Реализует отдельно назначенный компонент aCTFb с соблюдением OpenAPI и текущего кода"
model = "ali/deepseek-v4.1-flash"
developer_instructions = """
Работай только в выделенной области репозитория aCTFb. Читай docs/swagger.yaml и существующий код до изменений. Не меняй общую архитектуру, миграции или публичные API без согласования с главным агентом. Не делай фиктивных эндпоинтов, моков под видом реализации или псевдотестов. При сдаче задачи перечисли файлы, изменения, фактические тесты, риски и блокеры. Секреты не логируй.
"""
```

**Провайдерное ограничение:** subagents наследуют provider и MCP родителя, хотя могут выбирать `model`. Пользовательский `bobr-cli-proxy` с `ali/deepseek-v4.1-flash` должен быть настроен в пользовательском Codex-профиле / `~/.codex/config.toml`, **не в проектном репозитории**. Если основной Codex запущен с другим провайдером, модель DeepSeek может быть недоступна нативным субагентам. Тогда не выдавай fallback-модель за DeepSeek: зафиксируй blocker и предложи запуск родителя с профилем нужного провайдера либо **явно согласованную** альтернативную внешнюю оркестрацию. Не сохраняй токены, пользовательские адреса прокси или локальные пути в коммитах.

**Официальная документация Codex:** https://learn.chatgpt.com/docs/agent-configuration/subagents и https://learn.chatgpt.com/docs/config-file/config-reference . Перед записью настроек проверь поддержку ключей в установленной версии CLI.

В каждом цикле:
1. Главный агент сам определяет контракт задачи, интерфейсы, файлы и критерии готовности.
2. Делегирует независимые реализации/проверки (до 4 параллельно), указывает запрет на изменение общей схемы.
3. Дожидается результата, просматривает diff, прогоняет тесты и проверяет соблюдение контракта.
4. Принимает или возвращает исправления; интегрирует и обновляет `docs/implementation-plan.md`.
5. Не принимает «готово», если работающие сценарии реально не проверены.

## 8. Правила качества и безопасности

- **Нельзя** выполнять недоверенный Git-код в backend или на хосте; проверочные контейнеры/ВМ запускаются с лимитами CPU/RAM/PID/disk/time, сетевой политикой, без privileged, host mounts, Docker socket и облачных/SSH секретов.
- Право действия определяется на сервере по `user/admin` + Board/Module/Audience/Team ownership/schedule; сокрытие UI-кнопок не считается авторизацией.
- Идемпотентность mutating operations, событий NATS, webhook retries, provisioning и подсчёта баллов. Счёт и статус должны переживать рестарт worker.
- Ключи и токены хранятся в секретах/переменных окружения; минимальные scopes; маскирование в API, логах, UI и checker artifacts.
- Все S3 ключи нормализовать и привязывать к владельцу/модулю; presigned URLs short-lived; нет публичных bucket policy по умолчанию.
- Migrations — только через Goose; не дропать пользовательские данные и не переписывать применённые схемы без плана миграции.
- Не добавлять чрезмерную инфраструктуру, event sourcing, Kubernetes или новые БД «на будущее», если нет функции, которая их требует.
- Встроенная observability: structured logs, request/job IDs, health/readiness, базовые метрики (счётчики/длительности/ошибки), но без переноса лишнего функционала Proxier.

## 9. Формат результата и критерии завершения каждой итерации

В финальном ответе Codex после каждого завершённого вертикального среза:

1. Что реализовано и **что ещё не реализовано** (кратко, без выдуманных успехов).
2. Список изменённых компонентов/файлов и миграций.
3. Какие настоящие субагенты запускались, для каких задач и с какой моделью; если модель не удалось проверить — сказать.
4. Выполненные команды и точные результаты проверок: `go test`, `go vet`, миграция PostgreSQL, frontend build/test, API/Compose smoke, E2E по применимости.
5. Как воспроизвести основной пользовательский сценарий и где смотреть статус/логи.
6. Блокеры, остаточные риски, следующий приоритет из P0–P5.

**Не останавливаться только на аудите/плане. Приступить к реализации P0 немедленно, затем двигаться по работающим вертикальным срезам, а не пытаться одновременно закрыть все 153 HTTP-операции.**

---

### Стартовая команда координатору

> Прочитай `aCTFb_CODEX_TASK_v4.md`, текущий репозиторий aCTFb и локальные файлы design-референса Proxier (если они доступны рядом; если нет — используй предоставленный архив или ссылку и явно укажи доступность). Сначала проверь P0 и сверь OpenAPI. Создай минимальный план, затем сразу реализуй P0. Параллельно делегируй независимый read-only аудит и изучение дизайн-системы субагентам `ali/deepseek-v4.1-flash`. После P0 реализуй первый end-to-end сценарий Board → TASKS → FLAG → Submission → Score и подключи соответствующий фронтенд. Используй Proxier **только** для визуальной системы, не копируй его логику или стек. В каждом отчёте разделяй реализованное, проверенное и незавершённое.
