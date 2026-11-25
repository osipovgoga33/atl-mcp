package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// tfake - Confluence с шаблонами: 500 - шаблон пространства DEV, 501 - глобальный.
// base - путь REST шаблонов, который отвечает (второй отдаёт 404, как на
// установках, где REST живёт только под одним из путей).
type tfake struct {
	mu     sync.Mutex
	base   string
	bodies map[string]string
	reqs   []seen
	srv    *httptest.Server
}

func newTFake(t *testing.T, base, body string) *tfake {
	f := &tfake{base: base, bodies: map[string]string{"500": body, "501": "<p>глобальный</p>"}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reqs = append(f.reqs, seen{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Authorization")})
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		notFound := func() {
			w.WriteHeader(404)
			w.Write([]byte(`{"statusCode":404,"message":"null for uri"}`))
		}
		if !strings.HasPrefix(r.URL.Path, f.base) {
			notFound()
			return
		}
		tmpl := func(id string) map[string]any {
			m := map[string]any{"templateId": id, "name": "Шаблон спецификации", "description": "ФС на разработку",
				"templateType": "page", "labels": []any{map[string]any{"prefix": "global", "name": "sap_dev_doc"}},
				"body": map[string]any{"storage": map[string]any{"value": f.bodies[id], "representation": "storage"}}}
			if id == "500" {
				m["space"] = map[string]any{"key": "DEV"}
			}
			return m
		}
		sub := strings.TrimPrefix(r.URL.Path, f.base)
		switch {
		case r.Method == "GET" && sub == "/page":
			res := []any{}
			if r.URL.Query().Get("spaceKey") == "DEV" {
				res = append(res, tmpl("500"))
			} else {
				res = append(res, tmpl("501"))
			}
			json.NewEncoder(w).Encode(map[string]any{"results": res, "size": len(res)})
		case r.Method == "GET" && (sub == "/500" || sub == "/501"):
			json.NewEncoder(w).Encode(tmpl(sub[1:]))
		case r.Method == "PUT" && sub == "":
			var req struct {
				ID   string `json:"templateId"`
				Body struct {
					Storage struct {
						Value string `json:"value"`
					} `json:"storage"`
				} `json:"body"`
			}
			json.Unmarshal(b, &req)
			if _, ok := f.bodies[req.ID]; !ok {
				notFound()
				return
			}
			f.bodies[req.ID] = req.Body.Storage.Value
			json.NewEncoder(w).Encode(tmpl(req.ID))
		default:
			notFound()
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *tfake) ts(write bool, spaces ...string) *toolset {
	cfg := config{ConfURL: f.srv.URL, ConfToken: "good", JiraURL: f.srv.URL, JiraToken: "good", AllowWrite: write, WriteSpaces: map[string]bool{}}
	for _, s := range spaces {
		cfg.WriteSpaces[s] = true
	}
	return newToolset(cfg)
}

func (f *tfake) all() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]seen{}, f.reqs...)
}

func (f *tfake) puts() []seen {
	out := []seen{}
	for _, r := range f.all() {
		if r.Method != "GET" {
			out = append(out, r)
		}
	}
	return out
}

func (f *tfake) body(id string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[id]
}

const specTmpl = `<table class="wrapped relative-table" style="width: 100.0%;"><colgroup><col style="width: 11.3918%;" /><col style="width: 88.5919%;" /></colgroup>` +
	`<tbody><tr><th>Дата создания</th><td><p>Нужен макрос</p></td></tr>` +
	`<tr><th>Автор последнего изменения</th><td><p>Нужен макрос</p></td></tr></tbody></table>`

func TestTemplatesListAndRead(t *testing.T) {
	f := newTFake(t, "/rest/api/template", specTmpl)
	ts := f.ts(false)
	out, isErr := call(t, ts, "atl", map[string]any{"action": "conf_templates", "space": "DEV"})
	if isErr || !strings.Contains(out, "500 | Шаблон спецификации | DEV | ФС на разработку") {
		t.Fatal(out)
	}
	out, isErr = call(t, ts, "atl", map[string]any{"action": "conf_template", "id": "500"})
	if isErr || !strings.Contains(out, "формат: storage") || !strings.Contains(out, `<col style="width: 11.3918%;" />`) {
		t.Fatal(out)
	}
	out, isErr = call(t, ts, "atl", map[string]any{"action": "conf_template", "id": "500", "find": "Нужен макрос"})
	if isErr || !strings.Contains(out, "совпадений «Нужен макрос»: 2") {
		t.Fatal(out)
	}
	out, isErr = call(t, ts, "atl", map[string]any{"action": "conf_templates"})
	if isErr || !strings.Contains(out, "501 | Шаблон спецификации | глобальный") {
		t.Fatal(out)
	}
	for _, r := range f.all() {
		if r.Method != "GET" {
			t.Fatalf("чтение ушло не GET: %+v", r)
		}
	}
}

func TestTemplatePatchDryRunAndWrite(t *testing.T) {
	f := newTFake(t, "/rest/api/template", specTmpl)
	ts := f.ts(true)
	a := map[string]any{"action": "conf_template_patch", "id": "500", "dryRun": true, "operations": []any{
		map[string]any{"oldString": `<col style="width: 11.3918%;" /><col style="width: 88.5919%;" />`, "newString": `<col style="width: 230.0px;" /><col />`},
		map[string]any{"oldString": `Автор последнего изменения</th><td><p>Нужен макрос</p>`,
			"newString": `Автор последнего изменения</th><td><p><ac:structured-macro ac:name="change_author_ssps" ac:schema-version="1" /></p>`},
	}}
	out, isErr := call(t, ts, "atl_write", a)
	if isErr || !strings.Contains(out, "Сухой прогон") || !strings.Contains(out, "Применились бы все операции: 2") {
		t.Fatal(out)
	}
	if len(f.puts()) != 0 {
		t.Fatal("сухой прогон записал")
	}
	delete(a, "dryRun")
	out, isErr = call(t, ts, "atl_write", a)
	if isErr || !strings.Contains(out, "Обновлён шаблон 500") {
		t.Fatal(out)
	}
	got := f.body("500")
	if !strings.Contains(got, `<col style="width: 230.0px;" /><col />`) || !strings.Contains(got, "change_author_ssps") ||
		!strings.Contains(got, "Дата создания</th><td><p>Нужен макрос") {
		t.Fatal(got)
	}
	p := f.puts()
	if len(p) != 1 || p[0].Method != "PUT" || p[0].Path != "/rest/api/template" {
		t.Fatalf("%+v", p)
	}
	var req map[string]any
	json.Unmarshal([]byte(p[0].Body), &req)
	if req["templateId"] != "500" || req["name"] != "Шаблон спецификации" || req["templateType"] != "page" ||
		req["description"] != "ФС на разработку" {
		t.Fatalf("шапка шаблона потеряна: %v", req)
	}
	if sp, _ := req["space"].(map[string]any); sp["key"] != "DEV" {
		t.Fatalf("пространство потеряно: %v", req["space"])
	}
	if ls, _ := req["labels"].([]any); len(ls) != 1 {
		t.Fatalf("метки потеряны: %v", req["labels"])
	}
	st := req["body"].(map[string]any)["storage"].(map[string]any)
	if st["representation"] != "storage" {
		t.Fatalf("%v", st)
	}
}

func TestTemplateExperimentalFallback(t *testing.T) {
	f := newTFake(t, "/rest/experimental/template", specTmpl)
	ts := f.ts(true)
	if out, isErr := call(t, ts, "atl", map[string]any{"action": "conf_template", "id": "500"}); isErr {
		t.Fatal(out)
	}
	a := map[string]any{"action": "conf_template_patch", "id": "500", "operations": []any{
		map[string]any{"oldString": "Дата создания</th><td><p>Нужен макрос</p>",
			"newString": `Дата создания</th><td><p><ac:structured-macro ac:name="create_date_ssps" ac:schema-version="1" /></p>`}}}
	if out, isErr := call(t, ts, "atl_write", a); isErr {
		t.Fatal(out)
	}
	p := f.puts()
	if len(p) != 1 || p[0].Path != "/rest/experimental/template" {
		t.Fatalf("%+v", p)
	}
}

func TestTemplateGuardsBeforeNetwork(t *testing.T) {
	f := newTFake(t, "/rest/api/template", specTmpl)
	ts := f.ts(true)
	op := []any{map[string]any{"oldString": "a", "newString": "b"}}
	bad := []struct {
		tool string
		a    map[string]any
	}{
		{"atl", map[string]any{"action": "conf_template"}},
		{"atl", map[string]any{"action": "conf_template", "id": "50x"}},
		{"atl", map[string]any{"action": "conf_templates", "space": "DE V"}},
		{"atl_write", map[string]any{"action": "conf_template_patch", "operations": op}},
		{"atl_write", map[string]any{"action": "conf_template_patch", "id": "500"}},
		{"atl_write", map[string]any{"action": "conf_template_patch", "id": "500", "operations": []any{map[string]any{"oldString": "a", "newString": "a"}}}},
		{"atl_write", map[string]any{"action": "conf_template_update", "id": "500"}},
		{"atl_write", map[string]any{"action": "conf_template_update", "body": "<p>x</p>"}},
	}
	for _, c := range bad {
		if out, isErr := call(t, ts, c.tool, c.a); !isErr {
			t.Fatalf("%v: прошло: %s", c.a, out)
		}
	}
	if len(f.all()) != 0 {
		t.Fatalf("ушло в сеть: %+v", f.all())
	}
}

func TestTemplateWriteGuards(t *testing.T) {
	f := newTFake(t, "/rest/api/template", specTmpl+strings.Repeat("<p>хвост шаблона</p>", 40))
	op := []any{map[string]any{"oldString": "Дата создания", "newString": "Дата заведения"}}
	// запись выключена - не уходит ничего
	out, isErr := call(t, f.ts(false), "atl_write", map[string]any{"action": "conf_template_patch", "id": "500", "operations": op})
	if !isErr || !strings.Contains(out, "запись выключена") || len(f.all()) != 0 {
		t.Fatal(out)
	}
	// пространство не в белом списке
	if out, isErr := call(t, f.ts(true, "OTHER"), "atl_write", map[string]any{"action": "conf_template_patch", "id": "500", "operations": op}); !isErr {
		t.Fatal(out)
	}
	// глобальный шаблон при заданном белом списке
	gop := []any{map[string]any{"oldString": "глобальный", "newString": "общий"}}
	if out, isErr := call(t, f.ts(true, "DEV"), "atl_write", map[string]any{"action": "conf_template_patch", "id": "501", "operations": gop}); !isErr || !strings.Contains(out, "глобальный") {
		t.Fatal(out)
	}
	// тело вдвое короче - отказ без allow_shrink
	if out, isErr := call(t, f.ts(true), "atl_write", map[string]any{"action": "conf_template_update", "id": "500", "body": "<p>x</p>"}); !isErr || !strings.Contains(out, "allow_shrink") {
		t.Fatal(out)
	}
	if len(f.puts()) != 0 {
		t.Fatalf("запись мимо предохранителей: %+v", f.puts())
	}
	// в белом списке и с allow_shrink - пишет
	if out, isErr := call(t, f.ts(true, "DEV"), "atl_write", map[string]any{"action": "conf_template_update", "id": "500", "body": "<p>x</p>", "allow_shrink": true}); isErr {
		t.Fatal(out)
	}
	if f.body("500") != "<p>x</p>" {
		t.Fatal(f.body("500"))
	}
}

func TestTemplateToolSchema(t *testing.T) {
	f := newTFake(t, "/rest/api/template", specTmpl)
	var r, w map[string]any
	for _, tl := range f.ts(true).list() {
		switch tl["name"] {
		case "atl":
			r = tl
		case "atl_write":
			w = tl
		}
	}
	re := strings.Join(r["inputSchema"].(map[string]any)["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string), ",")
	we := strings.Join(w["inputSchema"].(map[string]any)["properties"].(map[string]any)["action"].(map[string]any)["enum"].([]string), ",")
	for _, s := range []string{"conf_templates", "conf_template"} {
		if !strings.Contains(re, s) || !strings.Contains(r["description"].(string), s) {
			t.Fatalf("%s нет в atl", s)
		}
	}
	for _, s := range []string{"conf_template_patch", "conf_template_update"} {
		if !strings.Contains(we, s) || !strings.Contains(w["description"].(string), s) {
			t.Fatalf("%s нет в atl_write", s)
		}
	}
}
