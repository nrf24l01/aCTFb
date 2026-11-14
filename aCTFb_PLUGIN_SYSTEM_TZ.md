# aCTFb — Plugin System (CTFd-style addons): техническое задание

> Версия 1.0, 12.11.2026. **Нормативная часть ТЗ для Codex**. Основа — исходный aCTFb; CTFd используется как **пример пользовательской модели аддонов**, а не как кодовая или языковая зависимость. Proxier — только визуальный дизайн-референс и источник идеи декларативного deployment templates.

## 1. Цель и принцип

Нужна **настоящая устанавливаемая система аддонов**, как у CTFd. Оператор добавляет сторонний плагин в каталог/устанавливает его пакет, включает в админке и получает новую функциональность **без изменения исходников и пересборки frontend/backend aCTFb**. Не подменять требование регистрацией Go-пакетов, которые должны компилироваться вместе с основным приложением.

Важно различать термины:
- **Plugin / addon** — самостоятельно распространяемое и устанавливаемое расширение платформы.
- **Board Module** — существующая предметная сущность `Board → Module`; плагины могут добавлять новые типы таких модулей, но могут вообще не иметь отношения к Board.
- **RuntimeDriver** — универсальный VM/CONTAINER-адаптер. Плагин может предоставлять новый драйвер, но не всякий плагин является драйвером.

Обычная сборка aCTFb должна работать без сторонних плагинов. Наличие отсутствующего/сломавшегося плагина не должно валить backend.

## 2. Аналогия с CTFd, границы переноса

CTFd обнаруживает Python-плагины в каталоге, вызывает `load(app)` и позволяет регистрировать маршруты, настройки, frontend assets, типы задач и собственные таблицы. Для Go **не пытаться воспроизвести `load(app)` путем импорта произвольных Go-пакетов, `plugin.Open` или компиляции исходников плагина вместе с сервером**. Заимствовать контракт установки и возможности расширения, реализовать их межпроцессным протоколом.

## 3. Установка и упаковка

Поддержать filesystem-first механизм: `plugins/<plugin-id>/manifest.yaml` + серверный компонент (OCI image **или** локальный исполняемый файл) + опциональная готовая сборка frontend assets + миграции. Весь пакет можно распространять как архив или репозиторий. В production **предпочесть OCI-контейнер и pinned image digest**; локальный исполняемый файл разрешить для контролируемой dev/операторской установки. Источники пакета не должны автоматически исполняться при upload/download.

Обязательные поля манифеста: стабильный namespaced `id` (`vendor.name`), `name`, `version`, диапазон совместимости с Plugin API, описание, автор, requested capabilities, объявленные permissions, объявленные API/UI extension points, вид backend transport/entrypoint/image, health endpoint, timeout defaults, зависимости; опционально: события, `configSchema`, migrations, assets, checksum/digest.

Пример **схематичный**, а не обязательный финальный YAML-синтаксис:

```yaml
id: example.achievements
name: Achievements
version: 1.0.0
apiVersion: "1"
backend:
  mode: oci
  image: registry.example.net/ctf/achievements@sha256:<digest>
  protocol: grpc
frontend:
  mode: iframe
  entry: public/index.html
permissions:
  - id: plugin.example.achievements.view
    scope: board
  - id: plugin.example.achievements.manage
    scope: board
capabilities:
  - core.boards.read
  - core.events.subscribe
routes:
  - path: /boards/:boardUuid/achievements
    permission: plugin.example.achievements.view
    scope: board
    ui: true
subscriptions:
  - board.completed
```

В окончательных примерах использовать валидный, полный digest вместо `<digest>`; это обозначение не является готовым деплой-манифестом.

Система должна проверять валидность манифеста, уникальность ID, совместимость версии, конфликт маршрутов и permissions, существование и целостность assets/image, набор запрашиваемых capabilities и допустимость зависимостей. Не извлекать архивы с path traversal, не следовать опасным symlink, ограничить размеры. Установку/обновление/удаление может инициировать **только глобальный admin**, с аудитом. Для опасных расширений показывать предупреждение о привилегиях; доверие и установка оператором — явное действие.

## 4. Архитектура исполнения

Реализовать `PluginManager` в ядре и **изолированные backend-процессы** плагинов. Возможен `plugin-runner`/manager как отдельный сервис, который запускает и останавливает OCI/исполняемые плагины, следит за health и перезапусками. Каждый плагин разговаривает с ядром через **версионированный gRPC API**, либо совместимый контракт по HTTP для простой интеграции; для первого релиза выбрать один основной transport и задокументировать его. Это **не** обычная загрузка Go `.so`; плагины могут быть написаны на Go, Python и других языках.

Менеджер хранит реестр установленных плагинов и их желаемое состояние (`discovered`, `installed`, `enabled`, `disabled`, `incompatible`, `unhealthy`, `failed`) в Postgres; текущее health-состояние вычисляется отдельно. Перезапуск ядра не требуется для загрузки нового Go-кода, потому что он не загружается в ядро; для MVP допустим **reload/restart plugin manager после установки**. Целевое API: обнаружить, validate, install, enable, disable, upgrade, inspect health, unload/remove (без автоматического удаления данных). Изменение состояния должно быть транзакционно согласовано с registry и иметь объяснимую ошибку.

Встроенные TASKS, ATTACK_DEFENSE и PENTEST остаются нативными компонентами Go, однако публикация их extensible contract (`BoardModuleProvider`) и всех плагиновых реализаций проходит через **единый discovery/capability registry**. Не заставлять встроенные модули работать как отдельные OCI контейнеры. Наличие плагиновой системы не должно тормозить core endpoints.

## 5. Расширяемые поверхности (extension points)

Plugin manifest может объявлять (по отдельности, а не все сразу):

1. `pages` / `menus` / `widgets`: страницы участника или администратора, элементы навигации, панели в явно предусмотренных slot'ах; административный интерфейс «Плагины» и настройки конкретного плагина.
2. `api`: namespaced маршруты под `/api/v2/plugins/{pluginId}/...` с единой аутентификацией, permission+scope validation, rate limiting, ограничением метода, path/query/body size и проксированием в процесс плагина. Путь не должен давать обход маршрутизатора ядра.
3. `events`/`hooks`: подписки на документированные события (`submission.judged`, `board.completed`, `instance.created` и т. п.) через NATS JetStream; стабильные payload schemas, retries, DLQ/replay, идемпотентность и deduplication. Нельзя дать хукам изменять verdict/score задним числом без отдельной разрешённой команды ядру.
4. `challenge_types` / `board_module_types`: новые типы заданий, проверок и/или Board Module, schema настроек, UI создания и решения, логика проверки через async job. Ядро валидирует результат и **единолично** начисляет баллы.
5. `deployment_runtime_drivers`: дополнительно регистрируемые драйверы `VM` или `CONTAINER`, реализующие `Plan/Provision/Inspect/Reset/Destroy`, с capability negotiation; политика доступа/лимиты/секреты/аккаунты инстансов остаются в core orchestrator.
6. `integrations`: новые внешние интеграции, например Git, и их UI, credential references, проверка подключения.
7. `settings`/`storage`: JSON Schema настроек плагина, зашифрованные секреты по scoped reference, изолированная БД/схема и версионированные миграции, позволяющие вести собственные данные.

Плагины **не должны** напрямую регистрировать произвольные middleware ядра, заменять авторизацию, выдавать глобальные роли, читать полные чужие secrets, делать прямые UPDATE в scoring tables, перехватывать любой маршрут через wildcard, менять существующие таблицы ядра. Если необходим новый контролируемый hook до выполнения операции, явно проектировать его как policy-aware versioned capability, а не как monkey patch.

## 6. Frontend — установка без пересборки Vue

Vue shell получает от backend metadata доступных, совместимых и включённых плагинов, nav items и UI routes, видимых **текущему пользователю** по фактическим правам. Не `import` из исходников плагина при сборке aCTFb.

Для MVP — **sandboxed iframe на отдельном origin** с проверкой origin, ограниченным `postMessage` bridge и capability-scoped backend API, без передачи access/refresh tokens в iframe. Через bridge доступны theme tokens, текущий контекст (boardUuid/moduleUuid), безопасная навигация и результаты разрешённых действий. Изолировать plugin frontend CSP и iframe sandbox. Это обеспечивает независимую сборку и развёртывание плагинов.

Для доверенных аддонов разрешить вторую модель `trusted-js`: заранее скомпилированный ESM bundle, загружаемый runtime и монтируемый в предоставленный Vue slot. **Явно объяснить риск:** JS в origin платформы имеет те же возможности, что и код основного frontend; это не песочница и требует проверки пакета, pinned assets и явного подтверждения администратором. Не позволять автору плагина незаметно включить trusted mode. Изменения основного frontend bundle при установке аддона не допускаются.

UI аддонов наследует дизайн-токены aCTFb, основанные на референсе Proxier, но бизнес-логику и файлы Proxier не использует.

## 7. RBAC и безопасный API ядра

Глобальный `admin` вправе `plugin.install|enable|disable|upgrade|remove|configure` (допустимо одно `plugins.manage` permission, разделённые действия должны всё равно аудироваться). Редактор Board не может устанавливать код, запускать контейнер плагина или менять capabilities. Может настраивать Board-level функциональность уже одобренного аддона **только** с scoped permissions и явно выданными правами.

Новые namespaced permissions (`plugin.<vendor>.<name>.<action>`) регистрируются из манифеста, но **не выдаются автоматически**. Plugin runtime получает service identity с явно одобренными capabilities; привилегии не должны превышать выданные администратором и запрошенные в manifest. Сервер ядра проверяет policy и ownership **перед** вызовом плагина и **до** выдачи данных/секретов. Плагин получает минимальные объекты, а не прямое соединение с основными таблицами или root Docker socket.

Задания, где плагин исполняет пользовательский код, должны проходить через изолированный worker с resource/network/time limits; нельзя считать сам plugin process безопасной песочницей для непроверенных student submissions. Сервисный токен не логировать и не передавать frontend.

Плагиновые данные: предпочтительно отдельная Postgres schema/database с ограниченным DB role (без доступа к таблицам ядра), отдельные object key prefixes. Core APIs для чтения данных Board/Submissions должны разрешать только scope/capabilities. При выключении данные сохраняются; миграция upgrade обратима там, где это реально, и rollback не обещать без поддержанной схемы.

## 8. API и операции

Предлагаемые маршруты (адаптировать в `docs/swagger.yaml`, сохраняя `/api/v2`):

```text
GET    /api/v2/admin/plugins
POST   /api/v2/admin/plugins/validate
POST   /api/v2/admin/plugins/install
GET    /api/v2/admin/plugins/{id}
POST   /api/v2/admin/plugins/{id}/enable
POST   /api/v2/admin/plugins/{id}/disable
POST   /api/v2/admin/plugins/{id}/upgrade
DELETE /api/v2/admin/plugins/{id}
GET    /api/v2/admin/plugins/{id}/logs
GET    /api/v2/plugins                       # Только видимые пользователю plugins+UI metadata
GET    /api/v2/plugins/{id}/...              # Namespaced proxy, declared routes only
```

У `PluginManager` должно быть public API для получения capability definitions; UI должен показывать версию плагина, статус/health, совместимость, author, необходимые разрешения, действия manage и страницу настроек. Для отклонённого пакета объяснять конкретную причину. `install` не означает автоматическое `enable` без явного подтверждения запрашиваемых полномочий.

Описать совместимость `Plugin API v1` и upgrade handshake, а также observability: request IDs, metrics `plugin_up`, `plugin_requests_total`, latency/errors, health/readiness, structured logs, job events, audit installing/admin actions. Всё отключается без остановки всего aCTFb.

## 9. Проверяемый пример, который доказывает реальную плагинность

**Обязательно написать два отдельно распространяемых demo addons** в `examples/plugins` (не включать их исходники в сборку core):

- `example.hello`: admin page + participant page, собственный backend API endpoint, собственная настройка и собственное permission, запись/чтение данных в private schema; установка и enable через manager, а frontend появляется в sidebar без Vite/Go rebuild.
- `example.score_events`: подписывается на `submission.judged`, ведёт отдельную статистику и показывает dashboard. Повторная доставка события не дублирует данные и не может менять score.

После этого добавить optional sample `example.custom_checker` для нового checker типа, чтобы доказать extension point. Демонстрации могут быть выключены по умолчанию.

**Приёмочные тесты:**
- Установить новый плагин в уже собранную aCTFb, перезапустить/перезагрузить только plugin manager (если нужно); endpoint, собственная страница и пункт меню работают без пересборки Go и Vue.
- Выключить плагин: страницы/маршруты недоступны, другие функции работают, данные сохраняются. Включить повторно — данные не исчезли.
- Падение plugin process → core остаётся healthy, маршруты этого плагина возвращают контролируемые 503 и понятный статус.
- Недопустимый/несовместимый manifest, конфликт `id`/routes/permissions, недостаточные capabilities отклоняются до запуска.
- Пользователь без permission не вызывает endpoint через прямой URL; Board A не выдаёт доступ к Board B; revoked grant вступает в силу.
- Отсутствие дублирования при NATS redelivery; запрет прямого изменения баллов и выдачи credentials.
- Обновить плагин с безопасной миграцией данных; не запускать HTTP handlers на частично применённой схеме.
- Тесты для архива с path traversal, XSS в метаданных, iframe message origin check, unavailable plugin backend, превышение timeouts.

## 10. План внедрения и делегирование Codex/DeepSeek

1. Основной Codex: закрепляет границы Plugin API, RBAC, формат manifest, маршрутизацию, события и миграции до начала параллельных изменений.
2. Субагент `plugin-manager`: manifest parser, discovery, lifecycle, installation, registry, health, admin endpoints.
3. Субагент `plugin-protocol`: protobuf/gRPC `Plugin API v1`, service identity, event subscriptions, scoped core SDK.
4. Субагент `plugin-frontend`: Vue shell plugin discovery, sandboxed iframe host, navigation, settings, design tokens.
5. Субагент `plugin-test-addons`: two independent example plugins and integration tests against pre-built artifacts.
6. Основной Codex: интегрирует, обновляет Swagger и docs, проверяет `go test`, реальные runtime integration tests, сборку frontend и **инсталляцию плагина после сборки core**. Использовать `ali/deepseek-v4.1-flash` в дочерних сессиях только при реально поддерживаемой конфигурации; факт использования модели проверять, не декларировать без проверки.

**Необходимая архитектурная граница:** наличие собственных встроенных `BoardModuleProvider` и `RuntimeDriverRegistry` не заменяет систему устанавливаемых плагинов. Статическая регистрация — допустимая внутренняя реализация **builtins**, но не выполнение требования о CTFd-style addons.
