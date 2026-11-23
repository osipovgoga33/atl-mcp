package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// pfake - Confluence со страницей 700, которая хранит тело и версию, как
// настоящая: PUT с номером не current+1 получает 409. bumpOnGET имитирует
// чужую правку между нашим чтением и записью.
type pfake struct {
	mu        sync.Mutex
	version   int
	body      string
	space     string
	reqs      []seen
	bumpOnGET int    // сколько раз после GET страницы «кто-то» её правит
	bumpWith  string // что чужая правка дописывает в конец
	srv       *httptest.Server
}

func newPFake(t *testing.T, body string) *pfake {
	f := &pfake{version: 12, body: body, space: "DEV"}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reqs = append(f.reqs, seen{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Authorization")})
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		if r.URL.Path != "/rest/api/content/700" {
			w.WriteHeader(404)
			w.Write([]byte(`{"statusCode":404,"message":"No content found"}`))
			return
		}
		page := func() map[string]any {
			return map[string]any{"id": "700", "type": "page", "title": "Реестр номеров",
				"space":   map[string]any{"key": f.space},
				"version": map[string]any{"number": f.version, "when": "2026-11-23T10:00:00.000+03:00", "by": map[string]any{"displayName": "Иван"}},
				"body":    map[string]any{"storage": map[string]any{"value": f.body}},
				"_links":  map[string]any{"webui": "/pages/viewpage.action?pageId=700"}}
		}
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(page())
			if f.bumpOnGET > 0 {
				f.bumpOnGET--
				f.version++
				f.body += f.bumpWith
			}
		case "PUT":
			var req struct {
				Title   string `json:"title"`
				Version struct {
					Number  int    `json:"number"`
					Message string `json:"message"`
				} `json:"version"`
				Body struct {
					Storage struct {
						Value          string `json:"value"`
						Representation string `json:"representation"`
					} `json:"storage"`
				} `json:"body"`
			}
			json.Unmarshal(b, &req)
			if req.Version.Number != f.version+1 {
				w.WriteHeader(409)
				w.Write([]byte(`{"statusCode":409,"message":"Version must be incremented on update. Current version is: ` + strconv.Itoa(f.version) + `"}`))
				return
			}
			f.version, f.body = req.Version.Number, req.Body.Storage.Value
			json.NewEncoder(w).Encode(page())
		default:
			w.WriteHeader(405)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *pfake) ts(write bool, spaces ...string) *toolset {
	cfg := config{ConfURL: f.srv.URL, ConfToken: "good", JiraURL: f.srv.URL, JiraToken: "good", AllowWrite: write, WriteSpaces: map[string]bool{}}
	for _, s := range spaces {
		cfg.WriteSpaces[s] = true
	}
	return newToolset(cfg)
}

func (f *pfake) puts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.reqs {
		if r.Method != "GET" {
			n++
		}
	}
	return n
}

func (f *pfake) state() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.version, f.body
}

func row(n, text string) string {
	return "<tr><td><p>" + n + "</p></td><td><p>" + text + "</p></td></tr>"
}

func registry() string {
	var b strings.Builder
	b.WriteString(`<p>Реестр</p><table><tbody><tr><th>Номер</th><th>Название</th></tr>`)
	for _, r := range [][2]string{{"1510", "Отчёт А"}, {"1511", "Отчёт Б"}, {"1512", "Отчёт В"}} {
		b.WriteString(row(r[0], r[1]))
	}
	b.WriteString(`</tbody></table>` + strings.Repeat("<p>хвост страницы</p>", 50))
	return b.String()
}

func patchArgs(ops ...map[string]any) map[string]any {
	list := []any{}
	for _, o := range ops {
		list = append(list, o)
	}
	return map[string]any{"action": "conf_patch", "id": "700", "operations": list}
}

func TestPatchInsertRowAfter(t *testing.T) {
	f := newPFake(t, registry())
	old := row("1512", "Отчёт В")
	a := patchArgs(map[string]any{"oldString": old, "newString": old + row("1513", "Отчёт Г")})
	a["message"] = "1513"
	out, isErr := call(t, f.ts(true, "DEV"), "atl_write", a)
	if isErr {
		t.Fatal(out)
	}
	for _, s := range []string{"version 12 -> 13", "применено операций: 1"} {
		if !strings.Contains(out, s) {
			t.Fatalf("нет %q в\n%s", s, out)
		}
	}
	v, body := f.state()
	if v != 13 || !strings.Contains(body, row("1512", "Отчёт В")+row("1513", "Отчёт Г")+"</tbody>") {
		t.Fatalf("v=%d тело:\n%s", v, body)
	}
	if !strings.Contains(f.lastBody(), `"message":"1513"`) {
		t.Fatal("message не ушёл")
	}
}

func TestPatchDryRunNoWrite(t *testing.T) {
	f := newPFake(t, registry())
	old := row("1512", "Отчёт В")
	for _, flag := range []string{"dryRun", "dry_run"} {
		a := patchArgs(map[string]any{"oldString": old, "newString": old + row("1513", "Новая")})
		a[flag] = true
		out, isErr := call(t, f.ts(true), "atl_write", a)
		if isErr {
			t.Fatal(out)
		}
		for _, s := range []string{"Сухой прогон, ничего не записано", "version 12", "было:  ", "стало: ", "1513", "Отчёт Б"} {
			if !strings.Contains(out, s) {
				t.Fatalf("%s: нет %q в\n%s", flag, s, out)
			}
		}
	}
	if f.puts() != 0 {
		t.Fatal("сухой прогон записал")
	}
	if v, _ := f.state(); v != 12 {
		t.Fatal("версия сдвинулась")
	}
}

func TestPatchAllOrNothing(t *testing.T) {
	f := newPFake(t, registry())
	ts := f.ts(true)
	cases := []struct {
		name string
		ops  []map[string]any
		want string
	}{
		{"не найден", []map[string]any{
			{"oldString": row("1511", "Отчёт Б"), "newString": row("1511", "Отчёт Б!")},
			{"oldString": "<p>нет такого</p>", "newString": "x"}}, "операция 2: oldString не найден"},
		{"не уникален", []map[string]any{{"oldString": "<p>хвост страницы</p>", "newString": "<p>x</p>"}}, "встречается 50 раз"},
		{"occurrence больше", []map[string]any{{"oldString": "<p>Отчёт В</p>", "newString": "x", "occurrence": 3}}, "occurrence 3, а вхождений 1"},
		// вторая операция ищет то, что убрала первая: применяются по порядку
		{"по порядку", []map[string]any{
			{"oldString": "Отчёт А", "newString": "Отчёт Я"},
			{"oldString": "Отчёт А", "newString": "Отчёт Ю"}}, "операция 2: oldString не найден"},
	}
	for _, c := range cases {
		out, isErr := call(t, ts, "atl_write", patchArgs(c.ops...))
		if !isErr || !strings.Contains(out, c.want) || !strings.Contains(out, "ничего не записано") {
			t.Fatalf("%s: %v %s", c.name, isErr, out)
		}
	}
	if f.puts() != 0 {
		t.Fatal("частичная правка ушла в запись")
	}
	if _, body := f.state(); body != registry() {
		t.Fatal("тело изменилось")
	}
}

func TestPatchOccurrenceAndSequence(t *testing.T) {
	f := newPFake(t, registry())
	out, isErr := call(t, f.ts(true), "atl_write", patchArgs(
		map[string]any{"oldString": "<p>хвост страницы</p>", "newString": "<p>второй хвост</p>", "occurrence": 2},
		map[string]any{"oldString": "Отчёт А", "newString": "Отчёт Я"},
		map[string]any{"oldString": "Отчёт Я", "newString": "Отчёт Я2"}))
	if isErr || !strings.Contains(out, "применено операций: 3") || !strings.Contains(out, "вхождение 2 из 50") {
		t.Fatal(out)
	}
	_, body := f.state()
	if !strings.Contains(body, "<p>хвост страницы</p><p>второй хвост</p><p>хвост страницы</p>") || !strings.Contains(body, "Отчёт Я2") {
		t.Fatal(body)
	}
}

func TestPatchConflictRetryOnce(t *testing.T) {
	f := newPFake(t, registry())
	f.bumpOnGET, f.bumpWith = 1, "<p>чужая правка</p>"
	old := row("1512", "Отчёт В")
	out, isErr := call(t, f.ts(true), "atl_write", patchArgs(map[string]any{"oldString": old, "newString": old + row("1513", "Новая")}))
	if isErr || !strings.Contains(out, "version 13 -> 14") || !strings.Contains(out, "Был конфликт версии") {
		t.Fatal(out)
	}
	v, body := f.state()
	if v != 14 || !strings.Contains(body, "<p>чужая правка</p>") || !strings.Contains(body, row("1513", "Новая")) {
		t.Fatalf("v=%d чужая правка потеряна или наша не легла:\n%s", v, body)
	}
}

func TestPatchConflictTwiceRefuses(t *testing.T) {
	f := newPFake(t, registry())
	f.bumpOnGET, f.bumpWith = 5, "<p>ещё</p>"
	old := row("1512", "Отчёт В")
	out, isErr := call(t, f.ts(true), "atl_write", patchArgs(map[string]any{"oldString": old, "newString": old + row("1513", "Новая")}))
	if !isErr || !strings.Contains(out, "дважды изменили") || !strings.Contains(out, "HTTP 409") {
		t.Fatal(out)
	}
	if f.puts() != 2 {
		t.Fatalf("PUT должно быть ровно 2 (одна повторная попытка), было %d", f.puts())
	}
	if _, body := f.state(); strings.Contains(body, "1513") {
		t.Fatal("правка легла")
	}
}

func TestPatchGuardsBeforeNetwork(t *testing.T) {
	f := newPFake(t, registry())
	ts := f.ts(true)
	bad := []map[string]any{
		{"action": "conf_patch", "operations": []any{map[string]any{"oldString": "a", "newString": "b"}}},
		{"action": "conf_patch", "id": "70x", "operations": []any{map[string]any{"oldString": "a", "newString": "b"}}},
		{"action": "conf_patch", "id": "700"},
		{"action": "conf_patch", "id": "700", "operations": []any{}},
		{"action": "conf_patch", "id": "700", "operations": []any{map[string]any{"oldString": "", "newString": "b"}}},
		{"action": "conf_patch", "id": "700", "operations": []any{map[string]any{"oldString": "a"}}},
		{"action": "conf_patch", "id": "700", "operations": []any{map[string]any{"oldString": "a", "newString": "a"}}},
		{"action": "conf_patch", "id": "700", "operations": []any{map[string]any{"oldString": "a", "newString": "b", "occurrence": 0}}},
		{"action": "conf_patch", "id": "700", "operations": []any{map[string]any{"oldString": "a", "newString": "b", "occurence": 1}}},
		{"action": "conf_patch", "id": "700", "operations": []any{"строка"}},
	}
	for _, a := range bad {
		if out, isErr := call(t, ts, "atl_write", a); !isErr {
			t.Fatalf("%v: прошло: %s", a, out)
		}
	}
	if len(f.all()) != 0 {
		t.Fatalf("ушло в сеть: %+v", f.all())
	}
}

func TestPatchOperationsAsJSONString(t *testing.T) {
	f := newPFake(t, registry())
	a := map[string]any{"action": "conf_patch", "id": "700", "dryRun": true,
		"operations": `[{"oldString":"Отчёт А","newString":"Отчёт Я"}]`}
	if out, isErr := call(t, f.ts(true), "atl_write", a); isErr || !strings.Contains(out, "Сухой прогон") {
		t.Fatal(out)
	}
}

func TestPatchWriteGuards(t *testing.T) {
	f := newPFake(t, registry())
	op := map[string]any{"oldString": "Отчёт А", "newString": "Отчёт Я"}
	// запись выключена - не уходит ничего
	if out, isErr := call(t, f.ts(false), "atl_write", patchArgs(op)); !isErr || !strings.Contains(out, "запись выключена") {
		t.Fatal(out)
	}
	if len(f.all()) != 0 {
		t.Fatal("ушёл запрос при выключенной записи")
	}
	// пространство не в белом списке - только чтение
	if out, isErr := call(t, f.ts(true, "OTHER"), "atl_write", patchArgs(op)); !isErr {
		t.Fatal(out)
	}
	if f.puts() != 0 {
		t.Fatal("запись мимо белого списка")
	}
	// сокращение тела вдвое - отказ без allow_shrink
	big := map[string]any{"oldString": strings.Repeat("<p>хвост страницы</p>", 50), "newString": ""}
	if out, isErr := call(t, f.ts(true), "atl_write", patchArgs(big)); !isErr || !strings.Contains(out, "allow_shrink") {
		t.Fatal(out)
	}
	if f.puts() != 0 {
		t.Fatal("сокращение ушло в запись")
	}
}

func TestPatchToolSchema(t *testing.T) {
	f := newPFake(t, registry())
	var w map[string]any
	for _, tl := range f.ts(true).list() {
		if tl["name"] == "atl_write" {
			w = tl
		}
	}
	if w == nil || !strings.Contains(w["description"].(string), "conf_patch") {
		t.Fatal("conf_patch нет в описании atl_write")
	}
	p := w["inputSchema"].(map[string]any)["properties"].(map[string]any)
	if p["operations"].(map[string]any)["type"] != "array" || p["dryRun"].(map[string]any)["type"] != "boolean" {
		t.Fatal("схема operations/dryRun")
	}
	enum := p["action"].(map[string]any)["enum"].([]string)
	if !strings.Contains(strings.Join(enum, ","), "conf_patch") {
		t.Fatal("conf_patch нет в enum")
	}
}

func TestApplyPatchUTF8Context(t *testing.T) {
	body := strings.Repeat("я", 400) + "ЦЕЛЬ" + strings.Repeat("ж", 400)
	_, ap, err := applyPatch(body, []patchOp{{Old: "ЦЕЛЬ", New: "МИШЕНЬ"}})
	if err != nil {
		t.Fatal(err)
	}
	if ap[0].Pos != 400 || ap[0].Before != strings.Repeat("я", patchContext)+"⟦ЦЕЛЬ⟧"+strings.Repeat("ж", patchContext) {
		t.Fatalf("%+v", ap[0])
	}
}

func (f *pfake) all() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen{}, f.reqs...)
}

func (f *pfake) lastBody() string {
	r := f.all()
	return r[len(r)-1].Body
}
