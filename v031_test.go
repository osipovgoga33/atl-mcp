package main

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Фрагмент спеки в духе реестра разработок (вымышленные имена).
const specTable = `<table><tbody>
<tr><th><p>Статус разработки</p></th><td><p><ac:structured-macro ac:name="status" ac:schema-version="1"><ac:parameter ac:name="colour">Green</ac:parameter><ac:parameter ac:name="title">работает</ac:parameter></ac:structured-macro></p></td></tr>
<tr><th><p>Пакет или ПИ</p></th><td><p>ZFI_DEMO_PACKAGE</p><p>второй абзац</p></td></tr>
<tr><th><p>Отв. разработчик</p></th><td><p><ac:link><ri:user ri:userkey="8a8a1b2c"/></ac:link></p></td></tr>
<tr><th><p>Транзакция</p></th><td><ul><li>Z_DEMO_TCODE Начисление &amp; налоги</li></ul></td></tr>
</tbody></table>
<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">abap</ac:parameter><ac:plain-text-body><![CDATA[IF a < b.]]></ac:plain-text-body></ac:structured-macro>`

func TestTableRendering(t *testing.T) {
	got := storageToText(specTable)
	for _, want := range []string{
		"| Статус разработки | [статус: работает] |",
		"| Пакет или ПИ | ZFI_DEMO_PACKAGE второй абзац |",
		"| Транзакция | - Z_DEMO_TCODE Начисление & налоги |",
		"IF a < b.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("нет %q в:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Greenработает") || strings.Contains(got, "abap\n") {
		t.Fatalf("параметры макросов просочились в текст:\n%s", got)
	}
}

func TestResolveUsers(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/rest/api/user" && r.URL.Query().Get("key") == "8a8a1b2c" {
			calls++
			w.Write([]byte(`{"displayName":"Иван Петров"}`))
			return
		}
		w.WriteHeader(404)
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	ts := newToolset(config{ConfURL: srv.URL, ConfToken: "x"})
	s := specTable + `<p><ri:user ri:userkey="8a8a1b2c"/> и <ri:user ri:userkey="unknown"/></p>`
	got := storageToText(ts.resolveUsers(s))
	if !strings.Contains(got, "| Отв. разработчик | @Иван Петров |") || !strings.Contains(got, "@пользователь") {
		t.Fatalf("имена:\n%s", got)
	}
	if calls != 1 {
		t.Fatalf("повторный ключ должен браться из кэша, запросов %d", calls)
	}
}

func TestStatusTimesDoneWithoutResolutionDate(t *testing.T) {
	f := newJFake(t)
	f.routes["GET /rest/api/2/issue/ABC-7"] = jsonR(`{"key":"ABC-7","fields":{"summary":"x","created":"2025-12-09T16:35:00.000+0300",
"status":{"name":"Выполнено","statusCategory":{"key":"done"}},
"resolutiondate":null},"changelog":{"total":2,"histories":[
{"created":"2025-12-09T20:35:00.000+0300","author":{"displayName":"Г"},"items":[{"field":"status","fromString":"В очереди","toString":"В работе"}]},
{"created":"2025-12-29T11:12:00.000+0300","author":{"displayName":"Г"},"items":[{"field":"status","fromString":"В работе","toString":"Выполнено"}]}]}}`)
	out, isErr := call(t, jts(f, false), "atl", map[string]any{"action": "jira_status_times", "key": "ABC-7"})
	if isErr {
		t.Fatal(out)
	}
	if !strings.Contains(out, "до перехода в финальный статус 2025-12-29 11:12") || !strings.Contains(out, "Выполнено | 0.0 ч") {
		t.Fatalf("финальный статус без даты решения:\n%s", out)
	}
	if !strings.Contains(out, "В очереди | 4.0 ч") {
		t.Fatalf("первый статус:\n%s", out)
	}
}

func TestTLSOptions(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()
	// без доверия — понятная ошибка с подсказкой
	c := newClient("Jira", srv.URL, "x", false, false, "")
	_, err := c.do("GET", "/rest/api/2/field", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "ATL_CA_FILE") || !strings.Contains(err.Error(), "ATL_JIRA_INSECURE_TLS") {
		t.Fatalf("ошибка сертификата: %v", err)
	}
	// с CA-файлом (PEM) — проверка проходит
	dir := t.TempDir()
	pemPath := filepath.Join(dir, "corp.cer")
	os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o644)
	if _, err := newClient("Jira", srv.URL, "x", false, false, pemPath).do("GET", "/x", nil, nil); err != nil {
		t.Fatalf("PEM: %v", err)
	}
	// DER тоже
	derPath := filepath.Join(dir, "corp.der")
	os.WriteFile(derPath, srv.Certificate().Raw, 0o644)
	if _, err := newClient("Jira", srv.URL, "x", false, false, derPath).do("GET", "/x", nil, nil); err != nil {
		t.Fatalf("DER: %v", err)
	}
	// без проверки — проходит
	if _, err := newClient("Jira", srv.URL, "x", false, true, "").do("GET", "/x", nil, nil); err != nil {
		t.Fatalf("insecure: %v", err)
	}
	// битый файл — ошибка до сети
	bad := filepath.Join(dir, "bad.cer")
	os.WriteFile(bad, []byte("не сертификат"), 0o644)
	if _, err := newClient("Jira", srv.URL, "x", false, false, bad).do("GET", "/x", nil, nil); err == nil || !strings.Contains(err.Error(), "сертификатов не найдено") {
		t.Fatalf("битый CA: %v", err)
	}
	// флаги раздельные, общий — на оба
	os.Setenv("ATL_JIRA_INSECURE_TLS", "true")
	cfg := loadConfig()
	os.Unsetenv("ATL_JIRA_INSECURE_TLS")
	if cfg.ConfInsecure || !cfg.JiraInsecure {
		t.Fatalf("раздельные флаги: %+v", cfg)
	}
	os.Setenv("ATL_INSECURE_TLS", "1")
	cfg = loadConfig()
	os.Unsetenv("ATL_INSECURE_TLS")
	if !cfg.ConfInsecure || !cfg.JiraInsecure {
		t.Fatal("общий флаг должен действовать на оба")
	}
	_ = json.Valid
	_ = time.Now
}
