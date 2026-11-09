package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

type seen struct {
	Method, Path, Query, Body, Auth string
}

type fake struct {
	mu   sync.Mutex
	reqs []seen
	srv  *httptest.Server
	// состояние страницы 123
	version int
	body    string
}

func newFake(t *testing.T) *fake {
	f := &fake{version: 5, body: "<h1>Спека</h1><p>" + strings.Repeat("Текст спецификации. ", 60) + "</p>" +
		`<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[SELECT * FROM mara.]]></ac:plain-text-body></ac:structured-macro>`}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, seen{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Authorization")})
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer good" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Write([]byte(`{"statusCode":401,"message":"Not authorized"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		page := func() map[string]any {
			return map[string]any{"id": "123", "type": "page", "title": "ZFI спека",
				"space":     map[string]any{"key": "DEV", "name": "Разработка"},
				"version":   map[string]any{"number": f.version, "when": "2026-10-07T10:11:12.000+03:00", "by": map[string]any{"displayName": "Иван"}},
				"body":      map[string]any{"storage": map[string]any{"value": f.body}},
				"ancestors": []any{map[string]any{"id": "1", "title": "Проект"}},
				"_links":    map[string]any{"webui": "/pages/viewpage.action?pageId=123"}}
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/search":
			json.NewEncoder(w).Encode(map[string]any{"results": []any{page()}, "size": 1})
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/123":
			json.NewEncoder(w).Encode(page())
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/555":
			p := page()
			p["id"] = "555"
			p["space"] = map[string]any{"key": "OTHER"}
			json.NewEncoder(w).Encode(p)
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/321":
			p := page()
			p["id"], p["title"] = "321", "Архив"
			json.NewEncoder(w).Encode(p)
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/888":
			p := page()
			p["id"], p["title"] = "888", "Потомок"
			p["ancestors"] = []any{map[string]any{"id": "1"}, map[string]any{"id": "123"}}
			json.NewEncoder(w).Encode(p)
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/123/child/attachment":
			if r.URL.Query().Get("filename") == "old.png" {
				w.Write([]byte(`{"results":[{"id":"att7","title":"old.png","version":{"number":2}}]}`))
			} else {
				w.Write([]byte(`{"results":[]}`))
			}
		case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/rest/api/content/123/child/attachment"):
			w.Write([]byte(`{"results":[{"id":"att8"}]}`))
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/123/child/comment":
			w.Write([]byte(`{"results":[{"id":"c1","body":{"storage":{"value":"<p>Вопрос</p>"}},"version":{"by":{"displayName":"Аня"},"when":"2026-10-07T09:00:00"}},{"id":"c2","ancestors":[{"id":"c1"}],"body":{"storage":{"value":"<p>Ответ</p>"}},"version":{"by":{"displayName":"Иван"}}}]}`))
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/777":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>login</html>"))
		case r.Method == "GET" && r.URL.Path == "/rest/api/content":
			json.NewEncoder(w).Encode(map[string]any{"results": []any{page()}})
		case r.Method == "PUT" && r.URL.Path == "/rest/api/content/123":
			var req map[string]any
			json.Unmarshal(b, &req)
			f.version = int(req["version"].(map[string]any)["number"].(float64))
			json.NewEncoder(w).Encode(page())
		case r.Method == "POST" && r.URL.Path == "/rest/api/content":
			json.NewEncoder(w).Encode(map[string]any{"id": "999", "title": "Новая", "version": map[string]any{"number": 1}, "_links": map[string]any{"webui": "/x/999"}})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/label"):
			w.Write([]byte(`{"results":[]}`))
		case r.Method == "GET" && r.URL.Path == "/rest/api/2/issue/ABC-1":
			w.Write([]byte(`{"key":"ABC-1","fields":{"summary":"Сделать отчёт","status":{"name":"В работе"},"assignee":{"displayName":"Георгий"},"created":"2026-10-01T09:00:00.000+0300","updated":"2026-10-02T09:00:00.000+0300","description":"h1. ТЗ"}}`))
		case r.Method == "GET" && r.URL.Path == "/rest/api/2/search":
			w.Write([]byte(`{"total":1,"issues":[{"key":"ABC-1","fields":{"summary":"Сделать отчёт","status":{"name":"В работе"}}}]}`))
		case r.Method == "POST" && r.URL.Path == "/rest/api/2/issue/ABC-1/comment":
			w.Write([]byte(`{"id":"10001"}`))
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"statusCode":404,"message":"No content found"}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fake) requests() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen{}, f.reqs...)
}

func (f *fake) reset() { f.mu.Lock(); f.reqs = nil; f.mu.Unlock() }

func tsFor(f *fake, write bool, spaces ...string) *toolset {
	cfg := config{ConfURL: f.srv.URL, ConfToken: "good", JiraURL: f.srv.URL, JiraToken: "good", AllowWrite: write, WriteSpaces: map[string]bool{}}
	for _, s := range spaces {
		cfg.WriteSpaces[s] = true
	}
	return newToolset(cfg)
}

func call(t *testing.T, ts *toolset, tool string, a map[string]any) (string, bool) {
	t.Helper()
	// гоняем через JSON, как придёт от клиента
	raw, _ := json.Marshal(a)
	var m map[string]any
	json.Unmarshal(raw, &m)
	return ts.call(tool, m)
}

func onlyGET(t *testing.T, f *fake) {
	t.Helper()
	for _, r := range f.requests() {
		if r.Method != "GET" {
			t.Fatalf("ушёл не-GET запрос: %+v", r)
		}
	}
}

func TestProtocol(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, false)
	msgs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"atl","arguments":{"action":"conf_page","id":"123"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
	}
	var out bytes.Buffer
	serve(strings.NewReader(strings.Join(msgs, "\n")+"\n"), &out, ts)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("ожидалось 4 ответа (уведомление без ответа), получено %d:\n%s", len(lines), out.String())
	}
	var r1 map[string]any
	json.Unmarshal([]byte(lines[0]), &r1)
	if r1["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("версия протокола: %s", lines[0])
	}
	var r2 struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(lines[1]), &r2)
	if len(r2.Result.Tools) != 1 || r2.Result.Tools[0]["name"] != "atl" {
		t.Fatalf("при выключенной записи должен быть только atl: %s", lines[1])
	}
	if !strings.Contains(lines[2], "version: 5") || !strings.Contains(lines[2], `"isError":false`) {
		t.Fatalf("conf_page: %s", lines[2])
	}
	if !strings.Contains(lines[3], "-32601") {
		t.Fatalf("неизвестный метод: %s", lines[3])
	}
	ts2 := tsFor(f, true)
	if n := len(ts2.list()); n != 2 {
		t.Fatalf("при включённой записи должно быть 2 инструмента, есть %d", n)
	}
}

func TestReadActionsOnlyGET(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, true) // даже с включённой записью чтение — только GET
	cases := []map[string]any{
		{"action": "conf_search", "text": "отчёт", "space": "DEV"},
		{"action": "conf_page", "id": "123", "format": "storage"},
		{"action": "conf_page", "space": "DEV", "title": "ZFI спека"},
		{"action": "conf_children", "id": "123"},
		{"action": "conf_spaces"},
		{"action": "conf_attachments", "id": "123"},
		{"action": "jira_issue", "key": "abc-1", "comments": true},
		{"action": "jira_search", "jql": "project = ABC"},
	}
	for _, c := range cases {
		if out, isErr := call(t, ts, "atl", c); isErr && !strings.Contains(out, "404") {
			t.Fatalf("%v: %s", c, out)
		}
	}
	onlyGET(t, f)
	for _, r := range f.requests() {
		if r.Auth != "Bearer good" {
			t.Fatalf("нет Bearer: %+v", r)
		}
	}
}

func TestSearchCQL(t *testing.T) {
	f := newFake(t)
	out, isErr := call(t, tsFor(f, false), "atl", map[string]any{"action": "conf_search", "text": `a"b`, "space": "DEV"})
	if isErr {
		t.Fatal(out)
	}
	q := f.requests()[0].Query
	if !strings.Contains(q, "type%3Dpage") || !strings.Contains(q, `a%5C%22b`) {
		t.Fatalf("CQL собран неверно: %s", q)
	}
}

func TestWriteGuardInClient(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, false)
	if _, err := ts.conf.do("POST", "/rest/api/content", nil, map[string]any{}); err != errWriteDisabled {
		t.Fatalf("гард записи не сработал: %v", err)
	}
	if out, isErr := call(t, ts, "atl_write", map[string]any{"action": "conf_create", "space": "DEV", "title": "x", "body": "<p>x</p>"}); !isErr || !strings.Contains(out, "запись выключена") {
		t.Fatalf("atl_write при выключенной записи: %s", out)
	}
	if len(f.requests()) != 0 {
		t.Fatalf("при выключенной записи в сеть ушло: %+v", f.requests())
	}
}

func TestUpdateVersionMismatchNoPUT(t *testing.T) {
	f := newFake(t)
	out, isErr := call(t, tsFor(f, true), "atl_write", map[string]any{"action": "conf_update", "id": "123", "version": 4, "body": f.body})
	if !isErr || !strings.Contains(out, "изменилась") {
		t.Fatalf("ожидался отказ по версии: %s", out)
	}
	onlyGET(t, f)
}

func TestUpdateShrinkGuard(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, true)
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "conf_update", "id": "123", "version": 5, "body": "<p>обрывок</p>"})
	if !isErr || !strings.Contains(out, "allow_shrink") {
		t.Fatalf("ожидался отказ по сокращению: %s", out)
	}
	onlyGET(t, f)
	f.reset()
	out, isErr = call(t, ts, "atl_write", map[string]any{"action": "conf_update", "id": "123", "version": 5, "body": "<p>обрывок</p>", "allow_shrink": true})
	if isErr {
		t.Fatal(out)
	}
}

func TestUpdateOK(t *testing.T) {
	f := newFake(t)
	newBody := f.body + "<p>Добавлено</p>"
	out, isErr := call(t, tsFor(f, true, "DEV"), "atl_write", map[string]any{"action": "conf_update", "id": "123", "version": 5, "body": newBody, "message": "правка"})
	if isErr {
		t.Fatal(out)
	}
	var put *seen
	for _, r := range f.requests() {
		if r.Method == "PUT" {
			r := r
			put = &r
		}
	}
	if put == nil {
		t.Fatal("PUT не ушёл")
	}
	var req map[string]any
	json.Unmarshal([]byte(put.Body), &req)
	v := req["version"].(map[string]any)
	if v["number"].(float64) != 6 || v["message"] != "правка" || req["title"] != "ZFI спека" {
		t.Fatalf("тело PUT: %s", put.Body)
	}
	if req["body"].(map[string]any)["storage"].(map[string]any)["value"] != newBody {
		t.Fatal("тело страницы не то")
	}
	if !strings.Contains(out, "5 → 6") {
		t.Fatalf("ответ: %s", out)
	}
}

func TestWriteSpacesWhitelist(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, true, "DEV")
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "conf_create", "space": "HR", "title": "x", "body": "<p>x</p>"})
	if !isErr || !strings.Contains(out, "ATL_WRITE_SPACES") {
		t.Fatalf("белый список не сработал: %s", out)
	}
	out, isErr = call(t, ts, "atl_write", map[string]any{"action": "conf_update", "id": "555", "version": 5, "body": f.body})
	if !isErr || !strings.Contains(out, "OTHER") {
		t.Fatalf("белый список на update: %s", out)
	}
	onlyGET(t, f)
}

func TestCreateWithParentAndLabels(t *testing.T) {
	f := newFake(t)
	out, isErr := call(t, tsFor(f, true), "atl_write", map[string]any{"action": "conf_create", "parent_id": "123", "title": "Новая", "body": "<p>x</p>", "labels": []string{"spec", "zfi"}})
	if isErr {
		t.Fatal(out)
	}
	var post string
	labels := false
	for _, r := range f.requests() {
		if r.Method == "POST" && r.Path == "/rest/api/content" {
			post = r.Body
		}
		if r.Method == "POST" && r.Path == "/rest/api/content/999/label" {
			labels = strings.Contains(r.Body, `"spec"`)
		}
	}
	if !strings.Contains(post, `"key":"DEV"`) || !strings.Contains(post, `"ancestors":[{"id":"123"}]`) {
		t.Fatalf("тело создания: %s", post)
	}
	if !labels {
		t.Fatal("метки не ушли")
	}
}

func TestValidationBeforeNetwork(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, true)
	bad := []struct {
		tool string
		a    map[string]any
	}{
		{"atl", map[string]any{"action": "conf_page", "id": "123/../../admin"}},
		{"atl", map[string]any{"action": "conf_children", "id": "abc"}},
		{"atl", map[string]any{"action": "jira_issue", "key": "ABC-1/../x"}},
		{"atl", map[string]any{"action": "jira_issue", "key": "ABC-1", "fields": []string{"a,b"}}},
		{"atl", map[string]any{"action": "conf_search", "space": "DE V", "text": "x"}},
		{"atl_write", map[string]any{"action": "conf_update", "id": "123", "body": "<p>x</p>"}},
		{"atl_write", map[string]any{"action": "conf_create", "space": "DEV", "title": "x", "body": "<p>x</p>", "labels": []string{"с пробелом"}}},
	}
	for _, b := range bad {
		if out, isErr := call(t, ts, b.tool, b.a); !isErr {
			t.Fatalf("%v прошло: %s", b.a, out)
		}
	}
	if len(f.requests()) != 0 {
		t.Fatalf("валидация пропустила запрос в сеть: %+v", f.requests())
	}
}

func TestErrors(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, false)
	ts.conf.token = "bad"
	out, _ := call(t, ts, "atl", map[string]any{"action": "conf_page", "id": "123"})
	if !strings.Contains(out, "401") || !strings.Contains(out, "PAT") {
		t.Fatalf("401: %s", out)
	}
	ts.conf.token = "good"
	out, _ = call(t, ts, "atl", map[string]any{"action": "conf_page", "id": "777"})
	if !strings.Contains(out, "вместо JSON") {
		t.Fatalf("HTML: %s", out)
	}
	out, _ = call(t, ts, "atl", map[string]any{"action": "conf_page", "id": "404"})
	if !strings.Contains(out, "404") || !strings.Contains(out, "No content found") {
		t.Fatalf("404: %s", out)
	}
	empty := newToolset(config{})
	out, isErr := call(t, empty, "atl", map[string]any{"action": "jira_issue", "key": "A-1"})
	if !isErr || !strings.Contains(out, "Jira не настроено") {
		t.Fatalf("не настроено: %s", out)
	}
}

func TestStorageToText(t *testing.T) {
	s := `<h2>Назначение</h2><p>Отчёт &amp; выгрузка</p><ul><li>раз</li><li>два</li></ul>` +
		`<table><tr><th>Поле</th><th>Тип</th></tr><tr><td>BUKRS</td><td>CHAR4</td></tr></table>` +
		`<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[IF a < b.]]></ac:plain-text-body></ac:structured-macro>` +
		`<p><ac:link><ri:page ri:content-title="Другая страница"/></ac:link></p>`
	got := storageToText(s)
	for _, want := range []string{"## Назначение", "Отчёт & выгрузка", "- раз", "BUKRS | CHAR4", "[макрос code]", "IF a < b.", "[[Другая страница]]"} {
		if !strings.Contains(got, want) {
			t.Fatalf("нет %q в:\n%s", want, got)
		}
	}
}

func TestJira(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, true)
	out, isErr := call(t, ts, "atl", map[string]any{"action": "jira_issue", "key": "ABC-1"})
	if isErr || !strings.Contains(out, "В работе") || !strings.Contains(out, "Георгий") || !strings.Contains(out, "h1. ТЗ") {
		t.Fatalf("jira_issue: %s", out)
	}
	out, isErr = call(t, ts, "atl_write", map[string]any{"action": "jira_comment", "key": "ABC-1", "body": "Готово"})
	if isErr || !strings.Contains(out, "10001") {
		t.Fatalf("jira_comment: %s", out)
	}
}

func lastNonGET(f *fake) *seen {
	var out *seen
	for _, r := range f.requests() {
		if r.Method != "GET" {
			r := r
			out = &r
		}
	}
	return out
}

func TestAttach(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, true)
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "conf_attach", "id": "123", "name": "схема.svg", "content": "<svg/>", "message": "схема процесса"})
	if isErr || !strings.Contains(out, "добавлено") || !strings.Contains(out, `<ac:image><ri:attachment ri:filename="схема.svg"/></ac:image>`) {
		t.Fatalf("attach new: %s", out)
	}
	p := lastNonGET(f)
	if p.Path != "/rest/api/content/123/child/attachment" || !strings.Contains(p.Body, "<svg/>") || !strings.Contains(p.Body, "схема процесса") {
		t.Fatalf("multipart: %+v", p)
	}
	f.reset()
	out, isErr = call(t, ts, "atl_write", map[string]any{"action": "conf_attach", "id": "123", "name": "old.png", "content_base64": "iVBORw0KGgo="})
	if isErr || !strings.Contains(out, "обновлено (было v2)") {
		t.Fatalf("attach existing: %s", out)
	}
	if p := lastNonGET(f); p.Path != "/rest/api/content/123/child/attachment/att7/data" {
		t.Fatalf("обновление вложения ушло не туда: %s", p.Path)
	}
	// файл с диска
	dir := t.TempDir()
	fp := dir + "/spec.pdf"
	os.WriteFile(fp, []byte("%PDF-1.4"), 0o644)
	out, isErr = call(t, ts, "atl_write", map[string]any{"action": "conf_attach", "id": "123", "path": fp})
	if isErr || !strings.Contains(out, "spec.pdf") || !strings.Contains(out, "<ac:link>") {
		t.Fatalf("attach path: %s", out)
	}
}

func TestAttachGuardsBeforeNetwork(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, true)
	dir := t.TempDir()
	key := dir + "/id_rsa"
	os.WriteFile(key, []byte("secret"), 0o600)
	bad := []map[string]any{
		{"action": "conf_attach", "id": "123", "path": key},                                  // без разрешённого расширения
		{"action": "conf_attach", "id": "123", "path": key, "name": "x.png"},                 // подмена имени не помогает
		{"action": "conf_attach", "id": "123", "name": "x.exe", "content": "MZ"},             // расширение
		{"action": "conf_attach", "id": "123", "name": "../x.png", "content": "x"},           // путь в имени
		{"action": "conf_attach", "id": "123", "content": "x"},                               // нет имени
		{"action": "conf_attach", "id": "123", "name": "a.png", "content": "x", "path": key}, // два источника
		{"action": "conf_attach", "id": "123", "name": "a.png", "content_base64": "!!!"},     // кривой base64
		{"action": "conf_attach", "id": "123", "path": dir + "/nope.png"},                    // нет файла
	}
	for _, b := range bad {
		if out, isErr := call(t, ts, "atl_write", b); !isErr {
			t.Fatalf("%v прошло: %s", b, out)
		}
	}
	if len(f.requests()) != 0 {
		t.Fatalf("гард вложений пропустил запрос: %+v", f.requests())
	}
	// при выключенной записи — ни одного запроса
	ro := tsFor(f, false)
	if _, err := ro.conf.send("POST", "/x", nil, "text/plain", []byte("x")); err != errWriteDisabled {
		t.Fatalf("send без гарда: %v", err)
	}
	if len(f.requests()) != 0 {
		t.Fatal("send ушёл в сеть при выключенной записи")
	}
}

func TestComments(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, true)
	out, isErr := call(t, ts, "atl", map[string]any{"action": "conf_comments", "id": "123"})
	if isErr || !strings.Contains(out, "c2 ответ на c1") || !strings.Contains(out, "Вопрос") {
		t.Fatalf("conf_comments: %s", out)
	}
	f.reset()
	out, isErr = call(t, ts, "atl_write", map[string]any{"action": "conf_comment", "id": "123", "body": "<p>Принято</p>", "parent_comment_id": "c1"})
	if !isErr {
		t.Fatalf("нецифровой parent_comment_id должен отклоняться: %s", out)
	}
	out, isErr = call(t, ts, "atl_write", map[string]any{"action": "conf_comment", "id": "123", "body": "<p>Принято</p>", "parent_comment_id": "42"})
	if isErr {
		t.Fatal(out)
	}
	p := lastNonGET(f)
	if !strings.Contains(p.Body, `"type":"comment"`) || !strings.Contains(p.Body, `"container":{"id":"123","type":"page"}`) || !strings.Contains(p.Body, `"ancestors":[{"id":"42"}]`) {
		t.Fatalf("тело комментария: %s", p.Body)
	}
}

func TestMove(t *testing.T) {
	f := newFake(t)
	ts := tsFor(f, true)
	out, isErr := call(t, ts, "atl_write", map[string]any{"action": "conf_move", "id": "123", "parent_id": "321"})
	if isErr || !strings.Contains(out, "Проект (1) → Архив (321)") {
		t.Fatalf("move: %s", out)
	}
	var req map[string]any
	json.Unmarshal([]byte(lastNonGET(f).Body), &req)
	if req["ancestors"].([]any)[0].(map[string]any)["id"] != "321" || req["version"].(map[string]any)["number"].(float64) != 6 {
		t.Fatalf("PUT переноса: %v", req)
	}
	if req["body"].(map[string]any)["storage"].(map[string]any)["value"] != f.body {
		t.Fatal("при переносе тело страницы изменилось")
	}
	f.reset()
	for _, b := range []map[string]any{
		{"action": "conf_move", "id": "123", "parent_id": "555"}, // другое пространство
		{"action": "conf_move", "id": "123", "parent_id": "888"}, // под своего потомка
		{"action": "conf_move", "id": "123", "parent_id": "123"}, // под себя
		{"action": "conf_move", "id": "123", "parent_id": "321", "version": 3},
	} {
		if out, isErr := call(t, ts, "atl_write", b); !isErr {
			t.Fatalf("%v прошло: %s", b, out)
		}
	}
	onlyGET(t, f)
	out, _ = call(t, ts, "atl_write", map[string]any{"action": "conf_move", "id": "123", "parent_id": "1"})
	if !strings.Contains(out, "уже лежит") {
		t.Fatalf("перенос на место: %s", out)
	}
}
