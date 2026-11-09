package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// client — единственная точка выхода в сеть. Гард записи стоит здесь:
// пока запись выключена, наружу не уходит ничего, кроме GET.
type client struct {
	product    string // "Confluence" | "Jira"
	base       string
	token      string
	allowWrite bool
	allowHTML  bool // для fetchRaw: файл сам по себе HTML
	initErr    error
	http       *http.Client
}

var errWriteDisabled = errors.New("запись выключена: сервер запущен без ATL_ALLOW_WRITE")

func newClient(product, base, token string, allowWrite, insecure bool, caFile string) *client {
	c := &client{product: product, base: base, token: token, allowWrite: allowWrite}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	switch {
	case insecure:
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // явный флаг пользователя
	case caFile != "":
		pool, err := loadCAPool(caFile)
		if err != nil {
			c.initErr = err
		} else {
			tr.TLSClientConfig = &tls.Config{RootCAs: pool}
		}
	}
	c.http = &http.Client{Timeout: 60 * time.Second, Transport: tr}
	return c
}

// loadCAPool — системные корневые плюс сертификаты из файла (PEM или DER, .cer/.crt/.pem).
// Go на Windows/macOS при дополненном системном пуле проверяет и системой, и добавленными.
func loadCAPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ATL_CA_FILE: %v", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	added := 0
	rest := data
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if cert, err := x509.ParseCertificate(blk.Bytes); err == nil {
			pool.AddCert(cert)
			added++
		}
	}
	if added == 0 {
		if cert, err := x509.ParseCertificate(data); err == nil { // DER
			pool.AddCert(cert)
			added++
		}
	}
	if added == 0 {
		return nil, fmt.Errorf("ATL_CA_FILE %s: сертификатов не найдено (нужен .cer/.crt/.pem в Base-64 или DER)", path)
	}
	return pool, nil
}

func (c *client) configured() error {
	if c == nil || c.base == "" || c.token == "" {
		return fmt.Errorf("не настроено: не заданы URL или токен")
	}
	if c.initErr != nil {
		return c.initErr
	}
	return nil
}

func (c *client) do(method, path string, query url.Values, body any) ([]byte, error) {
	var raw []byte
	ct := ""
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		raw, ct = buf, "application/json; charset=utf-8"
	}
	return c.send(method, path, query, ct, raw)
}

// send — запрос с ответом в JSON.
func (c *client) send(method, path string, query url.Values, contentType string, body []byte) ([]byte, error) {
	return c.core(method, path, query, contentType, body, true)
}

// fetchRaw — GET двоичного содержимого (вложения).
func (c *client) fetchRaw(path string, allowHTML bool) ([]byte, error) {
	c2 := *c
	c2.allowHTML = allowHTML
	return c2.core(http.MethodGet, path, nil, "", nil, false)
}

// core — единственный выход в сеть; гард записи здесь.
func (c *client) core(method, path string, query url.Values, contentType string, body []byte, wantJSON bool) ([]byte, error) {
	if err := c.configured(); err != nil {
		return nil, fmt.Errorf("%s %w", c.productName(), err)
	}
	if method != http.MethodGet && !c.allowWrite {
		return nil, errWriteDisabled
	}
	u := c.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Atlassian-Token", "no-check")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "x509") || strings.Contains(msg, "certificate") {
			flag := "ATL_CONF_INSECURE_TLS"
			if c.product == "Jira" {
				flag = "ATL_JIRA_INSECURE_TLS"
			}
			return nil, fmt.Errorf("%s: сертификат сервера не прошёл проверку (%v). Укажи корпоративный сертификат в ATL_CA_FILE или отключи проверку: %s=true", c.product, err, flag)
		}
		return nil, fmt.Errorf("%s недоступен (%v). Проверь URL и VPN", c.product, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAttach+1))
	if err != nil {
		return nil, err
	}
	ct := resp.Header.Get("Content-Type")
	if !wantJSON && !c.allowHTML && strings.Contains(ct, "text/html") && resp.StatusCode < 300 {
		return nil, fmt.Errorf("%s вернул HTML вместо файла — похоже на страницу входа. Проверь токен и VPN", c.product)
	}
	if resp.StatusCode >= 300 {
		return nil, c.httpError(resp.StatusCode, ct, data)
	}
	if len(data) > maxAttach {
		return nil, fmt.Errorf("%s: ответ больше %d МБ — не принят", c.product, maxAttach>>20)
	}
	if wantJSON && len(data) > 0 && !strings.Contains(ct, "json") {
		return nil, fmt.Errorf("%s вернул %s вместо JSON — похоже на страницу входа или прокси. Проверь URL (корень сервера) и VPN", c.product, firstWord(ct))
	}
	return data, nil
}

func (c *client) productName() string { return c.product }

func firstWord(s string) string {
	if i := strings.IndexAny(s, "; "); i > 0 {
		return s[:i]
	}
	if s == "" {
		return "ответ без типа"
	}
	return s
}

func (c *client) httpError(code int, ct string, data []byte) error {
	msg := ""
	if strings.Contains(ct, "json") {
		var e struct {
			Message       string            `json:"message"`
			ErrorMessages []string          `json:"errorMessages"`
			Errors        map[string]string `json:"errors"`
			Data          struct {
				Errors []struct {
					Message struct {
						Key         string `json:"key"`
						Translation string `json:"translation"`
					} `json:"message"`
				} `json:"errors"`
			} `json:"data"`
		}
		if json.Unmarshal(data, &e) == nil {
			parts := []string{}
			if e.Message != "" {
				parts = append(parts, e.Message)
			}
			parts = append(parts, e.ErrorMessages...)
			for k, v := range e.Errors {
				parts = append(parts, k+": "+v)
			}
			for _, de := range e.Data.Errors {
				if de.Message.Translation != "" {
					parts = append(parts, de.Message.Translation)
				} else if de.Message.Key != "" {
					parts = append(parts, de.Message.Key)
				}
			}
			msg = strings.Join(parts, "; ")
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(data))
		if len(msg) > 300 || strings.HasPrefix(msg, "<") {
			msg = ""
		}
	}
	hint := ""
	switch code {
	case 401:
		hint = "токен не принят — проверь PAT (не истёк ли, тот ли сервер)"
	case 403:
		hint = "нет прав на это действие или объект"
	case 404:
		hint = "не найдено (или нет прав на просмотр)"
	case 409:
		hint = "конфликт версий — страницу изменили, перечитай"
	}
	out := fmt.Sprintf("%s: HTTP %d", c.product, code)
	if hint != "" {
		out += " — " + hint
	}
	if msg != "" {
		out += ". " + msg
	}
	return errors.New(out)
}
