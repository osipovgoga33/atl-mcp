package main

import (
	"net/http"
	"strings"
	"testing"
)

// bulkFake — три задачи: ABC-1 (срок 2026-10-30, в работе), ABC-2 (срок уже 2026-11-30),
// ABC-3 (на PUT отвечает 400). Переходы: у ABC-1 и ABC-3 есть «Тестирование», у ABC-2 нет.
func bulkFake(t *testing.T) *jfake {
	f := newJFake(t)
	f.routes["GET /rest/api/2/search"] = func(r *http.Request, _ string) (int, string) {
		jql := r.URL.Query().Get("jql")
		if strings.Contains(jql, "many") {
			return 200, `{"total":51,"issues":[]}`
		}
		return 200, `{"total":3,"issues":[
{"key":"ABC-1","fields":{"summary":"Отчёт","status":{"name":"Разработка"},"duedate":"2026-10-30"}},
{"key":"ABC-2","fields":{"summary":"Интерфейс","status":{"name":"Тестирование"},"duedate":"2026-11-30"}},
{"key":"ABC-3","fields":{"summary":"Загрузка","status":{"name":"Разработка"},"duedate":"2026-09-30"}}]}`
	}
	ok := func(*http.Request, string) (int, string) { return 204, "" }
	f.routes["PUT /rest/api/2/issue/ABC-1"] = ok
	f.routes["PUT /rest/api/2/issue/ABC-2"] = ok
	f.routes["PUT /rest/api/2/issue/ABC-3"] = func(*http.Request, string) (int, string) {
		return 400, `{"errorMessages":[],"errors":{"duedate":"Поле нельзя изменить"}}`
	}
	tr := `{"transitions":[{"id":"21","name":"На тестирование","to":{"name":"Тестирование"}}]}`
	f.routes["GET /rest/api/2/issue/ABC-1/transitions"] = jsonR(tr)
	f.routes["GET /rest/api/2/issue/ABC-3/transitions"] = jsonR(`{"transitions":[]}`)
	f.routes["POST /rest/api/2/issue/ABC-1/transitions"] = ok
	return f
}

func TestBulkUpdateDryRunNoWrites(t *testing.T) {
	f := bulkFake(t)
	ts := jts(f, true)
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_bulk_update", "jql": "project = ABC", "due_date": "2026-11-30", "dry_run": true})
	if isErr {
		t.Fatal(out)
	}
	for _, s := range []string{"ПРЕДПРОСМОТР", "ABC-1", "duedate: 2026-10-30 → 2026-11-30", "ABC-2", "уже так, пропущено", "Будет изменено: 2"} {
		if !strings.Contains(out, s) {
			t.Fatalf("нет %q в:\n%s", s, out)
		}
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatalf("dry_run ушёл в запись: %+v", w)
	}
}

func TestBulkUpdateExecute(t *testing.T) {
	f := bulkFake(t)
	ts := jts(f, true)
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_bulk_update", "keys": []string{"abc-1", "ABC-2", "ABC-3", "ABC-1"}, "due_date": "2026-11-30"})
	if isErr {
		t.Fatal(out)
	}
	w := f.nonGET()
	if len(w) != 2 || w[0].Path != "/rest/api/2/issue/ABC-1" || w[1].Path != "/rest/api/2/issue/ABC-3" {
		t.Fatalf("ждали PUT ABC-1 и ABC-3 (ABC-2 уже так, дубль ключа — один раз), ушло %+v", w)
	}
	if !strings.Contains(w[0].Body, `"duedate":"2026-11-30"`) {
		t.Fatalf("тело: %s", w[0].Body)
	}
	for _, s := range []string{"изменено 1", "уже как надо 1", "ошибок 1 (ABC-3)", "Поле нельзя изменить"} {
		if !strings.Contains(out, s) {
			t.Fatalf("нет %q в:\n%s", s, out)
		}
	}
	// поиск по ключам — одним запросом, с полем срока
	for _, r := range f.requests() {
		if r.Method == "GET" && r.Path == "/rest/api/2/search" {
			if !strings.Contains(r.Query, "duedate") || !strings.Contains(r.Query, "ABC-1%2CABC-2%2CABC-3") {
				t.Fatalf("запрос выборки: %s", r.Query)
			}
		}
	}
}

func TestBulkGuardsBeforeWrite(t *testing.T) {
	f := bulkFake(t)
	ts := jts(f, true, "ABC")
	many := []string{}
	for i := 1; i <= 51; i++ {
		many = append(many, "ABC-"+itoa(i))
	}
	bad := []map[string]any{
		{"action": "jira_bulk_update", "due_date": "2026-11-30"},                                        // ни keys, ни jql
		{"action": "jira_bulk_update", "keys": []string{"ABC-1"}, "jql": "x", "due_date": "2026-11-30"}, // оба
		{"action": "jira_bulk_update", "keys": []string{"ABC-1"}},                                       // нечего менять
		{"action": "jira_bulk_update", "keys": []string{"ABC-1"}, "due_date": "30.11.2026"},             // формат даты
		{"action": "jira_bulk_update", "keys": []string{"ABC-1", "XYZ-1"}, "due_date": "2026-11-30"},    // белый список
		{"action": "jira_bulk_update", "keys": []string{"ABC-1", "drop table"}, "due_date": "2026-11-30"},
		{"action": "jira_bulk_update", "keys": many, "due_date": "2026-11-30"},  // больше 50
		{"action": "jira_bulk_update", "jql": "many", "due_date": "2026-11-30"}, // выборка больше 50
		{"action": "jira_bulk_update", "keys": []string{"ABC-1"}, "fields": map[string]any{"bad field": 1}},
		{"action": "jira_bulk_transition", "keys": []string{"ABC-1"}},           // нет transition
		{"action": "jira_bulk_transition", "jql": "many", "transition": "Тест"}, // выборка больше 50
	}
	for _, b := range bad {
		if out, isErr := call(t, ts, "atl_write", b); !isErr {
			t.Fatalf("%v прошло: %s", b, out)
		}
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatalf("гарды пропустили запись: %+v", w)
	}
	// задача из выборки JQL вне белого списка — отказ целиком, без записи
	f.routes["GET /rest/api/2/search"] = jsonR(`{"total":2,"issues":[{"key":"ABC-1","fields":{}},{"key":"XYZ-5","fields":{}}]}`)
	if out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_bulk_update", "jql": "x", "due_date": "2026-11-30"}); !isErr || !strings.Contains(out, "XYZ") {
		t.Fatalf("JQL вне белого списка: %s", out)
	}
	// ключ, которого нет в выдаче, — отказ целиком
	f.routes["GET /rest/api/2/search"] = jsonR(`{"total":1,"issues":[{"key":"ABC-1","fields":{}}]}`)
	if out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_bulk_update", "keys": []string{"ABC-1", "ABC-9"}, "due_date": "2026-11-30"}); !isErr || !strings.Contains(out, "ABC-9") {
		t.Fatalf("пропавший ключ: %s", out)
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatalf("ушла запись: %+v", w)
	}
}

func TestBulkTransition(t *testing.T) {
	f := bulkFake(t)
	ts := jts(f, true)
	a := map[string]any{"action": "jira_bulk_transition", "jql": "project = ABC", "transition": "Тестирование", "comment": "Готово к тесту", "dry_run": true}
	out, isErr := call(t, ts, "atl_write", a)
	if isErr {
		t.Fatal(out)
	}
	for _, s := range []string{"ABC-1 | Разработка", "«На тестирование» → Тестирование", "ABC-2", "уже в этом статусе", "ABC-3", "недоступен", "Переход доступен: 1, уже в статусе: 1, недоступен: 1"} {
		if !strings.Contains(out, s) {
			t.Fatalf("dry_run: нет %q в:\n%s", s, out)
		}
	}
	if w := f.nonGET(); len(w) != 0 {
		t.Fatalf("dry_run ушёл в запись: %+v", w)
	}
	delete(a, "dry_run")
	out, isErr = call(t, ts, "atl_write", a)
	if isErr {
		t.Fatal(out)
	}
	w := f.nonGET()
	if len(w) != 1 || w[0].Path != "/rest/api/2/issue/ABC-1/transitions" || !strings.Contains(w[0].Body, `"id":"21"`) || !strings.Contains(w[0].Body, "Готово к тесту") {
		t.Fatalf("переход: %+v", w)
	}
	if !strings.Contains(out, "переведено 1, уже в статусе 1, не переведено 1 (ABC-3)") {
		t.Fatalf("итог: %s", out)
	}
}

func itoa(i int) string {
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

func TestBulkBudgetReturnsRest(t *testing.T) {
	f := bulkFake(t)
	ts := jts(f, true)
	ts.bulkBudget = 0 // после первой задачи время вышло
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "jira_bulk_update", "keys": []string{"ABC-1", "ABC-2", "ABC-3"}, "due_date": "2026-12-31"})
	if isErr {
		t.Fatal(out)
	}
	if w := f.nonGET(); len(w) != 1 || w[0].Path != "/rest/api/2/issue/ABC-1" {
		t.Fatalf("ждали одну запись ABC-1, ушло %+v", w)
	}
	for _, s := range []string{"НЕ УСПЕЛ", "ABC-2, ABC-3", "Повтори тот же вызов", "не успел 2"} {
		if !strings.Contains(out, s) {
			t.Fatalf("нет %q в:\n%s", s, out)
		}
	}
	// предпросмотр правки полей бюджетом не режется: там только одна выборка
	f.reset()
	out, _ = call(t, ts, "atl_write", map[string]any{"action": "jira_bulk_update", "keys": []string{"ABC-1", "ABC-2", "ABC-3"}, "due_date": "2026-12-31", "dry_run": true})
	if strings.Contains(out, "НЕ УСПЕЛ") || !strings.Contains(out, "Будет изменено: 3") {
		t.Fatalf("dry_run: %s", out)
	}
	// переходы: остаток возвращается и в предпросмотре (там запрос на задачу)
	out, _ = call(t, ts, "atl_write", map[string]any{"action": "jira_bulk_transition", "keys": []string{"ABC-1", "ABC-3"}, "transition": "Тестирование", "dry_run": true})
	if !strings.Contains(out, "НЕ УСПЕЛ") || !strings.Contains(out, "не проверено 1") {
		t.Fatalf("dry_run перехода: %s", out)
	}
}

func TestCompactWorklogInSearch(t *testing.T) {
	f := newJFake(t)
	user := func(n, d string) string {
		return `{"name":"` + n + `","displayName":"` + d + `","self":"https://x/rest/api/2/user?username=` + n + `","avatarUrls":{"48x48":"https://x/a.png"}}`
	}
	wl := `{"total":5,"maxResults":3,"worklogs":[{"author":` + user("ivan", "Иван") + `,"timeSpentSeconds":7200,"self":"https://x/w/1"},` +
		`{"author":` + user("anya", "Аня") + `,"timeSpentSeconds":1800},{"author":` + user("ivan", "Иван") + `,"timeSpentSeconds":3600}]}`
	f.routes["GET /rest/api/2/search"] = jsonR(`{"total":1,"issues":[{"key":"ABC-1","fields":{"summary":"Отчёт","worklog":` + wl + `,` +
		`"comment":{"total":4,"comments":[{"body":"длинный текст"}]},"customfield_1":{"a":{"self":"https://x/1","b":"оставить"}}}}]}`)
	ts := jts(f, false)
	out, isErr := call(t, ts, "atl", map[string]any{"action": "jira_search", "jql": "x", "fields": []string{"worklog", "comment", "customfield_1"}})
	if isErr {
		t.Fatal(out)
	}
	for _, s := range []string{"worklog=записей 5 (в сводке 3", "Иван 3ч; Аня 0.5ч", "comment=комментариев 4", `customfield_1={"a":{"b":"оставить"}}`} {
		if !strings.Contains(out, s) {
			t.Fatalf("нет %q в:\n%s", s, out)
		}
	}
	for _, s := range []string{"avatarUrls", "self", "длинный текст"} {
		if strings.Contains(out, s) {
			t.Fatalf("в ответе осталось %q:\n%s", s, out)
		}
	}
}
