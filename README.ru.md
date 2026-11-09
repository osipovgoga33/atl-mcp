# atl-mcp — Confluence + Jira Server / Data Center для Claude Desktop

[English](README.md)

MCP-сервер на Go (только стандартная библиотека). Ходит в Confluence и Jira
по персональному токену (PAT), запускается локально — значит, работает через
ваш VPN к внутренним серверам. Atlassian Cloud не нужен. Один exe, без Python.

Два инструмента: `atl` — чтение, `atl_write` — запись (виден, только если
запись включена). У обоих параметр `action`.

## Чтение — `atl`

**Confluence**

| action | параметры | что делает |
|---|---|---|
| `conf_search` | `text` / `title` / `space` или `cql`, `limit` | поиск страниц |
| `conf_page` | `id` или `space`+`title`, `format` = `text` \| `storage`, `offset`, `find` | страница; `storage` — исходный XHTML для правки, с `version`. Большая страница — кусками (`offset`); `find` — фрагменты вокруг каждого вхождения |
| `conf_children` | `id` | дочерние страницы |
| `conf_spaces` | `limit` | пространства |
| `conf_attachments` | `id` | вложения |
| `conf_comments` | `id` | комментарии с ответами |
| `conf_download` | `id`, `name` | скачать вложение в папку загрузок |

**Jira**

| action | параметры | что делает |
|---|---|---|
| `jira_issue` | `key`, `comments`, `fields` | задача: поля, подзадачи, связи (с id), вложения, оценка/факт |
| `jira_search` | `jql`, `limit`, `start_at`, `fields` | поиск задач с постраничностью |
| `jira_projects` | `text` | проекты |
| `jira_project` | `project` | типы задач, компоненты, версии |
| `jira_fields` | `text` | поля и id кастомных полей |
| `jira_create_meta` | `project`, `issuetype` | поля для создания: обязательные, допустимые значения (Jira 9+ и старый API) |
| `jira_transitions` | `key` | доступные переходы и их обязательные поля |
| `jira_changelog` | `key`, `field` | история изменений |
| `jira_status_times` | `key` | время в каждом статусе (календарное), переходы |
| `jira_worklog` | `key` | трудозатраты с итогом по людям |
| `jira_watchers` | `key` | наблюдатели |
| `jira_link_types` | — | типы связей |
| `jira_users` | `text`, `project` | пользователи; с `project` — кому можно назначить |
| `jira_filters` | — | избранные фильтры с JQL |
| `jira_download` | `key`, `name` / `attachment_id` / `all` | скачать вложения в папку загрузок |
| `jira_dev_info` | `key` | коммиты, ветки, PR (если есть интеграция с Bitbucket/GitLab) |
| `jira_boards` | `text`, `project`, `type` | доски |
| `jira_sprints` | `board_id`, `state` | спринты |
| `jira_sprint_issues` | `sprint_id`, `jql`, `limit`, `start_at`, `fields` | задачи спринта |
| `jira_board_issues` | `board_id`, `jql`, … | задачи доски |
| `jsm_desks` | — | сервис-деск проекты |
| `jsm_queues` | `desk_id` | очереди с числом задач |
| `jsm_queue_issues` | `desk_id`, `queue_id` | задачи очереди |
| `jsm_sla` | `key` | SLA заявки |

## Запись — `atl_write`

**Confluence**

| action | параметры |
|---|---|
| `conf_create` | `space` и/или `parent_id`, `title`, `body` (storage), `labels` |
| `conf_update` | `id`, `version` (прочитанный), `body` (полное тело), `title`, `message`, `minor_edit`, `allow_shrink` |
| `conf_label` | `id`, `labels` |
| `conf_attach` | `id`, ровно одно из `path` / `content` / `content_base64`, `name`, `message` |
| `conf_comment` | `id`, `body` (storage), `parent_comment_id` |
| `conf_move` | `id`, `parent_id` (то же пространство), `version` |

**Jira** (тексты — вики-разметка Jira)

| action | параметры |
|---|---|
| `jira_create` | `project`, `issuetype`, `summary`, `description`, `priority`, `assignee`, `labels`, `components`, `fix_versions`, `due_date`, `parent`, `fields` {кастомные как есть} |
| `jira_update` | `key` + те же поля, `fields` {}, `update` {} (операции Jira) |
| `jira_bulk_update` | `keys` [] или `jql` (до 50 задач) + те же поля, что у `jira_update`; `dry_run` — «было → стало» без записи |
| `jira_transition` | `key`, `transition` (id, название или целевой статус), `comment`, `resolution`, `fields` |
| `jira_bulk_transition` | `keys` [] или `jql` (до 50 задач), `transition`, `comment`, `resolution`, `fields`; `dry_run` — у каких задач переход доступен |
| `jira_assign` | `key`, `user` (логин или `none`) |
| `jira_comment` | `key`, `body`, `comment_id` — правка |
| `jira_worklog_add` | `key`, `time_spent` (`1h 30m`), `started` (`2026-10-07 10:00`), `comment`, `adjust_estimate` (`auto` \| `leave` \| `new` + `new_estimate`) |
| `jira_worklog_update` | `key`, `worklog_id`, `time_spent`, `started`, `comment`, `adjust_estimate` — только свои записи |
| `jira_link` / `jira_link_remove` | `inward_key`, `outward_key`, `link_type`, `comment` / `link_id` |
| `jira_remote_link` | `key`, `url`, `title` — напр. ссылка на спеку в Confluence |
| `jira_attach` | `key`, `path` / `content` / `content_base64`, `name` |
| `jira_watcher_add` / `jira_watcher_remove` | `key`, `user` |
| `jira_sprint_add` / `jira_backlog` | `sprint_id`, `keys` / `keys` (до 50) |
| `jira_sprint_create` | `board_id`, `name`, `start_date`, `end_date`, `goal` |
| `jira_version_create` | `project`, `name`, `description`, `start_date`, `release_date` |

Пакетные действия: одна задача — один запрос; задачи, где уже так (или уже в
целевом статусе), пропускаются, поэтому вызов можно повторить; ошибка по одной
задаче не останавливает остальные, в ответе — итог по каждой. Через 40 с действие
останавливается и перечисляет, до каких задач не дошло (клиент ждёт ответ около
минуты), — нужно повторить тот же вызов. Одно пакетное
действие — одно подтверждение в Claude Desktop вместо пятидесяти.

Удаления задач, страниц, комментариев и вложений нет намеренно. Удаляются только связи
между задачами и наблюдатели — это обратимо.

## Сознательно не перенесено из mcp-atlassian

- Удаление задач.
- Только для Atlassian Cloud: перенос задачи между проектами, пакетные истории изменений.
- Зависят от плагинов, которых может не быть: формы ProForma, межпроектные иерархии эпиков.
- Создание заявки Service Desk от имени клиента.

## Защиты

- Запись выключена по умолчанию (`ATL_ALLOW_WRITE`). Гард стоит в HTTP-клиенте:
  без флага наружу уходят только GET.
- Белые списки: `ATL_WRITE_SPACES` — пространства Confluence,
  `ATL_JIRA_WRITE_PROJECTS` — проекты Jira (связь и перенос в спринт проверяют
  все задействованные задачи).
- `conf_update` требует версию, прочитанную перед правкой: чужие правки не
  затираются. Новое тело вдвое короче старого — отказ, пока не передан
  `allow_shrink`.
- `conf_move` — только внутри пространства и не под собственного потомка.
- Переход Jira, которого нет, не отправляется — в ответ список доступных.
- Пакетные действия: не больше 50 задач (и для JQL — по общему числу найденных),
  белый список проверяется по каждой задаче выборки, ненайденный ключ — отказ
  целиком; всё это до первой записи.
- Ключи, id, логины, поля, метки, даты, `time_spent`, URL и имена файлов
  проверяются до запроса.
- Вложения на выгрузку — только разрешённые расширения и до 50 МБ: подсказкой
  со страницы нельзя заставить выгрузить ключ или конфиг.
- Скачивание — только с того же сервера (токен не уходит на чужой адрес), в
  папку загрузок, без перезаписи: повтор ложится как `имя (1).ext`.

## Настройка

| переменная | пример |
|---|---|
| `ATL_CONF_URL` | `https://wiki.example.com` |
| `ATL_CONF_TOKEN` | PAT Confluence |
| `ATL_JIRA_URL` | `https://jira.example.com` |
| `ATL_JIRA_TOKEN` | PAT Jira |
| `ATL_ALLOW_WRITE` | `true` — включить запись |
| `ATL_WRITE_SPACES` | `DEV,SAP` — пусто = любые |
| `ATL_JIRA_WRITE_PROJECTS` | `ABC,SD` — пусто = любые |
| `ATL_DOWNLOAD_DIR` | папка загрузок; пусто = `Загрузки/atl-mcp` |
| `ATL_CA_FILE` | путь к корпоративному сертификату (.cer/.crt/.pem, Base-64 или DER) |
| `ATL_CONF_INSECURE_TLS` | `true` — не проверять сертификат Confluence |
| `ATL_JIRA_INSECURE_TLS` | `true` — не проверять сертификат Jira |
| `ATL_INSECURE_TLS` | старый общий флаг: действует на оба |

Можно задать только Confluence или только Jira.

### Сертификат «signed by unknown authority»

Сервер подписан внутренним центром сертификации. Правильно — указать его
сертификат: открыть сервер в браузере → замок → сертификат → «Путь
сертификации» → верхний (корневой) → «Экспорт» в Base-64 `.cer` → путь в
`ATL_CA_FILE`. Проверка остаётся включённой, к системным доверенным добавляется
только этот. Быстрый обход — `ATL_JIRA_INSECURE_TLS=true` (шифрование
остаётся, подлинность сервера не проверяется); действует только на свой
продукт.

### Коллегам — пакет `.mcpb`

Двойной клик по `dist/atl-mcp.mcpb` (или Claude Desktop → Настройки →
Расширения → установить из файла), заполнить URL и токены в форме. Токены
хранятся в системном хранилище ключей.

### Вручную — `claude_desktop_config.json`

```json
"atlassian": {
  "command": "C:/Tools/atl-mcp/atl-mcp.exe",
  "env": {
    "ATL_CONF_URL": "https://wiki.example.com",
    "ATL_CONF_TOKEN": "...",
    "ATL_JIRA_URL": "https://jira.example.com",
    "ATL_JIRA_TOKEN": "...",
    "ATL_ALLOW_WRITE": "true"
  }
}
```

Путь — с прямыми слэшами (Windows их понимает) или с удвоенными обратными;
одинарный обратный слэш ломает JSON, и Desktop молча пропускает весь конфиг.
Второй инстанс (другой Confluence/Jira) — второй блок с тем же exe и другими
переменными.

## Проверка версии

`atl-mcp.exe --version`; строка старта в
`%APPDATA%\Claude\logs\mcp-server-<имя>.log`:
`старт atl-mcp 0.4.2: confluence=... jira=... запись=...`.

## Сборка

`bash build.sh` — тесты, бинарники в `dist/` (Windows, macOS arm64/amd64, Linux)
и `dist/atl-mcp.mcpb`.

## История версий

См. [CHANGELOG.md](CHANGELOG.md).
