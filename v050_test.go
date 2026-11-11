package main

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const fakeRF = `{"id":7,"name":"Команда Альфа","jiraFilter":{"id":100,"name":"Все задачи команды","jql":{"input":"assignee in (ivanov.ii)","value":"assignee in (ivanov.ii)"}},
"admins":[{"username":"ivanov.ii"}],
"staticFilters":[{"id":1,"label":{"input":"Текущий год","value":"Текущий год"},"jql":{"input":"resolved >= startOfYear()","value":"resolved >= startOfYear()"}}],
"dynamicFilters":[{"id":2,"key":"assignee","type":"options","displayName":"Исполнитель"}],
"smartFilters":[{"id":455,"name":{"input":"Отклонения","value":"Отклонения"},"clauses":[
  {"id":11,"label":{"input":"Нет оценки","value":"Нет оценки"},"jql":{"input":"timeestimate = 0","value":"timeestimate = 0"}},
  {"id":12,"label":{"input":"Родитель закрыт","value":"Родитель закрыт"},"jql":{"input":"issueFunction in linkedIssuesOf(\"statusCategory = Done\", \"дочерняя задача\")","value":"issueFunction in linkedIssuesOf(\"statusCategory = Done\", \"дочерняя задача\")"}}]}],
"richViews":[{"id":9,"name":{"input":"Разработка","value":"Разработка"},"columns":[
  {"type":{"input":"navigable-field"},"field":{"input":"issuekey"}},
  {"type":{"input":"smart-filter"},"smartFilter":{"input":"455"}}]}],
"richQueues":[],"customValues":[],"customRatios":[],"timeSeries":[]}`

func TestGadgetTitleDecode(t *testing.T) {
	if got := gadgetTitle("-041E-0442-043A-043B-043E-043D-0435-043D-0438-044F_-0440-0430-0437"); got != "Отклонения раз" {
		t.Fatalf("заголовок: %q", got)
	}
	if gadgetTitle(".") != "" || gadgetTitle("Plain") != "Plain" {
		t.Fatal("точка и простой текст")
	}
}

func TestRichFilterCompact(t *testing.T) {
	f := newJFake(t)
	f.routes["GET "+rfBase+"/7"] = jsonR(fakeRF)
	ts := jts(f, false)
	out, isErr := call(t, ts, "atl", map[string]any{"action": "jira_rich_filter", "id": "7"})
	if isErr {
		t.Fatal(out)
	}
	for _, s := range []string{"Rich-фильтр 7: Команда Альфа", "Базовый фильтр: 100 «Все задачи команды»",
		"- [1] Текущий год: resolved >= startOfYear()", "- [455] Отклонения", "  - [12] Родитель закрыт: issueFunction in linkedIssuesOf",
		"- [2] Исполнитель: поле assignee", "- [9] Разработка: issuekey, умный «Отклонения» (455)"} {
		if !strings.Contains(out, s) {
			t.Fatalf("нет %q в\n%s", s, out)
		}
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatalf("ушла запись: %+v", w)
	}
}

func TestRichFilterSearch(t *testing.T) {
	f := newJFake(t)
	f.routes["GET "+rfBase+"/search"] = func(r *http.Request, _ string) (int, string) {
		if r.URL.Query().Get("query") != "Альфа" {
			return 400, `{}`
		}
		return 200, `{"results":[{"id":7,"name":"Команда Альфа","jiraFilter":{"value":{"id":100,"name":"Все задачи команды"}},"smartFilters":[{"id":1}],"richViews":[]}]}`
	}
	ts := jts(f, false)
	out, isErr := call(t, ts, "atl", map[string]any{"action": "jira_rich_filter", "text": "Альфа"})
	if isErr || !strings.Contains(out, "- 7 | Команда Альфа | базовый фильтр 100 «Все задачи команды» | умных фильтров 1") {
		t.Fatal(out)
	}
}

func TestDashboardGadgets(t *testing.T) {
	f := newJFake(t)
	f.routes["GET "+rfBase+"/7"] = jsonR(fakeRF)
	f.routes["GET /rest/dashboards/1.0/500"] = jsonR(`{"id":"500","title":"Разработка","gadgets":[
	 {"id":"11","title":"Rich Filter Smart Counters","column":1,"userPrefs":{"fields":[
	   {"name":"isConfigured","value":"true"},{"name":"rf","value":"7"},{"name":"smartFilter","value":"455"},
	   {"name":"title","value":"-041E-0442-043A-043B-043E-043D-0435-043D-0438-044F"},{"name":"wq","value":"."},{"name":"showNone","value":""}]}},
	 {"id":"12","title":"Rich Filter Two Dimensional Statistics","column":0,"userPrefs":{"fields":[
	   {"name":"rf","value":"7"},{"name":"xStatType","value":"smart:455"},{"name":"yStatType","value":"assignees"}]}}]}`)
	ts := jts(f, false)
	out, isErr := call(t, ts, "atl", map[string]any{"action": "jira_dashboard", "id": "500"})
	if isErr {
		t.Fatal(out)
	}
	for _, s := range []string{"Дашборд 500: Разработка, гаджетов 2", "- [11] «Отклонения» (Rich Filter Smart Counters), колонка 1: rf=7 (Команда Альфа), smartFilter=455 (Отклонения)",
		"- [12] «Rich Filter Two Dimensional Statistics»", "xStatType=smart:455 (Отклонения), yStatType=assignees"} {
		if !strings.Contains(out, s) {
			t.Fatalf("нет %q в\n%s", s, out)
		}
	}
	if strings.Contains(out, "isConfigured") || strings.Contains(out, "wq=") || strings.Contains(out, "showNone") {
		t.Fatalf("лишние настройки:\n%s", out)
	}
	n := 0
	for _, r := range f.requests() {
		if r.Path == rfBase+"/7" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("rich-фильтр запрошен %d раз, ждали 1 (кэш на вызов)", n)
	}
}

func TestJQLTimeRichFilter(t *testing.T) {
	f := newJFake(t)
	f.routes["GET "+rfBase+"/7"] = jsonR(fakeRF)
	f.routes["GET /rest/api/2/search"] = jsonR(`{"total":42}`)
	ts := jts(f, false)
	out, isErr := call(t, ts, "atl", map[string]any{"action": "jira_jql_time", "rich_filter": "7", "smart_filter": "455"})
	if isErr {
		t.Fatal(out)
	}
	jqls := []string{}
	for _, r := range f.requests() {
		if r.Path == "/rest/api/2/search" {
			q, _ := url.ParseQuery(r.Query)
			if q.Get("maxResults") != "0" {
				t.Fatalf("нужен maxResults=0: %s", r.Query)
			}
			jqls = append(jqls, q.Get("jql"))
		}
	}
	if len(jqls) != 2 || jqls[0] != "filter = 100 AND (timeestimate = 0)" || !strings.HasPrefix(jqls[1], "filter = 100 AND (issueFunction in linkedIssuesOf(") {
		t.Fatalf("запросы: %q", jqls)
	}
	for _, s := range []string{"поверх «filter = 100»", "Отклонения [11] «Нет оценки»: ", "задач 42"} {
		if !strings.Contains(out, s) {
			t.Fatalf("нет %q в\n%s", s, out)
		}
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatalf("ушла запись: %+v", w)
	}
}

func TestJQLTimeBudget(t *testing.T) {
	f := newJFake(t)
	f.routes["GET /rest/api/2/search"] = func(*http.Request, string) (int, string) {
		time.Sleep(1200 * time.Millisecond)
		return 200, `{"total":1}`
	}
	ts := jts(f, false)
	ts.bulkBudget = 3500 * time.Millisecond
	out, isErr := call(t, ts, "atl", map[string]any{"action": "jira_jql_time", "jqls": []string{"a = 1", "b = 2", "c = 3", "d = 4"}})
	if isErr || !strings.Contains(out, "НЕ УСПЕЛ") || !strings.Contains(out, "start_at=") {
		t.Fatalf("ждали остановку по бюджету:\n%s", out)
	}
	if strings.Contains(out, "3. запрос 4") {
		t.Fatalf("четвёртый не должен был успеть:\n%s", out)
	}
}

func TestJQLTimeGuards(t *testing.T) {
	f := newJFake(t)
	ts := jts(f, false)
	for _, a := range []map[string]any{
		{"action": "jira_jql_time"},
		{"action": "jira_jql_time", "rich_filter": "x"},
		{"action": "jira_jql_time", "rich_filter": "7", "smart_filter": "a"},
		{"action": "jira_dashboard", "id": "../x"},
		{"action": "jira_rich_filter"},
	} {
		if out, isErr := call(t, ts, "atl", a); !isErr {
			t.Fatalf("ждали ошибку для %v: %s", a, out)
		}
	}
	for _, r := range f.requests() {
		if r.Path != rfBase+"/7" {
			t.Fatalf("гард после сети: %+v", r)
		}
	}
}
