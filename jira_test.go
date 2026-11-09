package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type route func(r *http.Request, body string) (int, string)

type jfake struct {
	mu     sync.Mutex
	reqs   []seen
	srv    *httptest.Server
	routes map[string]route
}

func jsonR(s string) route { return func(*http.Request, string) (int, string) { return 200, s } }

func newJFake(t *testing.T) *jfake {
	f := &jfake{routes: map[string]route{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, seen{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Authorization")})
		h, ok := f.routes[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(404)
			w.Write([]byte(`{"errorMessages":["Not found: ` + r.URL.Path + `"]}`))
			return
		}
		code, out := h(r, string(b))
		if strings.HasPrefix(out, "RAW:") {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(code)
			w.Write([]byte(out[4:]))
			return
		}
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		w.WriteHeader(code)
		w.Write([]byte(out))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *jfake) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen{}, f.reqs...)
}
func (f *jfake) reset() { f.mu.Lock(); f.reqs = nil; f.mu.Unlock() }
func (f *jfake) nonGET() []seen {
	out := []seen{}
	for _, r := range f.requests() {
		if r.Method != "GET" {
			out = append(out, r)
		}
	}
	return out
}

func jts(f *jfake, write bool, projects ...string) *toolset {
	cfg := config{ConfURL: f.srv.URL, ConfToken: "good", JiraURL: f.srv.URL, JiraToken: "good", AllowWrite: write,
		WriteSpaces: map[string]bool{}, JiraWrite: map[string]bool{}}
	for _, p := range projects {
		cfg.JiraWrite[p] = true
	}
	return newToolset(cfg)
}

const issueJSON = `{"id":"10001","key":"ABC-1","fields":{"summary":"Отчёт","status":{"name":"Done"},"created":"2026-10-01T09:00:00.000+0300","resolutiondate":"2026-10-03T09:00:00.000+0300",
"subtasks":[{"key":"ABC-2","fields":{"summary":"Под","status":{"name":"Open"}}}],
"issuelinks":[{"id":"777","type":{"name":"Blocks","inward":"is blocked by","outward":"blocks"},"outwardIssue":{"key":"XYZ-9","fields":{"summary":"Другая","status":{"name":"Open"}}}}],
"attachment":[{"id":"501","filename":"tz.pdf","size":8,"author":{"displayName":"Аня"},"created":"2026-10-01T10:00:00.000+0300","content":"SELF/secure/attachment/501/tz.pdf"},
{"id":"502","filename":"evil.png","size":3,"content":"https://evil.example.com/x.png"}],
"timetracking":{"originalEstimate":"2d","timeSpent":"1d","remainingEstimate":"1d"}},
"changelog":{"total":2,"maxResults":2,"histories":[
{"created":"2026-10-02T09:00:00.000+0300","author":{"displayName":"Иван"},"items":[{"field":"status","fromString":"Open","toString":"In Progress"},{"field":"assignee","fromString":"","toString":"Иван"}]},
{"created":"2026-10-01T21:00:00.000+0300","author":{"displayName":"Аня"},"items":[{"field":"priority","fromString":"Low","toString":"High"}]},
{"created":"2026-10-02T21:00:00.000+0300","author":{"displayName":"Иван"},"items":[{"field":"status","fromString":"In Progress","toString":"Done"}]}]}}`

func readyJira(f *jfake) {
	issue := strings.ReplaceAll(issueJSON, "SELF", f.srv.URL)
	f.routes["GET /rest/api/2/issue/ABC-1"] = jsonR(issue)
	f.routes["GET /secure/attachment/501/tz.pdf"] = jsonR("RAW:%PDF-1.4")
	f.routes["GET /rest/api/2/issue/ABC-1/comment"] = jsonR(`{"total":1,"comments":[{"id":"9","author":{"displayName":"Аня"},"created":"2026-10-01T10:00:00.000+0300","body":"ок"}]}`)
	f.routes["GET /rest/api/2/search"] = jsonR(`{"total":3,"issues":[{"key":"ABC-1","fields":{"summary":"Отчёт","status":{"name":"Done"}}}]}`)
	f.routes["GET /rest/api/2/project"] = jsonR(`[{"key":"ABC","name":"Альфа","lead":{"displayName":"Георгий"}},{"key":"XYZ","name":"Икс"}]`)
	f.routes["GET /rest/api/2/project/ABC"] = jsonR(`{"key":"ABC","name":"Альфа","issueTypes":[{"id":"3","name":"Задача"},{"id":"5","name":"Подзадача","subtask":true}],"components":[{"id":"1","name":"FI"}],"versions":[{"id":"7","name":"1.0","released":true}]}`)
	f.routes["GET /rest/api/2/field"] = jsonR(`[{"id":"customfield_10100","name":"Epic Link","custom":true,"schema":{"type":"any","custom":"com.pyxis.greenhopper.jira:gh-epic-link"}},{"id":"summary","name":"Summary","schema":{"type":"string"}}]`)
	f.routes["GET /rest/api/2/issue/createmeta/ABC/issuetypes"] = jsonR(`{"values":[{"id":"3","name":"Задача"}]}`)
	f.routes["GET /rest/api/2/issue/createmeta/ABC/issuetypes/3"] = jsonR(`{"values":[{"fieldId":"priority","name":"Приоритет","required":false,"schema":{"type":"priority"},"allowedValues":[{"id":"1","name":"High"}]},{"fieldId":"summary","name":"Тема","required":true,"schema":{"type":"string"}}]}`)
	f.routes["GET /rest/api/2/issue/ABC-1/transitions"] = jsonR(`{"transitions":[{"id":"11","name":"Взять в работу","to":{"name":"In Progress"}},{"id":"31","name":"Закрыть","to":{"name":"Done"},"fields":{"resolution":{"required":true,"name":"Решение"}}}]}`)
	f.routes["GET /rest/api/2/issue/ABC-1/worklog"] = jsonR(`{"total":2,"worklogs":[{"id":"1","author":{"displayName":"Иван"},"started":"2026-10-02T10:00:00.000+0300","timeSpent":"2h","timeSpentSeconds":7200,"comment":"код"},{"id":"2","author":{"displayName":"Аня"},"started":"2026-10-02T12:00:00.000+0300","timeSpent":"30m","timeSpentSeconds":1800}]}`)
	f.routes["GET /rest/api/2/issue/ABC-1/watchers"] = jsonR(`{"watchCount":1,"watchers":[{"name":"ivan","displayName":"Иван"}]}`)
	f.routes["GET /rest/api/2/issueLinkType"] = jsonR(`{"issueLinkTypes":[{"name":"Blocks","inward":"is blocked by","outward":"blocks"}]}`)
	f.routes["GET /rest/api/2/user/search"] = jsonR(`[{"name":"ivan","displayName":"Иван","emailAddress":"i@x","active":true}]`)
	f.routes["GET /rest/api/2/user/assignable/search"] = jsonR(`[{"name":"anya","displayName":"Аня","active":true}]`)
	f.routes["GET /rest/api/2/filter/favourite"] = jsonR(`[{"id":"100","name":"Мои","jql":"assignee = currentUser()","owner":{"displayName":"Георгий"}}]`)
	f.routes["GET /rest/dev-status/1.0/issue/summary"] = jsonR(`{"summary":{"repository":{"overall":{"count":1},"byInstanceType":{"stash":{"count":1}}},"pullrequest":{"overall":{"count":1},"byInstanceType":{"stash":{"count":1}}}}}`)
	f.routes["GET /rest/dev-status/1.0/issue/detail"] = func(r *http.Request, _ string) (int, string) {
		if r.URL.Query().Get("dataType") == "pullrequest" {
			return 200, `{"detail":[{"pullRequests":[{"id":"#5","status":"MERGED","name":"ABC-1 отчёт","author":{"name":"Иван"},"url":"u"}]}]}`
		}
		return 200, `{"detail":[{"repositories":[{"name":"sap-abap","url":"r","commits":[{"displayId":"a1b2c3","message":"ABC-1 первая версия\nдетали","author":{"name":"Иван"},"authorTimestamp":"2026-10-02T11:00:00.000+0300"}]}]}]}`
	}
	f.routes["GET /rest/agile/1.0/board"] = jsonR(`{"isLast":true,"values":[{"id":12,"name":"ABC доска","type":"scrum"}]}`)
	f.routes["GET /rest/agile/1.0/board/12/sprint"] = jsonR(`{"isLast":true,"values":[{"id":40,"name":"Спринт 7","state":"active","startDate":"2026-10-01T09:00:00.000+03:00","endDate":"2026-10-14T18:00:00.000+03:00"}]}`)
	f.routes["GET /rest/agile/1.0/sprint/40/issue"] = jsonR(`{"total":1,"issues":[{"key":"ABC-1","fields":{"summary":"Отчёт"}}]}`)
	f.routes["GET /rest/agile/1.0/board/12/issue"] = jsonR(`{"total":1,"issues":[{"key":"ABC-1","fields":{"summary":"Отчёт"}}]}`)
	f.routes["GET /rest/servicedeskapi/servicedesk"] = jsonR(`{"values":[{"id":"2","projectKey":"SD","projectName":"Поддержка"}]}`)
	f.routes["GET /rest/servicedeskapi/servicedesk/2/queue"] = jsonR(`{"values":[{"id":"8","name":"Новые","issueCount":4,"jql":"status = Open"}]}`)
	f.routes["GET /rest/servicedeskapi/servicedesk/2/queue/8/issue"] = jsonR(`{"isLastPage":true,"values":[{"key":"SD-3","fields":{"summary":"Не работает"}}]}`)
	f.routes["GET /rest/servicedeskapi/request/SD-3/sla"] = jsonR(`{"values":[{"name":"Время до решения","ongoingCycle":{"breached":false,"paused":false,"goalDuration":{"friendly":"8h"},"remainingTime":{"friendly":"3h"}}}]}`)
}

func TestJiraReadAll(t *testing.T) {
	f := newJFake(t)
	readyJira(f)
	dir := t.TempDir()
	ts := jts(f, true)
	ts.cfg.DownloadDir = dir
	cases := []struct {
		a    map[string]any
		want string
	}{
		{map[string]any{"action": "jira_issue", "key": "ABC-1", "comments": true}, "[777] blocks XYZ-9"},
		{map[string]any{"action": "jira_search", "jql": "project=ABC", "limit": 1}, "start_at=1"},
		{map[string]any{"action": "jira_projects", "text": "альф"}, "Проектов: 1"},
		{map[string]any{"action": "jira_project", "project": "abc"}, "5 | Подзадача (подзадача)"},
		{map[string]any{"action": "jira_fields", "text": "epic"}, "customfield_10100 | Epic Link | any gh-epic-link"},
		{map[string]any{"action": "jira_create_meta", "project": "ABC", "issuetype": "задача"}, "summary | Тема | string ОБЯЗАТЕЛЬНОЕ"},
		{map[string]any{"action": "jira_transitions", "key": "ABC-1"}, "обязательные поля: resolution (Решение)"},
		{map[string]any{"action": "jira_changelog", "key": "ABC-1", "field": "status"}, `"In Progress" → "Done"`},
		{map[string]any{"action": "jira_status_times", "key": "ABC-1"}, "Open | 24.0 ч"},
		{map[string]any{"action": "jira_worklog", "key": "ABC-1"}, "всего 2.50 ч"},
		{map[string]any{"action": "jira_watchers", "key": "ABC-1"}, "ivan | Иван"},
		{map[string]any{"action": "jira_link_types"}, "Blocks | blocks | is blocked by"},
		{map[string]any{"action": "jira_users", "text": "iv"}, "ivan | Иван"},
		{map[string]any{"action": "jira_users", "text": "an", "project": "ABC"}, "anya"},
		{map[string]any{"action": "jira_filters"}, "assignee = currentUser()"},
		{map[string]any{"action": "jira_dev_info", "key": "ABC-1"}, "PR #5 | MERGED"},
		{map[string]any{"action": "jira_boards", "project": "ABC"}, "12 | ABC доска | scrum"},
		{map[string]any{"action": "jira_sprints", "board_id": "12", "state": "active"}, "40 | Спринт 7 | active"},
		{map[string]any{"action": "jira_sprint_issues", "sprint_id": "40"}, "ABC-1"},
		{map[string]any{"action": "jira_board_issues", "board_id": "12", "jql": "status=Open"}, "ABC-1"},
		{map[string]any{"action": "jsm_desks"}, "2 | SD | Поддержка"},
		{map[string]any{"action": "jsm_queues", "desk_id": "2"}, "8 | Новые | 4"},
		{map[string]any{"action": "jsm_queue_issues", "desk_id": "2", "queue_id": "8"}, "SD-3"},
		{map[string]any{"action": "jsm_sla", "key": "SD-3"}, "осталось 3h"},
		{map[string]any{"action": "jira_download", "key": "ABC-1", "name": "tz.pdf"}, "tz.pdf (8 байт)"},
	}
	for _, c := range cases {
		out, isErr := call(t, ts, "atl", c.a)
		if isErr || !strings.Contains(out, c.want) {
			t.Fatalf("%v: ждали %q, получили (err=%v):\n%s", c.a["action"], c.want, isErr, out)
		}
	}
	if n := f.nonGET(); len(n) != 0 {
		t.Fatalf("чтение отправило не-GET: %+v", n)
	}
}

func TestStatusDurations(t *testing.T) {
	var is jiraChangelogIssue
	json.Unmarshal([]byte(issueJSON), &is)
	created, _ := parseJiraTime("2026-10-01T09:00:00.000+0300")
	end, _ := parseJiraTime("2026-10-03T09:00:00.000+0300")
	spans, lines := statusDurations(created, "Done", is.Log.Histories, end)
	// истории в ответе не по порядку — getChangelog сортирует; здесь проверяем саму функцию на отсортированных
	if len(lines) != 2 {
		t.Fatalf("переходов: %v", lines)
	}
	got := map[string]time.Duration{}
	for _, s := range spans {
		got[s.Status] = s.Total
	}
	if got["Open"] != 24*time.Hour || got["In Progress"] != 12*time.Hour || got["Done"] != 12*time.Hour {
		t.Fatalf("время в статусах: %v", got)
	}
	// без переходов — всё время в текущем статусе
	spans, _ = statusDurations(created, "Open", nil, end)
	if len(spans) != 1 || spans[0].Total != 48*time.Hour {
		t.Fatalf("без переходов: %+v", spans)
	}
}

func TestJiraDownloadGuards(t *testing.T) {
	f := newJFake(t)
	readyJira(f)
	dir := t.TempDir()
	ts := jts(f, false)
	ts.cfg.DownloadDir = dir
	for i := 0; i < 2; i++ {
		if out, isErr := call(t, ts, "atl", map[string]any{"action": "jira_download", "key": "ABC-1", "attachment_id": "501"}); isErr {
			t.Fatal(out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "tz (1).pdf")); err != nil {
		t.Fatalf("второе скачивание должно лечь рядом, не перезаписав: %v", err)
	}
	f.reset()
	out, _ := call(t, ts, "atl", map[string]any{"action": "jira_download", "key": "ABC-1", "name": "evil.png"})
	if !strings.Contains(out, "чужого адреса") {
		t.Fatalf("ссылка на чужой хост: %s", out)
	}
	for _, r := range f.requests() {
		if strings.Contains(r.Path, "x.png") {
			t.Fatal("ушёл запрос за вложением с чужого хоста")
		}
	}
	if out, isErr := call(t, ts, "atl", map[string]any{"action": "jira_download", "key": "ABC-1"}); !isErr || !strings.Contains(out, "all=true") {
		t.Fatalf("без name/all: %s", out)
	}
}

func TestCreateMetaLegacy(t *testing.T) {
	f := newJFake(t)
	f.routes["GET /rest/api/2/issue/createmeta"] = jsonR(`{"projects":[{"issuetypes":[{"id":"3","name":"Задача","fields":{"summary":{"name":"Тема","required":true,"schema":{"type":"string"}},"priority":{"name":"Приоритет","schema":{"type":"priority"}}}}]}]}`)
	out, isErr := call(t, jts(f, false), "atl", map[string]any{"action": "jira_create_meta", "project": "ABC", "issuetype": "Задача"})
	if isErr || !strings.Contains(out, "summary | Тема | string ОБЯЗАТЕЛЬНОЕ") {
		t.Fatalf("legacy createmeta: %s", out)
	}
}

func TestJiraWrites(t *testing.T) {
	f := newJFake(t)
	readyJira(f)
	f.routes["POST /rest/api/2/issue"] = jsonR(`{"id":"10050","key":"ABC-50"}`)
	f.routes["PUT /rest/api/2/issue/ABC-1"] = func(*http.Request, string) (int, string) { return 204, "" }
	f.routes["POST /rest/api/2/issue/ABC-1/transitions"] = func(*http.Request, string) (int, string) { return 204, "" }
	f.routes["PUT /rest/api/2/issue/ABC-1/assignee"] = func(*http.Request, string) (int, string) { return 204, "" }
	f.routes["POST /rest/api/2/issue/ABC-1/comment"] = jsonR(`{"id":"77"}`)
	f.routes["PUT /rest/api/2/issue/ABC-1/comment/77"] = jsonR(`{"id":"77"}`)
	f.routes["POST /rest/api/2/issue/ABC-1/worklog"] = jsonR(`{"id":"3"}`)
	f.routes["POST /rest/api/2/issueLink"] = func(*http.Request, string) (int, string) { return 201, "" }
	f.routes["GET /rest/api/2/issueLink/777"] = jsonR(`{"type":{"name":"Blocks"},"inwardIssue":{"key":"ABC-1"},"outwardIssue":{"key":"ABC-2"}}`)
	f.routes["DELETE /rest/api/2/issueLink/777"] = func(*http.Request, string) (int, string) { return 204, "" }
	f.routes["POST /rest/api/2/issue/ABC-1/remotelink"] = jsonR(`{"id":1}`)
	f.routes["POST /rest/api/2/issue/ABC-1/attachments"] = jsonR(`[{"id":"9"}]`)
	f.routes["POST /rest/api/2/issue/ABC-1/watchers"] = func(*http.Request, string) (int, string) { return 204, "" }
	f.routes["DELETE /rest/api/2/issue/ABC-1/watchers"] = func(*http.Request, string) (int, string) { return 204, "" }
	f.routes["POST /rest/agile/1.0/sprint/40/issue"] = func(*http.Request, string) (int, string) { return 204, "" }
	f.routes["POST /rest/agile/1.0/backlog/issue"] = func(*http.Request, string) (int, string) { return 204, "" }
	f.routes["POST /rest/agile/1.0/sprint"] = jsonR(`{"id":41}`)
	f.routes["POST /rest/api/2/version"] = jsonR(`{"id":"8"}`)
	ts := jts(f, true)

	type tc struct {
		a               map[string]any
		method, path    string
		bodyHas, outHas []string
	}
	cases := []tc{
		{map[string]any{"action": "jira_create", "project": "abc", "issuetype": "Задача", "summary": "Новая", "description": "h2. ТЗ", "assignee": "ivan", "labels": []string{"spec"}, "parent": "abc-1", "fields": map[string]any{"customfield_10100": "ABC-7"}},
			"POST", "/rest/api/2/issue", []string{`"project":{"key":"ABC"}`, `"issuetype":{"name":"Задача"}`, `"customfield_10100":"ABC-7"`, `"parent":{"key":"ABC-1"}`, `"assignee":{"name":"ivan"}`, `"labels":["spec"]`}, []string{"ABC-50"}},
		{map[string]any{"action": "jira_update", "key": "ABC-1", "summary": "Иначе", "update": map[string]any{"labels": []any{map[string]any{"add": "x"}}}},
			"PUT", "/rest/api/2/issue/ABC-1", []string{`"fields":{"summary":"Иначе"}`, `"update":{"labels":[{"add":"x"}]}`}, nil},
		{map[string]any{"action": "jira_transition", "key": "ABC-1", "transition": "done", "resolution": "Fixed", "comment": "Готово"},
			"POST", "/rest/api/2/issue/ABC-1/transitions", []string{`"transition":{"id":"31"}`, `"resolution":{"name":"Fixed"}`, `"body":"Готово"`}, []string{"«Закрыть» → Done"}},
		{map[string]any{"action": "jira_assign", "key": "ABC-1", "user": "none"},
			"PUT", "/rest/api/2/issue/ABC-1/assignee", []string{`{"name":null}`}, []string{"снят"}},
		{map[string]any{"action": "jira_comment", "key": "ABC-1", "body": "ок", "comment_id": "77"},
			"PUT", "/rest/api/2/issue/ABC-1/comment/77", []string{`"body":"ок"`}, []string{"изменён"}},
		{map[string]any{"action": "jira_worklog_add", "key": "ABC-1", "time_spent": "1h 30m", "started": "2026-10-07 10:00", "comment": "анализ"},
			"POST", "/rest/api/2/issue/ABC-1/worklog", []string{`"timeSpent":"1h 30m"`, `"started":"2026-10-07T10:00:00.000`}, nil},
		{map[string]any{"action": "jira_link", "inward_key": "ABC-1", "outward_key": "ABC-2", "link_type": "Blocks"},
			"POST", "/rest/api/2/issueLink", []string{`"inwardIssue":{"key":"ABC-1"}`, `"type":{"name":"Blocks"}`}, nil},
		{map[string]any{"action": "jira_link_remove", "link_id": "777"}, "DELETE", "/rest/api/2/issueLink/777", nil, []string{"ABC-2 — ABC-1"}},
		{map[string]any{"action": "jira_remote_link", "key": "ABC-1", "url": "https://wiki.example.com/pages/viewpage.action?pageId=1", "title": "Спека"},
			"POST", "/rest/api/2/issue/ABC-1/remotelink", []string{`"title":"Спека"`}, nil},
		{map[string]any{"action": "jira_attach", "key": "ABC-1", "name": "лог.txt", "content": "строка"},
			"POST", "/rest/api/2/issue/ABC-1/attachments", []string{"строка", `filename="лог.txt"`}, nil},
		{map[string]any{"action": "jira_watcher_add", "key": "ABC-1", "user": "ivan"}, "POST", "/rest/api/2/issue/ABC-1/watchers", []string{`"ivan"`}, nil},
		{map[string]any{"action": "jira_sprint_add", "sprint_id": "40", "keys": []string{"abc-1", "abc-2"}}, "POST", "/rest/agile/1.0/sprint/40/issue", []string{`["ABC-1","ABC-2"]`}, nil},
		{map[string]any{"action": "jira_backlog", "keys": []string{"ABC-1"}}, "POST", "/rest/agile/1.0/backlog/issue", []string{`["ABC-1"]`}, nil},
		{map[string]any{"action": "jira_sprint_create", "board_id": "12", "name": "Спринт 8", "start_date": "2026-10-15", "goal": "релиз"}, "POST", "/rest/agile/1.0/sprint", []string{`"originBoardId":"12"`, `"startDate":"2026-10-15T09:00:00.000`}, nil},
		{map[string]any{"action": "jira_version_create", "project": "ABC", "name": "2.0", "release_date": "2026-12-01"}, "POST", "/rest/api/2/version", []string{`"releaseDate":"2026-12-01"`}, nil},
	}
	for _, c := range cases {
		f.reset()
		out, isErr := call(t, ts, "atl_write", c.a)
		if isErr {
			t.Fatalf("%v: %s", c.a["action"], out)
		}
		w := f.nonGET()
		if len(w) != 1 || w[0].Method != c.method || w[0].Path != c.path {
			t.Fatalf("%v: ждали %s %s, ушло %+v", c.a["action"], c.method, c.path, w)
		}
		for _, s := range c.bodyHas {
			if !strings.Contains(w[0].Body, s) {
				t.Fatalf("%v: в теле нет %s:\n%s", c.a["action"], s, w[0].Body)
			}
		}
		for _, s := range c.outHas {
			if !strings.Contains(out, s) {
				t.Fatalf("%v: в ответе нет %s:\n%s", c.a["action"], s, out)
			}
		}
	}
	f.reset()
	if out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_watcher_remove", "key": "ABC-1", "user": "ivan"}); isErr {
		t.Fatal(out)
	}
	if w := f.nonGET(); len(w) != 1 || w[0].Method != "DELETE" || w[0].Query != "username=ivan" {
		t.Fatalf("удаление наблюдателя: %+v", w)
	}
}

func TestJiraWriteGuardsBeforeNetwork(t *testing.T) {
	f := newJFake(t)
	readyJira(f)
	ts := jts(f, true, "ABC")
	bad := []map[string]any{
		{"action": "jira_create", "project": "XYZ", "issuetype": "Задача", "summary": "x"}, // не в белом списке
		{"action": "jira_comment", "key": "XYZ-1", "body": "x"},
		{"action": "jira_link", "inward_key": "ABC-1", "outward_key": "XYZ-9", "link_type": "Blocks"},
		{"action": "jira_sprint_add", "sprint_id": "40", "keys": []string{"ABC-1", "XYZ-2"}},
		{"action": "jira_worklog_add", "key": "ABC-1", "time_spent": "полтора часа"},
		{"action": "jira_worklog_add", "key": "ABC-1", "time_spent": "1h", "started": "вчера"},
		{"action": "jira_assign", "key": "ABC-1", "user": "a b"},
		{"action": "jira_remote_link", "key": "ABC-1", "url": "javascript:alert(1)"},
		{"action": "jira_update", "key": "ABC-1"},
		{"action": "jira_create", "project": "ABC", "issuetype": "Задача", "summary": "x", "due_date": "завтра"},
		{"action": "jira_attach", "key": "ABC-1", "name": "x.exe", "content": "MZ"},
		{"action": "jira_version_create", "project": "XYZ", "name": "1"},
	}
	for _, b := range bad {
		if out, isErr := call(t, ts, "atl_write", b); !isErr {
			t.Fatalf("%v прошло: %s", b, out)
		}
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatalf("гарды пропустили запись: %+v", w)
	}
	// переход, которого нет, — отказ со списком доступных и без POST
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_transition", "key": "ABC-1", "transition": "Отменить"})
	if !isErr || !strings.Contains(out, "11 «Взять в работу»") {
		t.Fatalf("неизвестный переход: %s", out)
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatal("неизвестный переход ушёл в сеть")
	}
	// запись выключена: ни одного не-GET ни по одному действию
	ro := jts(f, false)
	_, wm := ro.writeActions()
	for name := range wm {
		if out, isErr := ro.call("atl_write", map[string]any{"action": name}); !isErr || !strings.Contains(out, "запись выключена") {
			t.Fatalf("%s при выключенной записи: %s", name, out)
		}
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatal("при выключенной записи ушла запись")
	}
}

func TestParseStarted(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, loc)
	cases := map[string]string{
		"":                          "2026-10-07T15:00:00.000+0300",
		"2026-10-06":                "2026-10-06T09:00:00.000+0300",
		"2026-10-06 14:30":          "2026-10-06T14:30:00.000+0300",
		"2026-10-06T14:30:00+05:00": "2026-10-06T14:30:00.000+0500",
	}
	for in, want := range cases {
		got, err := parseStarted(in, now)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v, ждали %q", in, got, err, want)
		}
	}
}

func TestDevInfoNoIntegration(t *testing.T) {
	f := newJFake(t)
	f.routes["GET /rest/api/2/issue/ABC-1"] = jsonR(`{"id":"1"}`)
	out, _ := call(t, jts(f, false), "atl", map[string]any{"action": "jira_dev_info", "key": "ABC-1"})
	if !strings.Contains(out, "нет интеграции") {
		t.Fatalf("dev-status 404: %s", out)
	}
}

func TestToolSchemas(t *testing.T) {
	f := newJFake(t)
	ts := jts(f, true)
	tools := ts.list()
	raw, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	ro, rm := ts.readActions()
	wo, wm := ts.writeActions()
	if len(ro) != len(rm) || len(wo) != len(wm) {
		t.Fatalf("порядок и таблица действий разошлись: %d/%d %d/%d", len(ro), len(rm), len(wo), len(wm))
	}
	for _, n := range append(ro, wo...) {
		if !strings.Contains(raw2(raw), `"`+n+`"`) {
			t.Fatalf("действие %s не в схеме", n)
		}
		if !strings.Contains(readDescription+writeDescription, n) {
			t.Fatalf("действие %s не описано", n)
		}
	}
}

func raw2(b []byte) string { return string(b) }
