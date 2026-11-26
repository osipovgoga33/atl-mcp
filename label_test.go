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

// labelFake - страница 700 в пространстве DEV с метками, ведёт их как Confluence.
type labelFake struct {
	mu     sync.Mutex
	labels []string
	reqs   []seen
	srv    *httptest.Server
	failOn string // имя метки, на снятии которой вернуть 500
}

func newLabelFake(t *testing.T, labels ...string) *labelFake {
	f := &labelFake{labels: labels}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reqs = append(f.reqs, seen{r.Method, r.URL.Path, r.URL.RawQuery, string(b), r.Header.Get("Authorization")})
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/700":
			json.NewEncoder(w).Encode(map[string]any{"id": "700", "type": "page", "title": "Инструкция",
				"space": map[string]any{"key": "DEV"}, "version": map[string]any{"number": 2}})
		case r.Method == "GET" && r.URL.Path == "/rest/api/content/700/label":
			res := []any{}
			for _, l := range f.labels {
				res = append(res, map[string]any{"prefix": "global", "name": l, "id": "1"})
			}
			json.NewEncoder(w).Encode(map[string]any{"results": res, "size": len(res)})
		case r.Method == "POST" && r.URL.Path == "/rest/api/content/700/label":
			var in []map[string]any
			json.Unmarshal(b, &in)
			for _, l := range in {
				f.labels = append(f.labels, l["name"].(string))
			}
			w.Write([]byte(`{"results":[]}`))
		case r.Method == "DELETE" && r.URL.Path == "/rest/api/content/700/label":
			name := r.URL.Query().Get("name")
			if name == f.failOn {
				w.WriteHeader(500)
				w.Write([]byte(`{"statusCode":500,"message":"boom"}`))
				return
			}
			keep := []string{}
			for _, l := range f.labels {
				if l != name {
					keep = append(keep, l)
				}
			}
			f.labels = keep
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
			w.Write([]byte(`{"statusCode":404,"message":"No content found"}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *labelFake) ts(write bool, spaces ...string) *toolset {
	cfg := config{ConfURL: f.srv.URL, ConfToken: "good", JiraURL: f.srv.URL, JiraToken: "good", AllowWrite: write, WriteSpaces: map[string]bool{}}
	for _, s := range spaces {
		cfg.WriteSpaces[s] = true
	}
	return newToolset(cfg)
}

func (f *labelFake) writes() []seen {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []seen{}
	for _, r := range f.reqs {
		if r.Method != "GET" {
			out = append(out, r)
		}
	}
	return out
}

func TestConfLabelRemove(t *testing.T) {
	f := newLabelFake(t, "sap_dev_doc", "bw")
	out, isErr := call(t, f.ts(true), "atl_write", map[string]any{"action": "conf_label", "id": "700", "remove": []any{"sap_dev_doc"}})
	if isErr || !strings.Contains(out, "сняты: sap_dev_doc") {
		t.Fatalf("ответ: %v %s", isErr, out)
	}
	w := f.writes()
	if len(w) != 1 || w[0].Method != "DELETE" || w[0].Query != "name=sap_dev_doc" {
		t.Fatalf("запросы записи: %+v", w)
	}
	if strings.Join(f.labels, ",") != "bw" {
		t.Fatalf("метки после: %v", f.labels)
	}
}

func TestConfLabelDryRunNoWrite(t *testing.T) {
	f := newLabelFake(t, "sap_dev_doc")
	for _, flag := range []string{"dryRun", "dry_run"} {
		out, isErr := call(t, f.ts(true), "atl_write", map[string]any{"action": "conf_label", "id": "700",
			"labels": []any{"instr"}, "remove": []any{"sap_dev_doc", "nope"}, flag: true})
		if isErr || !strings.Contains(out, "Сухой прогон") || !strings.Contains(out, "снялись бы: sap_dev_doc") ||
			!strings.Contains(out, "добавились бы: instr") || !strings.Contains(out, "нет на странице, не снимались: nope") {
			t.Fatalf("%s: %v %s", flag, isErr, out)
		}
	}
	if w := f.writes(); len(w) != 0 {
		t.Fatalf("dryRun ушёл в запись: %+v", w)
	}
}

func TestConfLabelAddSkipsExisting(t *testing.T) {
	f := newLabelFake(t, "a")
	out, isErr := call(t, f.ts(true), "atl_write", map[string]any{"action": "conf_label", "id": "700", "labels": []any{"A", "b"}})
	if isErr || !strings.Contains(out, "добавлены: b") || !strings.Contains(out, "уже стоят, не добавлялись: A") {
		t.Fatalf("%v %s", isErr, out)
	}
	w := f.writes()
	if len(w) != 1 || w[0].Method != "POST" || strings.Contains(w[0].Body, `"A"`) {
		t.Fatalf("запросы записи: %+v", w)
	}
}

func TestConfLabelNothingToDo(t *testing.T) {
	f := newLabelFake(t, "x")
	out, isErr := call(t, f.ts(true), "atl_write", map[string]any{"action": "conf_label", "id": "700", "remove": []any{"y"}})
	if isErr || !strings.Contains(out, "менять нечего") {
		t.Fatalf("%v %s", isErr, out)
	}
	if w := f.writes(); len(w) != 0 {
		t.Fatalf("лишняя запись: %+v", w)
	}
}

func TestConfLabelGuards(t *testing.T) {
	f := newLabelFake(t, "x")
	cases := []struct {
		a    map[string]any
		want string
	}{
		{map[string]any{"action": "conf_label", "id": "700"}, "нужен labels"},
		{map[string]any{"action": "conf_label", "id": "700", "remove": []any{"a b"}}, "недопустимая метка"},
		{map[string]any{"action": "conf_label", "id": "700", "labels": []any{"x"}, "remove": []any{"X"}}, "и в labels, и в remove"},
		{map[string]any{"action": "conf_label", "id": "../1", "remove": []any{"x"}}, "нужен id"},
	}
	for _, c := range cases {
		out, isErr := call(t, f.ts(true), "atl_write", c.a)
		if !isErr || !strings.Contains(out, c.want) {
			t.Fatalf("%v: %v %s", c.a, isErr, out)
		}
	}
	if len(f.reqs) != 0 {
		t.Fatalf("гард пропустил запрос в сеть: %+v", f.reqs)
	}
	// чужое пространство - отказ до записи
	out, isErr := call(t, f.ts(true, "OTHER"), "atl_write", map[string]any{"action": "conf_label", "id": "700", "remove": []any{"x"}})
	if !isErr || !strings.Contains(out, "ATL_WRITE_SPACES") {
		t.Fatalf("пространство: %v %s", isErr, out)
	}
	// запись выключена - только отказ
	out, isErr = call(t, f.ts(false), "atl_write", map[string]any{"action": "conf_label", "id": "700", "remove": []any{"x"}})
	if !isErr || len(f.writes()) != 0 {
		t.Fatalf("без ATL_ALLOW_WRITE: %v %s", isErr, out)
	}
}

func TestConfLabelPartialFailureReported(t *testing.T) {
	f := newLabelFake(t, "a", "b")
	f.failOn = "b"
	out, isErr := call(t, f.ts(true), "atl_write", map[string]any{"action": "conf_label", "id": "700", "remove": []any{"a", "b"}})
	if !isErr || !strings.Contains(out, "метка b не снята") || !strings.Contains(out, "уже сняты: a") {
		t.Fatalf("%v %s", isErr, out)
	}
}

func TestConfLabelSchemaHasRemove(t *testing.T) {
	f := newLabelFake(t)
	for _, tool := range f.ts(true).list() {
		if tool["name"] != "atl_write" {
			continue
		}
		p := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if p["remove"].(map[string]any)["type"] != "array" {
			t.Fatal("в схеме нет remove массивом")
		}
		return
	}
	t.Fatal("нет atl_write")
}
