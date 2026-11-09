package main

import (
	"net/http"
	"strings"
	"testing"
)

func worklogFake(t *testing.T) *jfake {
	f := newJFake(t)
	f.routes["GET /rest/api/2/myself"] = jsonR(`{"name":"ivanov.ii","key":"JIRAUSER1"}`)
	f.routes["GET /rest/api/2/issue/ABC-1/worklog/101"] = jsonR(`{"id":"101","started":"2026-11-26T10:00:00.000+0500","timeSpent":"5h","comment":"Работа","author":{"name":"ivanov.ii","displayName":"Иван Иванов"}}`)
	f.routes["GET /rest/api/2/issue/ABC-1/worklog/102"] = jsonR(`{"id":"102","timeSpent":"3h","author":{"name":"petrova.aa","displayName":"Анна Петрова"}}`)
	ok := func(*http.Request, string) (int, string) { return 200, `{"id":"103"}` }
	f.routes["PUT /rest/api/2/issue/ABC-1/worklog/101"] = ok
	f.routes["PUT /rest/api/2/issue/ABC-1/worklog/102"] = ok
	f.routes["POST /rest/api/2/issue/ABC-1/worklog"] = ok
	return f
}

func TestWorklogUpdateOwn(t *testing.T) {
	f := worklogFake(t)
	ts := jts(f, true)
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_worklog_update", "key": "ABC-1", "worklog_id": "101", "time_spent": "2h"})
	if isErr {
		t.Fatal(out)
	}
	w := f.nonGET()
	if len(w) != 1 || w[0].Method != "PUT" || w[0].Path != "/rest/api/2/issue/ABC-1/worklog/101" || !strings.Contains(w[0].Body, `"timeSpent":"2h"`) {
		t.Fatalf("запись: %+v", w)
	}
	if strings.Contains(w[0].Body, "started") || strings.Contains(w[0].Body, "comment") || w[0].Query != "" {
		t.Fatalf("ушло лишнее: %+v", w[0])
	}
	for _, s := range []string{"время 5h → 2h", "остаток пересчитала Jira"} {
		if !strings.Contains(out, s) {
			t.Fatalf("нет %q в %s", s, out)
		}
	}
}

func TestWorklogUpdateForeignRefused(t *testing.T) {
	f := worklogFake(t)
	ts := jts(f, true)
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_worklog_update", "key": "ABC-1", "worklog_id": "102", "time_spent": "1h"})
	if !isErr || !strings.Contains(out, "Анна Петрова") {
		t.Fatalf("чужую запись надо отвергнуть: %s", out)
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatalf("ушла запись: %+v", w)
	}
}

func TestWorklogGuardsBeforeNetwork(t *testing.T) {
	f := worklogFake(t)
	ts := jts(f, true)
	for _, a := range []map[string]any{
		{"action": "jira_worklog_update", "key": "ABC-1", "worklog_id": "x1", "time_spent": "1h"},
		{"action": "jira_worklog_update", "key": "ABC-1", "worklog_id": "101"},
		{"action": "jira_worklog_update", "key": "ABC-1", "worklog_id": "101", "time_spent": "2h", "adjust_estimate": "new"},
		{"action": "jira_worklog_add", "key": "ABC-1", "time_spent": "2h", "adjust_estimate": "leave", "new_estimate": "1h"},
		{"action": "jira_worklog_add", "key": "ABC-1", "time_spent": "2h", "adjust_estimate": "whatever"},
	} {
		if out, isErr := call(t, ts, "atl_write", a); !isErr {
			t.Fatalf("ждали ошибку для %v: %s", a, out)
		}
	}
	if r := f.requests(); len(r) != 0 {
		t.Fatalf("гард после сети: %+v", r)
	}
}

func TestWorklogAdjustModes(t *testing.T) {
	f := worklogFake(t)
	ts := jts(f, true)
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_worklog_add", "key": "ABC-1", "time_spent": "2h", "started": "2026-11-26 10:00", "adjust_estimate": "leave"})
	if isErr || !strings.Contains(out, "остаток не тронут") {
		t.Fatal(out)
	}
	if q := f.nonGET()[0].Query; q != "adjustEstimate=leave" {
		t.Fatalf("query: %s", q)
	}
	f.reset()
	out, isErr = call(t, ts, "atl_write", map[string]any{"action": "jira_worklog_update", "key": "ABC-1", "worklog_id": "101", "time_spent": "2h", "adjust_estimate": "new", "new_estimate": "24h"})
	if isErr || !strings.Contains(out, "остаток = 24h") {
		t.Fatal(out)
	}
	if q := f.nonGET()[0].Query; !strings.Contains(q, "adjustEstimate=new") || !strings.Contains(q, "newEstimate=24h") {
		t.Fatalf("query: %s", q)
	}
}

func TestBulkComponentsSameSkipped(t *testing.T) {
	f := newJFake(t)
	f.routes["GET /rest/api/2/search"] = jsonR(`{"total":1,"issues":[{"key":"ABC-1","fields":{"summary":"Отчёт","status":{"name":"Создана"},"components":[{"self":"x","id":"1","name":"HCM"}]}}]}`)
	ts := jts(f, true)
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_bulk_update", "keys": []string{"ABC-1"}, "components": []string{"HCM"}})
	if isErr || !strings.Contains(out, "уже так, пропущено") {
		t.Fatalf("компоненты те же — пропуск: %s", out)
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatalf("перезаписал то же: %+v", w)
	}
}

func TestIssueLinksCompact(t *testing.T) {
	v := map[string]any{"id": "3259053", "self": "x",
		"type":        map[string]any{"name": "Parent", "inward": "родительская задача", "outward": "дочерняя задача", "self": "y"},
		"inwardIssue": map[string]any{"key": "ABC-7", "self": "z", "fields": map[string]any{"summary": "Бизнес-запрос", "status": map[string]any{"name": "Анализ", "iconUrl": "i"}}}}
	got := jiraVal([]any{v})
	if got != "[3259053] родительская задача ABC-7 | Анализ | Бизнес-запрос" {
		t.Fatalf("связь: %s", got)
	}
}

func TestConfPageOffsetAndFind(t *testing.T) {
	f := newFake(t)
	f.body = "<p>" + strings.Repeat("а", 70000) + `<span style="color: rgb(255,0,0);">ABC-42 правка</span>` + strings.Repeat("б", 70000) + "</p>"
	ts := tsFor(f, false)
	out, isErr := call(t, ts, "atl", map[string]any{"action": "conf_page", "id": "123", "format": "storage"})
	if isErr || !strings.Contains(out, "[фрагмент: символы 0–") || !strings.Contains(out, "offset=") {
		t.Fatalf("первый кусок: %.300s", out)
	}
	if len([]rune(out)) > maxOut {
		t.Fatalf("кусок длиннее предела: %d", len([]rune(out)))
	}
	out, isErr = call(t, ts, "atl", map[string]any{"action": "conf_page", "id": "123", "format": "storage", "offset": 69000})
	if isErr || !strings.Contains(out, "ABC-42 правка") || !strings.Contains(out, "символы 69000–") {
		t.Fatalf("кусок с offset: %.300s", out)
	}
	out, isErr = call(t, ts, "atl", map[string]any{"action": "conf_page", "id": "123", "format": "storage", "find": "RGB(255,0,0)"})
	if isErr || !strings.Contains(out, "совпадений «RGB(255,0,0)»: 1") || !strings.Contains(out, "ABC-42 правка") {
		t.Fatalf("find: %.500s", out)
	}
	if len([]rune(out)) > 5000 {
		t.Fatalf("find вернул слишком много: %d", len([]rune(out)))
	}
	if out, isErr = call(t, ts, "atl", map[string]any{"action": "conf_page", "id": "123", "offset": 10_000_000}); !isErr {
		t.Fatalf("offset за концом: %s", out)
	}
	onlyGET(t, f)
}
