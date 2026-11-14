# aCTFb — дополнение к ТЗ Codex: универсальная конфигурация деплоя

> Редакция: 12.11.2026, v2.0 (scoped RBAC и modular registry). Источники: локальные архивы `proxier-main.zip` и `aCTFb-main.zip`. Это **проектируемое расширение aCTFb**, а не описание уже реализованной системы. В случае конфликта с предыдущим ТЗ этот документ имеет приоритет для deployment/instances/Pentest.

## 1. Что взять из Proxier

Исследованы: `docs/modules/servers.md` (Templates/Manifest), `docs/processes/servers/template-authoring.md`, `docs/processes/servers/server-redeploy.md`, `docs/adr/0003-proxier-rendered-stack-templates.md`; код `internal/modules/servers/{manifest,templates,render,deploy}`.

В Proxier конфигурация развёртывания — это **версионированный пакет** `manifest.yaml` + файлы. Внутри манифеста указаны входные параметры, генерируемые значения, файлы, шаги установки/редеплоя/удаления, проверка здоровья и опубликованные endpoints. Есть editor, save draft, validate, preview, immutable publish, diff/plan, apply, upgrade, rollback и лог jobs. Секреты не светятся в preview/diff. Изменение default version не обновляет уже работающие серверы автоматически.

**НЕ переносить 1:1** Proxier SSH bootstrap, shell-steps, установку Docker, `docker compose`, VPN/proxy/DNS и серверные endpoint-типы. Там целевой объект — SSH-сервер со стеком, в aCTFb — **изолированная учебная среда по запросу**, состоящая из логических VM/CONTAINER nodes, независимо от поставщика инфраструктуры. Идея — скопировать подход к декларативным шаблонам и предсказуемому жизненному циклу, не код и не бизнес-логику.

## 2. Базовый контракт: Kind ≠ Driver

**Resource kind** — логический тип узла, поддерживаемый в любых подходящих реализациях:

- `VM`: виртуальная машина; драйверы `proxmox`, `kubevirt`.
- `CONTAINER`: контейнер; драйверы `docker`, `podman`, `kubernetes`.

`kind` содержится в **узле шаблона** и определяет общие свойства и допустимые операции. `driver` содержится в **административной интеграции** и определяет механизм выполнения. Студенту и большинству форм задания виден `kind`, а не конкретный движок. Один логический шаблон должен работать с несколькими совместимыми драйверами без переписывания общих полей; невозможные для конкретного драйвера возможности должны детерминированно отклоняться **на Validate/Preview до запуска**, а не игнорироваться.

`DeploymentTemplate` → immutable `TemplateVersion` → `DeploymentBinding` (версия + соответствия kind→интеграция, параметры, политики Board/Module/Task) → `Instance` → `InstanceNode` → реальные ресурсы провайдера.

- **Template:** описание *что создать*, без секретов провайдера и без привязки к его UUID/IP.
- **Integration:** конфигурация *где и как исполнять* (kind, driver, API endpoint, credentials, resource pools, network/publication policy, квоты). Доступна только admin.
- **Binding:** какой шаблон/версию и какие интеграции использовать в конкретной задаче либо Pentest-модуле. Для чистой VM/CONTAINER среды достаточно одного target; для смешанной разрешить `targets.VM` и `targets.CONTAINER`, **только если есть поддерживаемая схема межузловой сети**. По умолчанию все nodes одного kind работают на одной интеграции.
- **Instance:** развёртывание для конкретного владельца `USER` или `TEAM`; runtime resource IDs/статусы/сгенерированные секреты хранятся отдельно от шаблона.

**Смысл `source.ref`:** для CONTAINER — OCI image ref (в соревнованиях фиксированный digest), для VM — стабильное логическое имя VM-шаблона, которое конкретная интеграция разрешает в Proxmox VMID или KubeVirt source/volume. `templateVmid` — **специальное поле Proxmox**, не универсальный VM-синтаксис.

## 3. Формат пакета и пример manifest.yaml

Пакет состоит из `manifest.yaml` и опциональных текстовых/binary файлов (конфиги приложения, cloud-init, init data, учебные файлы). Структура и пример ниже — **новое предложение**, которого пока нет в Swagger/Go aCTFb. Поддержать YAML как пользовательский формат, сохранять нормализованную типизированную модель в backend. Публичный API остаётся `/api/v2`.

```yaml
apiVersion: actfb.dev/v1alpha1
kind: DeploymentTemplate
metadata:
  name: Web security lab
  slug: web-security-lab
  description: Изолированный учебный стенд

parameters:
  - key: difficulty
    label: Difficulty
    type: choice
    options: [easy, normal]
    default: easy

generated:
  - key: lab_password
    kind: password
    length: 24
    rotateOnReset: true

nodes:
  - key: web
    name: Vulnerable website
    kind: CONTAINER
    source:
      type: OCI_IMAGE
      ref: registry.example/ctf/web@sha256:REPLACE_WITH_REAL_DIGEST
    resources:
      cpuMillicores: 500
      memoryMiB: 512
      diskMiB: 1024
    env:
      - name: LAB_PASSWORD
        valueFrom: { generated: lab_password }
      - name: DIFFICULTY
        valueFrom: { parameter: difficulty }
    files:
      - source: files/app-config.yml.tmpl
        target: /etc/lab/config.yml
        mode: '0440'
    endpoints:
      - key: web-http
        protocol: HTTP
        targetPort: 8080
        exposure: PUBLIC
        display: { label: Web challenge }
    driverOptions:
      docker:
        init: true
      kubernetes:
        workload: Deployment

  - key: workstation
    name: Workstation VM
    kind: VM
    source:
      type: VM_TEMPLATE
      ref: ubuntu-workstation-lab
    resources:
      cpuMillicores: 2000
      memoryMiB: 2048
      diskMiB: 10240
    bootstrap:
      cloudInitFile: files/workstation-cloud-init.yaml.tmpl
    endpoints:
      - key: ssh
        protocol: SSH
        targetPort: 22
        exposure: PRIVATE
    driverOptions:
      proxmox:
        linkedClone: true
        storage: local-lvm
      kubevirt:
        storageClassName: longhorn

checks:
  - kind: tcp
    node: web
    endpoint: web-http

lifecycle:
  reset: RECREATE
  # TTL по правилам TASK либо срокам PENTEST; задаётся binding/policy,
  # а не является безусловной настройкой шаблона.
```

**Примечание к примеру:** смешанный VM+CONTAINER стенд можно опубликовать и запускать **только после** настройки совместимой сетевой связности и политики изоляции. Без неё валидатор обязан отказать. `REPLACE_WITH_REAL_DIGEST` — демонстрационная заглушка; не разрешать запуск с ней. `files/...` — относительные пути к пакету. `PRIVATE` endpoints выдаются по правилам доступа Swagger; `PUBLIC` host/port выделяет backend по `InstancePublicationPolicy`, а не пользовательский manifest.

### 3.1. Общие поля

- Metadata: slug, name, description, version/ref, requirements/capabilities.
- Parameters: `key`, `label`, `type` (`string/int/bool/choice/text`), `required/default/sample`, `secret`. Секреты указывать ссылками, не смешивать в YAML с plaintext.
- Generated: `uuid/password/hex/base64`, длина и политика сохранения при reset. Значения генерируются **на конкретный инстанс**, не глобально на шаблон.
- Node: `key`, `name`, `kind`, `source`, `resources`, `env` (для контейнера), `bootstrap` (для VM), `files` (допустимые способы доставки зависят от возможностей драйвера), `endpoints`, `checks`, `driverOptions`.
- Resources: CPU, RAM, disk, опциональные limits/requests и иные безопасные общие поля. Явно указать единицы измерения.
- Network: приватная сетевая область каждого инстанса, DNS-имена узлов, декларация разрешённых связей; выдача внешних адресов по интеграционной publication policy.
- Endpoints: использовать понятия текущего API (`EndpointConfig`, `AccessEndpoint`): `key`, protocol, targetPort, `PUBLIC|PRIVATE`, display-only command.
- Lifecycle: create, inspect, reset, destroy, expiration, reconciliation. Пользователь не управляет нативными параметрами драйверов.

### 3.2. Специальные опции

`driverOptions` — необязательные **namespaced** расширения (`proxmox`, `kubevirt`, `docker`, `podman`, `kubernetes`). Нужный драйвер получает только свой блок; остальные не влияют на исполнение и отмечаются в preview как *неиспользуемые в выбранном target*. Каждая реализация предоставляет **строгую типизированную схему и валидатор**. Не передавать произвольный YAML/JSON напрямую в API Proxmox/Kubernetes/Docker и не позволять обходить политики безопасности.

Примеры допустимых расширений: Proxmox storage/bridge/linked clone; KubeVirt storage class/instance type; Docker init/user/healthcheck; Podman rootless/network; Kubernetes namespace constraints/nodeSelector/resource settings. Дублирующие общие поля параметры (`memory`, `cpu`) задавать только в common spec; если нужен override, явно документировать приоритет и совместимость, избегать конфликтующих значений.

**Capabilities** драйвера: supported `kinds`, source types, bootstrap/cloud-init, shared networking, persistent/ephemeral volumes, public exposure, snapshot vs recreate reset, files injection, readiness. Отсутствие требуемой capability → ошибка проверки с именем поля/узла/интеграции. Никаких неявных fallbacks с потерей функциональности.

## 4. Абстракция драйверов в Go

Реализовать registry встроенных адаптеров без `plugin.Open`; для **сторонних RuntimeDriver** предусмотреть подключение через устанавливаемый plugin/sidecar и Plugin API v1 согласно `aCTFb_PLUGIN_SYSTEM_TZ.md`. Адаптер не должен знать о Board, Module, scoring или правах ученика: на вход получает доверенный `DesiredNode` от backend/worker, а не сырой запрос пользователя.

```go
// Иллюстративный контракт; имена и состав методов адаптировать к коду.
type RuntimeDriver interface {
    Name() string                      // proxmox, kubevirt, docker, podman, kubernetes
    Kind() ResourceKind                // VM / CONTAINER
    Capabilities(ctx context.Context, cfg IntegrationConfig) (Capabilities, error)
    Validate(ctx context.Context, cfg IntegrationConfig, node NodeSpec) []Finding
    Plan(ctx context.Context, target Target, desired NodeSpec, current *NodeState) (Plan, error)
    Apply(ctx context.Context, target Target, desired NodeSpec, op Operation) (ProviderResource, error)
    Inspect(ctx context.Context, target Target, id string) (ProviderResource, error)
    Destroy(ctx context.Context, target Target, id string, op Operation) error
}
```

- `Operation` содержит идемпотентный `operationID`, `instanceUUID`, `nodeKey`, `generation`. Стабильные provider labels/tags позволяют найти ресурсы после падения worker, повторного получения NATS-сообщения или частичного успеха.
- Backend владеет access control, template revision, quotas, TTL, выделением endpoints и журналом операций. Driver отвечает только за provider-specific provisioning/inspection/teardown.
- Реализация reset: **уничтожить и создать чистый ресурс** (общий обязательный сценарий) или оптимизированное восстановление snapshot при явно объявленной capability. Не менять семантику reset без проверки флагов/политик.
- Для контейнеров: Docker container, Podman container, Kubernetes Pod/Deployment/Service — это разные нативные объекты, но один логический `CONTAINER`-узел; backend API не должен от этого зависеть.
- Для VM: Proxmox clone по разрешённому template VMID, KubeVirt clone/VM/DataVolume из разрешённого source; VM template ref разрешать через integration mapping/каталог образов.
- Публикация endpoints — отдельная policy-составляющая; драйвер может сообщить private addresses/ports, а публичная маршрутизация/порты/домены выделяются строго через доверенную конфигурацию интеграции.

## 5. Lifecycle по аналогии с Proxier

**Template lifecycle:** `draft → validate → preview → publish immutable version → bind to task/pentest`. Версии 1,2,3... Не изменять опубликованные файлы. Default version влияет только на новые bindings, если администратор не закрепил конкретную версию. Import/export zip — полезно, import git — отдельный этап после безопасной проверки URL/объёмов/путей. Дать форму обычных настроек и advanced YAML/file editor (можно использовать CodeMirror).

**Runtime lifecycle:** `requested → queued → provisioning → running → resetting/destroying → expired/error/removed`. В публичном `InstanceStatus` сохранить текущие значения `PROVISIONING`, `RUNNING`, `RESETTING`, `DESTROYING`, `ERROR`, `EXPIRED`; `queued/removed` допускаются как внутренние фазы/события, а не новые публичные enum без изменения Swagger.

**Dry-run / Preview:** валидировать manifest, render с примерными значениями, проверить driver capabilities и quotas, сформировать *план* по node (create/update/recreate/delete), нужные образы/шаблоны, ресурсы, endpoints, файлы, проверки и предупреждения. Применять только после явного действия admin/ученика согласно access policy. Никаких реальных VM/контейнеров во время preview. Секреты mask.

**Deploy:** snapshot версии и привязки сохраняется на момент постановки job. Worker выполняет операции идемпотентно, отслеживает шаги и logs, делает проверку готовности, хранит внешний resource ID, выдаёт endpoints только после успешной конфигурации. При ошибке — `ERROR`, понятный шаг, безопасный retry либо compensating cleanup. Не оставлять orphan resources.

**Upgrade/redeploy:** работающий учебный инстанс **не обновляется автоматически** при публикации новой версии. Для будущих новых инстансов используй выбранную версию; изменение активного допускается только по отдельной admin-операции, с diff и подтверждением, если не нарушает честность соревнования. Для первоначального MVP достаточно reset/recreate с фиксированной версией; hot-upgrade активной лаборатории не обязателен.

**Destroy/expiry:** истечение TTL или end window запрещает дальнейшую работу, worker удаляет ресурсы, повторная очистка безопасна; периодический reconciliation сравнивает Postgres и provider inventory по labels/tags. Принцип: одна учебная среда не должна мешать другой, команды работают в собственных инстансах. Проверки после перезапуска приложения и когда провайдер недоступен.

## 6. Контракт Swagger aCTFb: обязательные изменения

Существующий `docs/swagger.yaml` **не универсален**; менять его осознанно и регенерировать модели. Сейчас:

| Existing definition | Current behaviour | Required change |
|---|---|---|
| `InstanceNodeKind` | `CONTAINER`, `VM` | **Оставить**, подходит идеально. |
| `InstanceNodeConfig` | `CONTAINER` требует `image`, VM требует `templateVmid` | Добавить универсальный `source`/`resources`/`driverOptions` или ссылку на версию шаблона. `templateVmid` оставить только как legacy Proxmox mapping; новая VM не должна зависеть от VMID. |
| `TaskInstanceConfig` | `providerIntegrationUuid`, `nodes`, TTL | Сохранить endpoint и общую семантику; добавить `deploymentTemplateUuid` + `templateVersion`, параметры/binding. В переходный период поддержать inline `nodes`, но не одновременно с template ref. |
| `PentestConfig` | `proxmoxIntegrationUuid`, все nodes VM | Перейти на `providerIntegrationUuid`/binding по kind, отказавшись от эксклюзивной зависимости от Proxmox. Поддержать VM на Proxmox и KubeVirt; смешанные VM/Container — после реализации сетевой изоляции. Срок окончания Pentest по-прежнему конец модуля/Board, без независимого TTL. |
| `IntegrationType` | `GITLAB/GITHUB/GITEA/PROXMOX/CONTAINER` | Явно разделить category/type (`VM`/`CONTAINER` для вычислений) и driver (`proxmox`, `kubevirt`, `docker`, `podman`, `kubernetes`). Для старого `PROXMOX` — compatibility migration/alias. Git-интеграции не менять. |
| `IntegrationCreate/PatchRequest` | type, baseUrl, credentials, arbitrary settings, publication policy | Добавить документированный `driver`; `settings` валидировать по драйверу; секреты write-only/encrypted. `InstancePublicationPolicy` — единственный источник диапазонов public host/ports. |
| `Instance` / `InstanceNode` / `AccessEndpoint` | Статус, owner, nodes, endpoints | **Сохранить** shape participant API; по необходимости добавить optional safe metadata (template version), но не provider credentials/native API details. |

Не вводить новый `/api/v3` только из-за рефакторинга. Если совместимость сохранения/чтения старых `nodes` невозможна, сделать явную миграцию и отдельно описать breaking change. Все client/OpenAPI-generated модели обновить на основании исправленного Swagger, не редактировать сгенерированные файлы вручную.

### Admin API (проектируемые ресурсы, не существующие в архиве)

На базе `/api/v2/admin` добавить маршруты, согласованные со Swagger-конвенциями (global admin для publish/credentials; scoped role для разрешённого read/bind): `/deployment-templates` list/create/detail, `/deployment-templates/{uuid}/draft` get/put/validate/preview/publish, `/deployment-templates/{uuid}/versions` list/detail/diff, импорт/экспорт по необходимости. Для binding существующие Task/Pentest create/config PATCH/GET расширить ссылкой на версию вместо ещё одного независимого ресурса. Для реальных jobs использовать доступный admin instances API; job logs/status реализовать с авторизацией и аудитом.

## 7. UI (в стилистике Proxier, а не с его логикой)

Глобальный `admin`: **Deployment templates** → список/версии → редактор дерева `manifest.yaml` + files (или структурная форма для упрощённого шаблона) → Validate с line numbers → Preview для выбранного integration driver → Publish → привязать к Task/Pentest. На экране задания показывать kind, объём ресурсов, выбранную интеграцию и итоговые endpoints. Страница инстанса: nodes/статус, ошибки, срок, адреса, reset/destroy, история job. Визуально использовать существующие токены Rosé Pine Console из Proxier, но без его бизнес-сущностей.

Студент: только кнопки, предусмотренные политикой модуля (`Start`, `Reset`, `Destroy`, `Extend`, если разрешено), время, доступные endpoints и ошибки. Не показывать provider driverOptions, токены, VMIDs, image pull credentials, приватную топологию других инстансов.

## 8. Безопасность и корректность

- Только глобальный `admin` может создавать/редактировать/публиковать доверенные DeploymentTemplate и управлять provider credentials. `board_owner` может привязать к своей Board/Module только уже опубликованный и допущенный администратором шаблон, если он входит в allowlist его scope. `board_editor` — лишь при отдельном `deployment_templates.use` grant. Студент не передаёт raw provider options, OCI image или cloud-init в endpoint запуска.
- Запрет host network, privileged, Docker socket, hostPath/host mounts, host devices, опасных capabilities, неконтролируемого egress, произвольных SSH bootstrap скриптов. Только явно доверенная политика может разрешить исключение для специальных стендов, отдельно от пользовательского manifest.
- Kubernetes/Proxmox credentials только у доверенного worker, минимальные разрешения, не внутри guest/pod; network policies/выделенная сеть на инстанс; DNS/SSH/HTTP endpoints только из проверенных источников.
- Input validation: manifest schema, файлы и пути без traversal/symlink, размеры, zip bomb; строгий template render без случайных значений из окружения; allowlist параметров и драйверских опций.
- Идемпотентность при `at-least-once` NATS, уникальность owner instance, лимиты конкурентных операций, release портов/адресов/ресурсов, retention журналов без секретов.
- Гарантировать невозможность произвольного доступа от студенческого контейнера в управляющую сеть или соседний стенд. Для действительно опасных Pentest нагрузок VM-изоляция предпочтительна, контейнерная среда — отдельная доверенная настройка администратора.

## 9. Порядок реализации Codex и тесты

1. **Контракт/архитектура:** прочитать имеющийся `docs/swagger.yaml` и Proxier manifest/template/deploy; описать новый `DeploymentTemplate` + `Binding` + `RuntimeDriver` и совместимость. Сначала поправить OpenAPI + миграции + типизированную модель, затем HTTP/API. Этот шаг — после общих blockers P0.
2. **Template engine:** YAML parser, strict validation, versioned drafts/publish, sample rendering, secret masking, preview/diff, проверки файлов/зависимостей. Unit tests (валидный/сломанный YAML, отсутствующий param, неподдерживаемое поле, неизвестный driver option, secret redaction, неизменность версии).
3. **Driver registry, orchestration worker:** fake/mock runtime для тестов, NATS jobs, reconcile, TTL, state transitions, driver capabilities, endpoint publication. Contract tests на kind independence, retry, duplicate job, cancellation after partial create, unsupported feature.
4. **MVP Docker + Proxmox:** полноценный запуск одного CONTAINER-узла Docker и одной VM Proxmox из **одной схемы шаблонов**, с reset/destroy/cleanup и проверяемыми endpoints. Не пытаться реализовать сразу все пять драйверов на уровне production.
5. **Другие драйверы:** Podman, Kubernetes для CONTAINER, KubeVirt для VM. После каждого — тесты на одинаковый контракт и capabilities; не добавлять фиктивные адаптеры, которые только возвращают success.
6. **Advanced:** безопасная двухтипная сеть для VM+CONTAINER в одном Pentest, импорт/экспорт, версия и rollout, snapshot оптимизации по необходимости. Пока совместимая сеть не реализована, смешанные templates должны быть отклонены валидатором с объяснением.
7. **Frontend:** реальный CRUD, версия/редактор/preview, подключение шаблона к заданию, студентский instance UI. Ни одного неработающего кнопочного действия или mock success.

**Обязательные acceptance tests:**

- Один `CONTAINER` manifest создаёт сопоставимую среду через Docker и Podman (при реализации Podman), а затем Kubernetes — без изменения common spec.
- Один `VM` manifest можно связать и с Proxmox, и с KubeVirt, а native VMID нигде не является обязательным общим полем.
- Неизвестная capability/provider option → понятная валидационная ошибка; не silent ignore.
- Draft/validate/preview не создают инфраструктуру. Published version immutable; новые версии не меняют активные инстансы без явного действия.
- В момент kill worker после создания provider resource retry находит ресурс по idempotency/tags и не создаёт дубликат.
- RESET даёт чистый инстанс, DESTROY/TTL освобождают все ресурсы и публичные порты; чужие endpoints и secrets не доступны.
- Проверка API/Swagger на отсутствие несовместимых ответов, в том числе для Task/Pentest и существующих Integration endpoints.

## 10. Субагенты DeepSeek v4.1 Flash

Главный Codex делегирует отдельными задачами: `contract-auditor` (только чтение Swagger и проектирование миграции), `template-engine` (parser/validation/versioning), `runtime-docker`, `runtime-proxmox`, `runtime-podman`, `runtime-kubernetes`, `runtime-kubevirt`, `qa-security`, `frontend-template-editor`. **Перед параллельной реализацией** главный агент утверждает общий Go interface и schema, закрепляет зоны файлов; одновременное изменение swagger/migrations/shared interfaces несколькими агентами запрещено. Для каждого задания проверить фактически выбранную модель `ali/deepseek-v4.1-flash`, не имитировать делегирование. Самостоятельно интегрировать результаты, запускать тесты, указывать незавершённые критерии.

## 11. Прямая команда Codex

> Изучи `aCTFb_DEPLOYMENT_TZ_v3.md` и существующий `docs/swagger.yaml`. Перенеси из Proxier принцип `manifest + files`, draft/validate/preview/publish, immutable versions, diff/plan и lifecycle jobs. В aCTFb типы ресурсов строго `VM`/`CONTAINER`, а драйверы — отдельные сменные адаптеры. Универсальные поля не должны зависеть от Proxmox VMID, Docker CLI или Kubernetes API. Реализуй работающий MVP Docker+Proxmox, сохрани совместимость API и явно расширь контракт для KubeVirt, Podman и Kubernetes; не подменяй их заглушками. Делегируй независимые части DeepSeek субагентам, но сам координируй интерфейсы, интеграцию и тестирование.


## 12. RBAC и runtime registry (обязательная интеграция с основным ТЗ v3)

Нормативные требования — **раздел 4B** в `aCTFb_CODEX_TASK_v4.md` и Plugin System ТЗ `aCTFb_PLUGIN_SYSTEM_TZ.md`. Deployment subsystem не вводит собственный независимый список пользователей/ролей и не смешивает тип ресурса, право на действие и драйвер исполнения.

**Раздельные полномочия:**

| Действие | Кому разрешено |
|---|---|
| Создать/обновить provider integration, поменять credentials/network restrictions | Только глобальному `admin` |
| Создать/изменить/опубликовать DeploymentTemplate со source, driverOptions, secrets | Только глобальному `admin` (в MVP) |
| Получить preview и bind опубликованной версии к Board/Module | `admin`; `board_owner` своей Board и при наличии allowlist на конкретный template; `board_editor` с отдельным grant |
| Показать студенту разрешённые endpoint/TTL | Участнику, которому доступны конкретные Board, Module и свой Instance; без provider metadata/secrets |
| Start/reset/destroy/extend своего стенда | Авторизованному участнику только в рамках action policy, срока, quota и ownership |
| Принудительно управлять Instance внутри Board | `admin` или `board_owner` в своём scope с `module.instances.admin` |
| Смотреть системный audit, менять global quotas, sandbox/network policies | Только глобальному `admin` |

Реальная проверка: `Authorize(actor, permission, resourceCtx)` в backend + повторная проверка нормализованного security context в worker; нельзя полагаться на выключение кнопок в UI. Не позволять `board_owner` или `board_editor` указывать произвольную VM image, OCI image, hostPath, privileged, host network, driverOptions или чужую integration UUID через binding endpoint. Даже опубликованный template доступен только после отдельного допуска в scope (`template_allowlist`/approved bindings). Не выдавать секреты/токены в preview/diff, client API, logs, audit или webhooks.

`RuntimeDriverRegistry` — **отдельная** точка расширения от `BoardModuleRegistry`. Новые сторонние drivers должны уметь подключаться как independently installed addons, не требуя пересборки core; встроенные драйверы могут оставаться нативными. Module `PENTEST` либо `TASKS` запрашивает создание `DesiredDeployment`; orchestrator применяет template/binding и quotas; driver создаёт **только** VM/CONTAINER. В driver-коде запрещены обращения к `Board`, `User`, `Score` и ACL; driver не решает, кому можно запускать стенд. Registry предоставляет `ID`, `Kind`, `Capabilities`, строгую schema `driverOptions`, `Plan/Provision/Inspect/Reset/Destroy`, совместимость и reason для неподдерживаемой операции.

Провайдеры Docker/Podman/Kubernetes/Proxmox/KubeVirt регистрируются **в доверенной сборке**; загрузка пользовательского исполняемого кода и небезопасное применение произвольных driverOptions запрещены. Новые RuntimeDriver добавляются без модификации business logic TASKS/PENTEST и без изменения общего manifest schema. Для тестирования использовать один manifest VM на двух драйверах и один manifest CONTAINER на двух драйверах (по мере появления реализованных адаптеров).

**Дополнительные приёмочные проверки:** владелец Board A не может привязать интеграцию B либо прочитать её credentials; reviewer не может стартовать чужие VM; студент не может подменить template version или driverOptions; после отзыва grant повторный reset/extend отклоняется; async worker не создаёт ресурс после отказа или без действительного авторизованного запроса; owner Board A не может получить список VM Board B через admin Instance API.
