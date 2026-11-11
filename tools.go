package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type toolset struct {
	cfg       config
	conf      *client
	jira      *client
	userNames map[string]string // кэш ключ пользователя Confluence → имя
	// bulkBudget — сколько пакетное действие работает до того, как вернуть
	// остаток: клиент (Desktop, удалённая сессия) ждёт ответ около минуты.
	bulkBudget time.Duration
}

func newToolset(cfg config) *toolset {
	return &toolset{
		cfg:        cfg,
		conf:       newClient("Confluence", cfg.ConfURL, cfg.ConfToken, cfg.AllowWrite, cfg.ConfInsecure, cfg.CAFile),
		jira:       newClient("Jira", cfg.JiraURL, cfg.JiraToken, cfg.AllowWrite, cfg.JiraInsecure, cfg.CAFile),
		bulkBudget: 40 * time.Second,
	}
}

var (
	reID       = regexp.MustCompile(`^\d{1,20}$`)
	reSpace    = regexp.MustCompile(`^~?[A-Za-z0-9_\-.]{1,255}$`)
	reIssueKey = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,30}-\d{1,10}$`)
	reLabel    = regexp.MustCompile(`^[^\s:!#&()\[\]^|,]{1,255}$`)
	reField    = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,100}$`)
	reFileName = regexp.MustCompile(`^[^\\/:*?"<>|\x00-\x1f]{1,200}\.[A-Za-z0-9]{1,8}$`)
)

const readDescription = `Чтение Confluence и Jira (Server/DC). Параметр action.
CONFLUENCE:
- conf_search: text и/или title, space, или свой cql; limit (≤100).
- conf_page: id ИЛИ space+title; format = text (по умолч.) | storage (исходный XHTML — нужен перед правкой). Возвращает version. Большая страница — кусками: offset (с какого символа, ответ подскажет следующий). find — куски вокруг каждого вхождения текста (номер задачи, цвет правок вида rgb(255,0,0) в storage).
- conf_children: id. conf_spaces: limit. conf_attachments: id. conf_comments: id.
- conf_download: id, name — скачать вложение в папку загрузок.
JIRA:
- jira_issue: key; comments=true; fields — доп. поля (customfield_…).
- jira_search: jql, limit (≤100), start_at, fields.
- jira_projects: text (фильтр). jira_project: project — типы задач, компоненты, версии.
- jira_fields: text — поиск полей (id кастомных полей). jira_create_meta: project, issuetype — поля для создания, обязательные, допустимые значения.
- jira_transitions: key — доступные переходы и их обязательные поля.
- jira_changelog: key, field (напр. status). jira_status_times: key — время в каждом статусе.
- jira_worklog: key — трудозатраты с итогом по людям. jira_watchers: key. jira_link_types.
- jira_users: text, project (тогда — кому можно назначить). jira_filters: избранные фильтры.
- jira_dashboards: text (поиск по названию; без него избранные). jira_dashboard: id: гаджеты и их настройки (rich-фильтр, умный фильтр по имени).
- jira_rich_filter: id: rich-фильтр (Rich Filters): базовый фильтр, быстрые, динамические и умные фильтры с JQL каждого условия, представления; text: поиск по названию.
- jira_jql_time: jql | jqls [] | rich_filter (+ smart_filter): время и число задач по каждому запросу или условию, по одному; бюджет 40 с, продолжение start_at.
- jira_download: key, name или attachment_id или all=true — вложения в папку загрузок.
- jira_dev_info: key — коммиты, ветки, PR (если есть интеграция).
- jira_boards: text, project, type (scrum|kanban). jira_sprints: board_id, state (active,future,closed).
- jira_sprint_issues: sprint_id, jql. jira_board_issues: board_id, jql (обе: limit, start_at, fields).
- jsm_desks. jsm_queues: desk_id. jsm_queue_issues: desk_id, queue_id. jsm_sla: key.`

const writeDescription = `Запись в Confluence и Jira. Параметр action.
CONFLUENCE:
- conf_create: space и/или parent_id, title, body (storage XHTML), labels [].
- conf_update: id, version (прочитанный через conf_page — обязателен), body (полное новое тело), title, message, minor_edit, allow_shrink.
- conf_label: id, labels [].
- conf_attach: id, ровно одно из path | content | content_base64; name; message. То же имя = новая версия. Возвращает фрагмент storage для вставки.
- conf_comment: id, body (storage), parent_comment_id. conf_move: id, parent_id (то же пространство), version.
JIRA (тексты — вики-разметка Jira):
- jira_create: project, issuetype, summary, description, priority, assignee, labels [], components [], fix_versions [], due_date, parent (для подзадачи), fields {кастомные поля как есть}.
- jira_update: key + те же поля и/или fields {}, update {} (операции Jira, напр. {"labels":[{"add":"x"}]}).
- jira_bulk_update: keys [] ИЛИ jql (до 50 задач) + те же поля, что у jira_update; dry_run=true — показать «было → стало» без записи. Задачи, где уже так, пропускаются; ошибка по одной не останавливает остальные.
- jira_transition: key, transition (id, название или целевой статус), comment, resolution, fields {}.
- jira_bulk_transition: keys [] ИЛИ jql (до 50 задач), transition, comment, resolution, fields {}; dry_run=true — у каких задач переход доступен. Уже в целевом статусе — пропуск.
- jira_assign: key, user (логин или none). jira_comment: key, body, comment_id (правка своего).
- jira_worklog_add: key, time_spent ("1h 30m"), started ("2026-10-07 10:00"), comment, adjust_estimate (auto — остаток уменьшит Jira, по умолчанию | leave — не трогать | new + new_estimate).
- jira_worklog_update: key, worklog_id (из jira_worklog), time_spent, started, comment, adjust_estimate/new_estimate — только свои записи.
- jira_link: inward_key, outward_key, link_type, comment. jira_link_remove: link_id (из jira_issue).
- jira_remote_link: key, url, title — напр. ссылка на спеку в Confluence.
- jira_attach: key, path | content | content_base64, name.
- jira_watcher_add / jira_watcher_remove: key, user.
- jira_sprint_add: sprint_id, keys []. jira_backlog: keys []. jira_sprint_create: board_id, name, start_date, end_date, goal.
- jira_version_create: project, name, description, start_date, release_date.`

type action func(args) (string, error)

func (t *toolset) readActions() ([]string, map[string]action) {
	m := map[string]action{
		"conf_search": t.confSearch, "conf_page": t.confPage, "conf_children": t.confChildren,
		"conf_spaces": t.confSpaces, "conf_attachments": t.confAttachments, "conf_comments": t.confComments,
		"conf_download": t.confDownload,
		"jira_issue":    t.jiraIssue, "jira_search": t.jiraSearch, "jira_projects": t.jiraProjects,
		"jira_project": t.jiraProject, "jira_fields": t.jiraFields, "jira_create_meta": t.jiraCreateMeta,
		"jira_transitions": t.jiraTransitions, "jira_changelog": t.jiraChangelog, "jira_status_times": t.jiraStatusTimes,
		"jira_worklog": t.jiraWorklog, "jira_watchers": t.jiraWatchers, "jira_link_types": t.jiraLinkTypes,
		"jira_users": t.jiraUsers, "jira_filters": t.jiraFilters, "jira_download": t.jiraDownload,
		"jira_dashboards": t.jiraDashboards, "jira_dashboard": t.jiraDashboard, "jira_rich_filter": t.jiraRichFilter, "jira_jql_time": t.jiraJQLTime,
		"jira_dev_info": t.jiraDevInfo, "jira_boards": t.jiraBoards, "jira_sprints": t.jiraSprints,
		"jira_sprint_issues": t.jiraSprintIssues, "jira_board_issues": t.jiraBoardIssues,
		"jsm_desks": t.jsmDesks, "jsm_queues": t.jsmQueues, "jsm_queue_issues": t.jsmQueueIssues, "jsm_sla": t.jsmSLA,
	}
	order := []string{"conf_search", "conf_page", "conf_children", "conf_spaces", "conf_attachments", "conf_comments", "conf_download",
		"jira_issue", "jira_search", "jira_projects", "jira_project", "jira_fields", "jira_create_meta", "jira_transitions",
		"jira_changelog", "jira_status_times", "jira_worklog", "jira_watchers", "jira_link_types", "jira_users", "jira_filters",
		"jira_dashboards", "jira_dashboard", "jira_rich_filter", "jira_jql_time",
		"jira_download", "jira_dev_info", "jira_boards", "jira_sprints", "jira_sprint_issues", "jira_board_issues",
		"jsm_desks", "jsm_queues", "jsm_queue_issues", "jsm_sla"}
	return order, m
}

func (t *toolset) writeActions() ([]string, map[string]action) {
	m := map[string]action{
		"conf_create": t.confCreate, "conf_update": t.confUpdate, "conf_label": t.confLabel,
		"conf_attach": t.confAttach, "conf_comment": t.confComment, "conf_move": t.confMove,
		"jira_create": t.jiraCreate, "jira_update": t.jiraUpdate, "jira_transition": t.jiraTransition,
		"jira_bulk_update": t.jiraBulkUpdate, "jira_bulk_transition": t.jiraBulkTransition,
		"jira_assign": t.jiraAssign, "jira_comment": t.jiraComment, "jira_worklog_add": t.jiraWorklogAdd, "jira_worklog_update": t.jiraWorklogUpdate,
		"jira_link": t.jiraLink, "jira_link_remove": t.jiraLinkRemove, "jira_remote_link": t.jiraRemoteLink,
		"jira_attach":         t.jiraAttach,
		"jira_watcher_add":    func(a args) (string, error) { return t.jiraWatcher(a, true) },
		"jira_watcher_remove": func(a args) (string, error) { return t.jiraWatcher(a, false) },
		"jira_sprint_add":     t.jiraSprintAdd, "jira_backlog": t.jiraBacklog, "jira_sprint_create": t.jiraSprintCreate,
		"jira_version_create": t.jiraVersionCreate,
	}
	order := []string{"conf_create", "conf_update", "conf_label", "conf_attach", "conf_comment", "conf_move",
		"jira_create", "jira_update", "jira_bulk_update", "jira_transition", "jira_bulk_transition", "jira_assign", "jira_comment", "jira_worklog_add", "jira_worklog_update",
		"jira_link", "jira_link_remove", "jira_remote_link", "jira_attach", "jira_watcher_add", "jira_watcher_remove",
		"jira_sprint_add", "jira_backlog", "jira_sprint_create", "jira_version_create"}
	return order, m
}

func props(names ...string) map[string]any {
	p := map[string]any{}
	for _, n := range names {
		switch n {
		case "fields", "labels", "keys", "components", "fix_versions", "jqls":
			p[n] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		case "comments", "minor_edit", "allow_shrink", "all", "dry_run":
			p[n] = map[string]any{"type": "boolean"}
		case "limit", "version", "start_at", "offset":
			p[n] = map[string]any{"type": "integer"}
		default:
			p[n] = map[string]any{"type": "string"}
		}
	}
	return p
}

func (t *toolset) list() []map[string]any {
	rOrder, _ := t.readActions()
	rp := props("id", "space", "title", "text", "cql", "format", "name", "key", "jql", "fields", "comments", "limit",
		"start_at", "project", "issuetype", "field", "attachment_id", "all", "board_id", "sprint_id", "state", "type",
		"desk_id", "queue_id", "offset", "find", "rich_filter", "smart_filter", "jqls")
	rp["action"] = map[string]any{"type": "string", "enum": rOrder}
	rp["format"] = map[string]any{"type": "string", "enum": []string{"text", "storage"}}
	read := map[string]any{
		"name": "atl", "description": readDescription,
		"inputSchema": map[string]any{"type": "object", "properties": rp, "required": []string{"action"}, "additionalProperties": false},
		"annotations": map[string]any{"readOnlyHint": true, "title": "Confluence/Jira: чтение"},
	}
	tools := []map[string]any{read}
	if t.cfg.AllowWrite {
		wOrder, _ := t.writeActions()
		wp := props("id", "space", "parent_id", "title", "body", "version", "message", "minor_edit", "allow_shrink", "labels",
			"key", "path", "name", "content", "content_base64", "parent_comment_id",
			"project", "issuetype", "summary", "description", "priority", "assignee", "components", "fix_versions", "due_date",
			"parent", "transition", "comment", "resolution", "user", "comment_id", "time_spent", "started",
			"inward_key", "outward_key", "link_type", "link_id", "url", "keys", "sprint_id", "board_id",
			"start_date", "end_date", "goal", "release_date", "jql", "dry_run", "worklog_id", "adjust_estimate", "new_estimate")
		wp["action"] = map[string]any{"type": "string", "enum": wOrder}
		wp["adjust_estimate"] = map[string]any{"type": "string", "enum": []string{"auto", "leave", "new"}}
		wp["fields"] = map[string]any{"type": "object", "description": "Поля Jira как есть, напр. {\"customfield_10100\": \"x\"}"}
		wp["update"] = map[string]any{"type": "object", "description": "Операции update Jira"}
		tools = append(tools, map[string]any{
			"name": "atl_write", "description": writeDescription,
			"inputSchema": map[string]any{"type": "object", "properties": wp, "required": []string{"action"}, "additionalProperties": false},
			"annotations": map[string]any{"readOnlyHint": false, "destructiveHint": false, "title": "Confluence/Jira: запись"},
		})
	}
	return tools
}

type args map[string]any

func (a args) str(k string) string {
	switch v := a[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

func (a args) raw(k string) string {
	if v, ok := a[k].(string); ok {
		return v
	}
	return ""
}

func (a args) boolean(k string) bool {
	switch v := a[k].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	}
	return false
}

func (a args) num(k string) (int, bool) {
	switch v := a[k].(type) {
	case float64:
		return int(v), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil
	}
	return 0, false
}

func (a args) limit(def, max int) int {
	n, ok := a.num("limit")
	if !ok || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (a args) list(k string) []string {
	out := []string{}
	switch v := a[k].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func (t *toolset) call(name string, raw map[string]any) (string, bool) {
	a := args(raw)
	if a == nil {
		a = args{}
	}
	act := a.str("action")
	var m map[string]action
	switch name {
	case "atl":
		_, m = t.readActions()
	case "atl_write":
		if !t.cfg.AllowWrite {
			return errWriteDisabled.Error(), true
		}
		_, m = t.writeActions()
	default:
		return fmt.Sprintf("неизвестный инструмент %q", name), true
	}
	fn, ok := m[act]
	if !ok {
		return fmt.Sprintf("неизвестное действие %q для %s", act, name), true
	}
	out, err := fn(a)
	if err != nil {
		return err.Error(), true
	}
	return out, false
}

// spaceWritable — белый список пространств для записи (ATL_WRITE_SPACES).
func (t *toolset) spaceWritable(space string) error {
	if len(t.cfg.WriteSpaces) == 0 || t.cfg.WriteSpaces[strings.ToUpper(space)] {
		return nil
	}
	keys := []string{}
	for k := range t.cfg.WriteSpaces {
		keys = append(keys, k)
	}
	return fmt.Errorf("запись в пространство %s запрещена настройкой ATL_WRITE_SPACES (разрешены: %s)", space, strings.Join(keys, ", "))
}

const maxOut = 60000

func truncate(s, hint string) string {
	r := []rune(s)
	if len(r) <= maxOut {
		return s
	}
	return string(r[:maxOut]) + fmt.Sprintf("\n\n[ОБРЕЗАНО: показано %d из %d символов. %s]", maxOut, len(r), hint)
}
