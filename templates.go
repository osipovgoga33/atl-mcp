package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Шаблоны страниц Confluence (Space tools -> Content tools -> Templates).
// Это не страницы: у них нет версий и CQL их не видит, отдельный REST
// /rest/api/template. На части установок он живёт под /rest/experimental/template -
// путь подбирается при первом обращении и запоминается.

type confTemplate struct {
	ID           string          `json:"templateId"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	TemplateType string          `json:"templateType"`
	Labels       json.RawMessage `json:"labels,omitempty"`
	Space        *struct {
		Key string `json:"key"`
	} `json:"space,omitempty"`
	Body struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
}

type confTemplateList struct {
	Results []confTemplate `json:"results"`
	Size    int            `json:"size"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

func (c *confTemplate) spaceKey() string {
	if c.Space == nil {
		return ""
	}
	return c.Space.Key
}

var templateBases = []string{"/rest/api/template", "/rest/experimental/template"}

// templateGet - GET по пути шаблонов с подбором базы: 404 на первой базе -
// пробуем следующую; удачную запоминаем.
func (t *toolset) templateGet(sub string, q url.Values) ([]byte, error) {
	bases := templateBases
	if t.tmplBase != "" {
		bases = []string{t.tmplBase}
	}
	var lastErr error
	for _, base := range bases {
		data, err := t.conf.do("GET", base+sub, q, nil)
		if err == nil {
			t.tmplBase = base
			return data, nil
		}
		var he *httpErr
		if errors.As(err, &he) && he.Code == 404 && t.tmplBase == "" {
			lastErr = err
			continue
		}
		return nil, err
	}
	return nil, fmt.Errorf("Confluence не отдаёт шаблоны ни по одному пути (%s): %v", strings.Join(templateBases, ", "), lastErr)
}

func (t *toolset) getTemplate(id string) (*confTemplate, error) {
	if !reID.MatchString(id) {
		return nil, fmt.Errorf("недопустимый id шаблона %q (нужны цифры)", id)
	}
	data, err := t.templateGet("/"+id, url.Values{"expand": {"body"}})
	if err != nil {
		return nil, err
	}
	var c confTemplate
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("Confluence: не разобран ответ по шаблону: %v", err)
	}
	if c.ID == "" {
		c.ID = id
	}
	return &c, nil
}

func (t *toolset) confTemplates(a args) (string, error) {
	q := url.Values{"limit": {strconv.Itoa(a.limit(50, 200))}}
	sp := a.str("space")
	if sp != "" {
		if !reSpace.MatchString(sp) {
			return "", fmt.Errorf("недопустимый ключ пространства %q", sp)
		}
		q.Set("spaceKey", sp)
	}
	data, err := t.templateGet("/page", q)
	if err != nil {
		return "", err
	}
	var l confTemplateList
	if err := json.Unmarshal(data, &l); err != nil {
		return "", fmt.Errorf("Confluence: не разобран список шаблонов: %v", err)
	}
	var b strings.Builder
	where := "глобальные"
	if sp != "" {
		where = "пространство " + sp
	}
	fmt.Fprintf(&b, "Шаблоны страниц (%s): %d", where, len(l.Results))
	if l.Links.Next != "" {
		b.WriteString(" (есть ещё - увеличь limit)")
	}
	b.WriteString("\n")
	for _, c := range l.Results {
		key := c.spaceKey()
		if key == "" {
			key = "глобальный"
		}
		fmt.Fprintf(&b, "- %s | %s | %s", c.ID, c.Name, key)
		if d := strings.TrimSpace(c.Description); d != "" {
			fmt.Fprintf(&b, " | %s", d)
		}
		b.WriteString("\n")
	}
	if len(l.Results) == 0 && sp == "" {
		b.WriteString("Шаблоны пространства без space не показываются - передай space.\n")
	}
	return b.String(), nil
}

func (t *toolset) confTemplate(a args) (string, error) {
	tp, err := t.getTemplate(a.str("id"))
	if err != nil {
		return "", err
	}
	format := a.str("format")
	if format == "" {
		format = "storage"
	}
	var body string
	switch format {
	case "storage":
		body = tp.Body.Storage.Value
	case "text":
		body = storageToText(t.resolveUsers(tp.Body.Storage.Value))
	default:
		return "", fmt.Errorf("format: storage или text")
	}
	key := tp.spaceKey()
	if key == "" {
		key = "глобальный"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "шаблон: %s\nимя: %s\nпространство: %s\n", tp.ID, tp.Name, key)
	if d := strings.TrimSpace(tp.Description); d != "" {
		fmt.Fprintf(&b, "описание: %s\n", d)
	}
	fmt.Fprintf(&b, "формат: %s\nВерсий у шаблона нет: правка пишет поверх.\n\n", format)
	return renderBody(&b, body, a)
}

// templateWritable - белый список пространств действует и на шаблоны;
// глобальный шаблон при заданном списке не пишется.
func (t *toolset) templateWritable(tp *confTemplate) error {
	key := tp.spaceKey()
	if key == "" {
		if len(t.cfg.WriteSpaces) > 0 {
			return fmt.Errorf("шаблон %s глобальный, а запись ограничена ATL_WRITE_SPACES - не пишется", tp.ID)
		}
		return nil
	}
	return t.spaceWritable(key)
}

func (t *toolset) putTemplate(tp *confTemplate, body string) error {
	if t.tmplBase == "" {
		t.tmplBase = templateBases[0]
	}
	typ := tp.TemplateType
	if typ == "" {
		typ = "page"
	}
	req := map[string]any{"templateId": tp.ID, "name": tp.Name, "templateType": typ,
		"description": tp.Description, "body": storageBody(body)}
	if key := tp.spaceKey(); key != "" {
		req["space"] = map[string]any{"key": key}
	}
	if len(tp.Labels) > 0 && string(tp.Labels) != "null" {
		req["labels"] = tp.Labels
	}
	_, err := t.conf.do("PUT", t.tmplBase, nil, req)
	return err
}

// checkShrink - тот же предохранитель, что у страниц.
func checkShrink(oldBody, newBody string, a args) (int, int, error) {
	oldLen, newLen := utf8.RuneCountInString(oldBody), utf8.RuneCountInString(newBody)
	if oldLen > 500 && newLen*2 < oldLen && !a.boolean("allow_shrink") {
		return oldLen, newLen, fmt.Errorf("после правки тело (%d симв.) более чем вдвое короче текущего (%d) - ничего не записано. Если так и задумано, передай allow_shrink=true", newLen, oldLen)
	}
	return oldLen, newLen, nil
}

func (t *toolset) confTemplatePatch(a args) (string, error) {
	id := a.str("id")
	if !reID.MatchString(id) {
		return "", fmt.Errorf("нужен id шаблона (цифры, из atl conf_templates)")
	}
	ops, err := parsePatchOps(a)
	if err != nil {
		return "", err
	}
	tp, err := t.getTemplate(id)
	if err != nil {
		return "", err
	}
	if err := t.templateWritable(tp); err != nil {
		return "", err
	}
	newBody, applied, err := applyPatch(tp.Body.Storage.Value, ops)
	if err != nil {
		return "", fmt.Errorf("ничего не записано. %v", err)
	}
	oldLen, newLen, err := checkShrink(tp.Body.Storage.Value, newBody, a)
	if err != nil {
		return "", err
	}
	dry := a.boolean("dryRun") || a.boolean("dry_run")
	if !dry {
		if err := t.putTemplate(tp, newBody); err != nil {
			return "", err
		}
	}
	return templatePatchReport(tp, applied, oldLen, newLen, dry), nil
}

func templatePatchReport(tp *confTemplate, applied []patchApplied, oldLen, newLen int, dry bool) string {
	var b strings.Builder
	if dry {
		fmt.Fprintf(&b, "Сухой прогон, ничего не записано. Шаблон %s «%s».\n", tp.ID, tp.Name)
		fmt.Fprintf(&b, "Применились бы все операции: %d; тело %d -> %d симв.\n", len(applied), oldLen, newLen)
	} else {
		fmt.Fprintf(&b, "Обновлён шаблон %s «%s»: применено операций: %d; тело %d -> %d симв.\n",
			tp.ID, tp.Name, len(applied), oldLen, newLen)
	}
	for _, p := range applied {
		occ := "уникальный"
		if p.Occurrence > 0 {
			occ = fmt.Sprintf("вхождение %d из %d", p.Occurrence, p.Count)
		}
		fmt.Fprintf(&b, "%d) символ %d, %s: %d -> %d симв.\n", p.N, p.Pos, occ, p.OldLen, p.NewLen)
		if dry {
			fmt.Fprintf(&b, "   было:  %s\n   стало: %s\n", p.Before, p.After)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (t *toolset) confTemplateUpdate(a args) (string, error) {
	id := a.str("id")
	if !reID.MatchString(id) {
		return "", fmt.Errorf("нужен id шаблона (цифры, из atl conf_templates)")
	}
	body := a.raw("body")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("нужен body - полное новое тело шаблона в storage")
	}
	tp, err := t.getTemplate(id)
	if err != nil {
		return "", err
	}
	if err := t.templateWritable(tp); err != nil {
		return "", err
	}
	oldLen, newLen, err := checkShrink(tp.Body.Storage.Value, body, a)
	if err != nil {
		return "", err
	}
	if err := t.putTemplate(tp, body); err != nil {
		return "", err
	}
	return fmt.Sprintf("Обновлён шаблон %s «%s»: тело %d -> %d симв.", tp.ID, tp.Name, oldLen, newLen), nil
}
