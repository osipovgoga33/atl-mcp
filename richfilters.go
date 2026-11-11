package main

// Дашборды Jira и rich-фильтры плагина Rich Filters for Jira Dashboards,
// плюс замер времени JQL. Всё только чтение (GET).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const rfBase = "/rest/qoti-rich-filters/latest/rich-filters"

// ---------- дашборды ----------

func (t *toolset) jiraDashboards(a args) (string, error) {
	text := strings.ToLower(a.str("text"))
	q := url.Values{"maxResults": {"100"}}
	head := "Избранные дашборды"
	if text == "" {
		q.Set("filter", "favourite")
	} else {
		q.Set("maxResults", "1000")
		head = fmt.Sprintf("Дашборды с «%s» в названии", a.str("text"))
	}
	var r struct {
		Total      int              `json:"total"`
		Dashboards []map[string]any `json:"dashboards"`
	}
	if err := t.jget("/rest/api/2/dashboard", q, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, d := range r.Dashboards {
		name := mstr(d, "name")
		if text != "" && !strings.Contains(strings.ToLower(name), text) {
			continue
		}
		n++
		fmt.Fprintf(&b, "- %s | %s\n", mstr(d, "id"), name)
	}
	return fmt.Sprintf("%s: %d\n%s", head, n, b.String()), nil
}

// reGadgetTitle: заголовок гаджета в настройках хранится как «-041E-0442…_…»:
// код символа после дефиса, подчёркивание вместо пробела.
var reGadgetTitle = regexp.MustCompile(`-([0-9A-Fa-f]{4})`)

func gadgetTitle(s string) string {
	if s == "" || s == "." {
		return ""
	}
	if !reGadgetTitle.MatchString(s) {
		return s
	}
	s = reGadgetTitle.ReplaceAllStringFunc(s, func(m string) string {
		n, err := strconv.ParseUint(m[1:], 16, 32)
		if err != nil {
			return m
		}
		return string(rune(n))
	})
	return strings.ReplaceAll(s, "_", " ")
}

type rfDef struct {
	ID         int              `json:"id"`
	Name       string           `json:"name"`
	JiraFilter json.RawMessage  `json:"jiraFilter"`
	Admins     []map[string]any `json:"admins"`
	Static     []rfClause       `json:"staticFilters"`
	Dynamic    []map[string]any `json:"dynamicFilters"`
	Smart      []rfSmart        `json:"smartFilters"`
	Views      []rfView         `json:"richViews"`
	Queues     []any            `json:"richQueues"`
	Values     []any            `json:"customValues"`
	Ratios     []any            `json:"customRatios"`
	Series     []any            `json:"timeSeries"`
}

type rfText struct {
	Input string `json:"input"`
	Value any    `json:"value"`
}

func (x rfText) String() string {
	if s, ok := x.Value.(string); ok && s != "" {
		return s
	}
	return x.Input
}

type rfClause struct {
	ID    int    `json:"id"`
	Label rfText `json:"label"`
	JQL   rfText `json:"jql"`
}

type rfSmart struct {
	ID      int        `json:"id"`
	Name    rfText     `json:"name"`
	Clauses []rfClause `json:"clauses"`
}

type rfView struct {
	ID      int    `json:"id"`
	Name    rfText `json:"name"`
	Columns []struct {
		Type        rfText `json:"type"`
		Field       rfText `json:"field"`
		SmartFilter rfText `json:"smartFilter"`
	} `json:"columns"`
}

// baseFilter: базовый фильтр Jira. В ответе по id он лежит прямо, в поиске под value.
func (d rfDef) baseFilter() (id, name, jql string) {
	var f map[string]any
	if json.Unmarshal(d.JiraFilter, &f) != nil || f == nil {
		return "", "", ""
	}
	if v, ok := f["value"].(map[string]any); ok {
		f = v
	}
	id, name = mstr(f, "id"), mstr(f, "name")
	if j, ok := f["jql"].(map[string]any); ok {
		jql = mstr(j, "value")
		if jql == "" {
			jql = mstr(j, "input")
		}
	} else {
		jql = mstr(f, "jql")
	}
	return
}

func (d rfDef) smartName(id string) string {
	for _, s := range d.Smart {
		if strconv.Itoa(s.ID) == id {
			return s.Name.String()
		}
	}
	return ""
}

func (t *toolset) loadRichFilter(id string) (rfDef, error) {
	var d rfDef
	data, err := t.jira.do(http.MethodGet, rfBase+"/"+id, nil, nil)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return d, fmt.Errorf("rich-фильтр %s не найден или плагин Rich Filters не установлен: %v", id, err)
		}
		return d, err
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return d, fmt.Errorf("Jira: не разобран rich-фильтр %s: %v", id, err)
	}
	return d, nil
}

func (t *toolset) jiraDashboard(a args) (string, error) {
	id, err := idArg(a, "id")
	if err != nil {
		return "", err
	}
	var d struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Layout  string `json:"layout"`
		Gadgets []struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			Column    int    `json:"column"`
			AMD       string `json:"amdModule"`
			UserPrefs struct {
				Fields []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"fields"`
			} `json:"userPrefs"`
		} `json:"gadgets"`
	}
	if err := t.jget("/rest/dashboards/1.0/"+id, nil, &d); err != nil {
		return "", err
	}
	rfs := map[string]rfDef{}
	var b strings.Builder
	fmt.Fprintf(&b, "Дашборд %s: %s, гаджетов %d\n", d.ID, d.Title, len(d.Gadgets))
	for _, g := range d.Gadgets {
		prefs := map[string]string{}
		keys := []string{}
		for _, f := range g.UserPrefs.Fields {
			prefs[f.Name] = f.Value
			if f.Value == "" || f.Value == "." || f.Name == "isConfigured" || f.Name == "title" {
				continue
			}
			keys = append(keys, f.Name)
		}
		sort.Strings(keys)
		name := gadgetTitle(prefs["title"])
		if name == "" {
			name = g.Title
		}
		fmt.Fprintf(&b, "- [%s] «%s» (%s), колонка %d", g.ID, name, g.Title, g.Column)
		var rf rfDef
		if v := prefs["rf"]; reID.MatchString(v) {
			if _, ok := rfs[v]; !ok {
				if def, err := t.loadRichFilter(v); err == nil {
					rfs[v] = def
				} else {
					rfs[v] = rfDef{}
				}
			}
			rf = rfs[v]
		}
		parts := []string{}
		for _, k := range keys {
			v := prefs[k]
			label := ""
			switch {
			case k == "rf" && rf.Name != "":
				label = rf.Name
			case k == "smartFilter":
				label = rf.smartName(v)
			case strings.HasPrefix(v, "smart:"):
				label = rf.smartName(strings.TrimPrefix(v, "smart:"))
			}
			if label != "" {
				parts = append(parts, fmt.Sprintf("%s=%s (%s)", k, v, label))
			} else {
				parts = append(parts, k+"="+v)
			}
		}
		if len(parts) > 0 {
			b.WriteString(": " + strings.Join(parts, ", "))
		}
		b.WriteString("\n")
	}
	if len(rfs) > 0 {
		b.WriteString("\nНастройки rich-фильтра целиком: jira_rich_filter с id; время условий: jira_jql_time с rich_filter.\n")
	}
	return truncate(b.String(), "сузь запрос"), nil
}

// ---------- rich-фильтры ----------

func (t *toolset) jiraRichFilter(a args) (string, error) {
	if text := a.str("text"); text != "" && a.str("id") == "" {
		var r struct {
			Results []rfDef `json:"results"`
		}
		if err := t.jget(rfBase+"/search", url.Values{"query": {text}}, &r); err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Rich-фильтры по «%s»: %d\n", text, len(r.Results))
		for _, d := range r.Results {
			fid, fname, _ := d.baseFilter()
			fmt.Fprintf(&b, "- %d | %s | базовый фильтр %s «%s» | умных фильтров %d, представлений %d\n",
				d.ID, d.Name, fid, fname, len(d.Smart), len(d.Views))
		}
		return truncate(b.String(), "уточни text"), nil
	}
	id, err := idArg(a, "id")
	if err != nil {
		return "", fmt.Errorf("укажи id rich-фильтра (из jira_dashboard, prefs rf) или text для поиска по названию")
	}
	d, err := t.loadRichFilter(id)
	if err != nil {
		return "", err
	}
	fid, fname, fjql := d.baseFilter()
	var b strings.Builder
	fmt.Fprintf(&b, "Rich-фильтр %d: %s\n", d.ID, d.Name)
	fmt.Fprintf(&b, "Базовый фильтр: %s «%s»\nJQL: %s\n", fid, fname, fjql)
	if len(d.Admins) > 0 {
		names := []string{}
		for _, u := range d.Admins {
			names = append(names, mstr(u, "username"))
		}
		fmt.Fprintf(&b, "Админы: %s\n", strings.Join(names, ", "))
	}
	if len(d.Static) > 0 {
		b.WriteString("\nБыстрые фильтры:\n")
		for _, c := range d.Static {
			fmt.Fprintf(&b, "- [%d] %s: %s\n", c.ID, c.Label.String(), c.JQL.String())
		}
	}
	if len(d.Dynamic) > 0 {
		b.WriteString("\nДинамические фильтры:\n")
		for _, f := range d.Dynamic {
			fmt.Fprintf(&b, "- [%s] %s: поле %s, %s\n", mstr(f, "id"), mstr(f, "displayName"), mstr(f, "key"), mstr(f, "type"))
		}
	}
	if len(d.Smart) > 0 {
		b.WriteString("\nУмные фильтры:\n")
		for _, s := range d.Smart {
			fmt.Fprintf(&b, "- [%d] %s\n", s.ID, s.Name.String())
			for _, c := range s.Clauses {
				fmt.Fprintf(&b, "  - [%d] %s: %s\n", c.ID, c.Label.String(), c.JQL.String())
			}
		}
	}
	if len(d.Views) > 0 {
		b.WriteString("\nПредставления:\n")
		for _, v := range d.Views {
			cols := []string{}
			for _, c := range v.Columns {
				switch {
				case c.SmartFilter.Input != "":
					cols = append(cols, fmt.Sprintf("умный «%s» (%s)", d.smartName(c.SmartFilter.Input), c.SmartFilter.Input))
				case c.Field.Input != "":
					cols = append(cols, c.Field.Input)
				default:
					cols = append(cols, c.Type.Input)
				}
			}
			fmt.Fprintf(&b, "- [%d] %s: %s\n", v.ID, v.Name.String(), strings.Join(cols, ", "))
		}
	}
	if n := len(d.Queues) + len(d.Values) + len(d.Ratios) + len(d.Series); n > 0 {
		fmt.Fprintf(&b, "\nЕщё: очередей %d, своих значений %d, отношений %d, временных рядов %d\n",
			len(d.Queues), len(d.Values), len(d.Ratios), len(d.Series))
	}
	return truncate(b.String(), "умные фильтры и представления выше полностью не поместились"), nil
}

// ---------- замер JQL ----------

type jqlItem struct{ label, jql string }

// jiraJQLTime: сколько Jira считает каждый запрос (maxResults=0: только число задач).
// Источник: jql / jqls или условия rich-фильтра поверх его базового фильтра.
// Запросы по одному, не параллельно: замер под нагрузкой ничего не говорит,
// а тяжёлый запрос в несколько потоков нагружает общий сервер.
func (t *toolset) jiraJQLTime(a args) (string, error) {
	items := []jqlItem{}
	prefix := ""
	if rid := a.str("rich_filter"); rid != "" {
		if !reID.MatchString(rid) {
			return "", fmt.Errorf("rich_filter должен быть числом")
		}
		d, err := t.loadRichFilter(rid)
		if err != nil {
			return "", err
		}
		fid, _, _ := d.baseFilter()
		if !reID.MatchString(fid) {
			return "", fmt.Errorf("у rich-фильтра %s не найден базовый фильтр", rid)
		}
		prefix = "filter = " + fid
		only := a.str("smart_filter")
		if only != "" && !reID.MatchString(only) {
			return "", fmt.Errorf("smart_filter должен быть числом (id умного фильтра из jira_rich_filter)")
		}
		if only == "" {
			items = append(items, jqlItem{"базовый фильтр", prefix})
			for _, c := range d.Static {
				items = append(items, jqlItem{"быстрый «" + c.Label.String() + "»", prefix + " AND (" + c.JQL.String() + ")"})
			}
		}
		for _, s := range d.Smart {
			if only != "" && strconv.Itoa(s.ID) != only {
				continue
			}
			for _, c := range s.Clauses {
				items = append(items, jqlItem{fmt.Sprintf("%s [%d] «%s»", s.Name.String(), c.ID, c.Label.String()), prefix + " AND (" + c.JQL.String() + ")"})
			}
		}
	}
	for i, q := range a.list("jqls") {
		items = append(items, jqlItem{fmt.Sprintf("запрос %d", i+1), q})
	}
	if q := a.str("jql"); q != "" {
		items = append(items, jqlItem{"запрос", q})
	}
	if len(items) == 0 {
		return "", fmt.Errorf("укажи jql, jqls [] или rich_filter (и при желании smart_filter)")
	}
	start, _ := a.num("start_at")
	if start < 0 || start >= len(items) {
		start = 0
	}
	deadline := time.Now().Add(t.bulkBudget)
	var b strings.Builder
	if prefix != "" {
		fmt.Fprintf(&b, "Каждое условие считается поверх «%s», по одному запросу, только число задач.\n", prefix)
	}
	slow := 0
	next := -1
	for i := start; i < len(items); i++ {
		left := time.Until(deadline)
		if left < 2*time.Second {
			next = i
			break
		}
		it := items[i]
		c2 := *t.jira
		h := *t.jira.http
		h.Timeout = left
		c2.http = &h
		t0 := time.Now()
		data, err := c2.do(http.MethodGet, "/rest/api/2/search", url.Values{"jql": {it.jql}, "maxResults": {"0"}, "fields": {"key"}}, nil)
		ms := time.Since(t0).Milliseconds()
		mark := ""
		if ms >= 5000 {
			mark = " ⚠ медленно"
			slow++
		}
		switch {
		case err != nil && time.Since(t0) >= left-200*time.Millisecond:
			fmt.Fprintf(&b, "%d. %s: не дождался, больше %.1f с ⚠\n   %s\n", i, it.label, float64(ms)/1000, it.jql)
			if mark == "" {
				slow++
			}
			next = i + 1
		case err != nil:
			fmt.Fprintf(&b, "%d. %s: ошибка %v\n   %s\n", i, it.label, err, it.jql)
		default:
			var r struct {
				Total int `json:"total"`
			}
			_ = json.Unmarshal(data, &r)
			fmt.Fprintf(&b, "%d. %s: %.1f с, задач %d%s\n", i, it.label, float64(ms)/1000, r.Total, mark)
			if mark != "" {
				fmt.Fprintf(&b, "   %s\n", it.jql)
			}
		}
		if next == i+1 {
			break
		}
	}
	if next >= 0 && next < len(items) {
		fmt.Fprintf(&b, "\nНЕ УСПЕЛ: осталось %d из %d. Повтори тот же вызов с start_at=%d.\n", len(items)-next, len(items), next)
	}
	if slow > 0 {
		fmt.Fprintf(&b, "\nМедленных (от 5 с): %d. Типичная причина: подзапрос функции ScriptRunner (linkedIssuesOf, subtasksOf, parentsOf…) по всей Jira. Сузь его до задач базового фильтра.\n", slow)
	}
	return truncate(b.String(), "сузь smart_filter"), nil
}
