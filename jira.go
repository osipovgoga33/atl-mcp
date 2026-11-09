package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	reProject  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,30}$`)
	reJiraUser = regexp.MustCompile(`^[^\s/?#&\\]{1,255}$`)
)

var jiraBaseFields = []string{"summary", "status", "issuetype", "priority", "assignee", "reporter",
	"created", "updated", "duedate", "resolution", "resolutiondate", "components", "fixVersions", "labels",
	"parent", "subtasks", "issuelinks", "attachment", "timetracking", "description"}

func jiraVal(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case map[string]any:
		if s, ok := compactContainer(x); ok {
			return s
		}
		if s, ok := compactLink(x); ok {
			return s
		}
		for _, k := range []string{"displayName", "name", "value", "key"} {
			if s, ok := x[k].(string); ok && s != "" {
				if k == "key" {
					if f, ok := x["fields"].(map[string]any); ok {
						if sm, ok := f["summary"].(string); ok {
							return s + " " + sm
						}
					}
				}
				return s
			}
		}
		return compactJSON(x)
	case []any:
		parts := []string{}
		for _, e := range x {
			if s := jiraVal(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return compactJSON(v)
}

// compactContainer — сводка вместо JSON для полей-контейнеров (worklog, comment):
// полный JSON трудозатрат одной задачи с аватарами и ссылками весит ~10 КБ.
func compactContainer(x map[string]any) (string, bool) {
	if wl, ok := x["worklogs"].([]any); ok {
		byAuthor := map[string]float64{}
		order := []string{}
		for _, e := range wl {
			w, _ := e.(map[string]any)
			who := jiraVal(w["author"])
			if _, seen := byAuthor[who]; !seen {
				order = append(order, who)
			}
			sec, _ := w["timeSpentSeconds"].(float64)
			byAuthor[who] += sec
		}
		parts := []string{}
		for _, who := range order {
			parts = append(parts, fmt.Sprintf("%s %s", who, strconv.FormatFloat(byAuthor[who]/3600, 'f', -1, 64)+"ч"))
		}
		total, _ := x["total"].(float64)
		out := fmt.Sprintf("записей %d", int(total))
		if int(total) > len(wl) {
			out += fmt.Sprintf(" (в сводке %d, полностью — jira_worklog)", len(wl))
		}
		if len(parts) > 0 {
			out += ": " + strings.Join(parts, "; ")
		}
		return out, true
	}
	if cs, ok := x["comments"].([]any); ok {
		total, _ := x["total"].(float64)
		return fmt.Sprintf("комментариев %d (текст — jira_issue с comments=true)", max(int(total), len(cs))), true
	}
	return "", false
}

// compactLink — связь задачи (элемент issuelinks) одной строкой, как в jira_issue:
// «[id] родительская задача ERP-1 | Анализ | Тема». Полный JSON связи с типом,
// статусом и приоритетом весит ~1,5 КБ.
func compactLink(x map[string]any) (string, bool) {
	typ, ok := x["type"].(map[string]any)
	if !ok {
		return "", false
	}
	other, label := x["inwardIssue"], mstr(typ, "inward")
	if other == nil {
		other, label = x["outwardIssue"], mstr(typ, "outward")
	}
	is, ok := other.(map[string]any)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("[%s] %s %s | %s | %s", mstr(x, "id"), label, mstr(is, "key"),
		mstr(is, "fields", "status"), mstr(is, "fields", "summary")), true
}

// compactJSON — JSON без служебных ссылок и аватаров, не длиннее 1000 символов.
func compactJSON(v any) string {
	b, _ := json.Marshal(stripNoise(v))
	r := []rune(string(b))
	if len(r) > 1000 {
		return string(r[:1000]) + "…[обрезано]"
	}
	return string(r)
}

func stripNoise(v any) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, e := range x {
			if k == "self" || k == "avatarUrls" || k == "iconUrl" {
				continue
			}
			m[k] = stripNoise(e)
		}
		return m
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = stripNoise(e)
		}
		return out
	}
	return v
}

func mstr(m map[string]any, path ...string) string {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[p]
	}
	return jiraVal(cur)
}

func (t *toolset) extraFields(a args) ([]string, error) {
	extra := a.list("fields")
	for _, f := range extra {
		if !reField.MatchString(f) {
			return nil, fmt.Errorf("недопустимое имя поля %q", f)
		}
	}
	return extra, nil
}

func issueKey(a args) (string, error) {
	key := strings.ToUpper(a.str("key"))
	if !reIssueKey.MatchString(key) {
		return "", fmt.Errorf("недопустимый ключ задачи %q (вида ABC-123)", key)
	}
	return key, nil
}

func (t *toolset) jget(path string, q url.Values, v any) error {
	data, err := t.jira.do("GET", path, q, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("Jira: не разобран ответ %s: %v", path, err)
	}
	return nil
}

// ---------- задача ----------

func (t *toolset) jiraIssue(a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	extra, err := t.extraFields(a)
	if err != nil {
		return "", err
	}
	fields := append(append([]string{}, jiraBaseFields...), extra...)
	var is struct {
		ID     string         `json:"id"`
		Key    string         `json:"key"`
		Fields map[string]any `json:"fields"`
	}
	if err := t.jget("/rest/api/2/issue/"+key, url.Values{"fields": {strings.Join(fields, ",")}}, &is); err != nil {
		return "", err
	}
	var b strings.Builder
	f := is.Fields
	fmt.Fprintf(&b, "%s: %s\nurl: %s/browse/%s\n", is.Key, jiraVal(f["summary"]), t.jira.base, is.Key)
	for _, k := range []string{"issuetype", "status", "resolution", "priority", "assignee", "reporter", "parent", "components", "fixVersions", "labels", "duedate"} {
		if s := jiraVal(f[k]); s != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, s)
		}
	}
	fmt.Fprintf(&b, "created: %s\nupdated: %s\n", shortDate(jiraVal(f["created"])), shortDate(jiraVal(f["updated"])))
	if r := jiraVal(f["resolutiondate"]); r != "" {
		fmt.Fprintf(&b, "resolved: %s\n", shortDate(r))
	}
	if tt, ok := f["timetracking"].(map[string]any); ok && len(tt) > 0 {
		fmt.Fprintf(&b, "оценка: %s, затрачено: %s, осталось: %s\n", mstr(tt, "originalEstimate"), mstr(tt, "timeSpent"), mstr(tt, "remainingEstimate"))
	}
	for _, k := range extra {
		fmt.Fprintf(&b, "%s: %s\n", k, jiraVal(f[k]))
	}
	if subs, ok := f["subtasks"].([]any); ok && len(subs) > 0 {
		b.WriteString("\nПодзадачи:\n")
		for _, s := range subs {
			m, _ := s.(map[string]any)
			fmt.Fprintf(&b, "- %s | %s | %s\n", mstr(m, "key"), mstr(m, "fields", "status"), mstr(m, "fields", "summary"))
		}
	}
	if links, ok := f["issuelinks"].([]any); ok && len(links) > 0 {
		b.WriteString("\nСвязи:\n")
		for _, l := range links {
			m, _ := l.(map[string]any)
			if in, ok := m["inwardIssue"].(map[string]any); ok {
				fmt.Fprintf(&b, "- [%s] %s %s | %s | %s\n", mstr(m, "id"), mstr(m, "type", "inward"), mstr(in, "key"), mstr(in, "fields", "status"), mstr(in, "fields", "summary"))
			}
			if out, ok := m["outwardIssue"].(map[string]any); ok {
				fmt.Fprintf(&b, "- [%s] %s %s | %s | %s\n", mstr(m, "id"), mstr(m, "type", "outward"), mstr(out, "key"), mstr(out, "fields", "status"), mstr(out, "fields", "summary"))
			}
		}
	}
	if atts, ok := f["attachment"].([]any); ok && len(atts) > 0 {
		b.WriteString("\nВложения:\n")
		for _, at := range atts {
			m, _ := at.(map[string]any)
			fmt.Fprintf(&b, "- %s | %s | %s байт | %s | %s\n", mstr(m, "id"), mstr(m, "filename"), mstr(m, "size"), mstr(m, "author"), shortDate(mstr(m, "created")))
		}
	}
	if d := jiraVal(f["description"]); d != "" {
		fmt.Fprintf(&b, "\nОписание:\n%s\n", d)
	}
	if a.boolean("comments") {
		var cl struct {
			Total    int `json:"total"`
			Comments []struct {
				ID      string         `json:"id"`
				Author  map[string]any `json:"author"`
				Created string         `json:"created"`
				Body    string         `json:"body"`
			} `json:"comments"`
		}
		if err := t.jget("/rest/api/2/issue/"+key+"/comment", url.Values{"maxResults": {"100"}}, &cl); err != nil {
			fmt.Fprintf(&b, "\nКомментарии не получены: %v\n", err)
		} else {
			fmt.Fprintf(&b, "\nКомментарии (%d):\n", cl.Total)
			for _, c := range cl.Comments {
				fmt.Fprintf(&b, "--- %s | %s, %s\n%s\n", c.ID, jiraVal(c.Author), shortDate(c.Created), c.Body)
			}
		}
	}
	return truncate(b.String(), "Запроси без comments или конкретные поля."), nil
}

func renderIssueRows(b *strings.Builder, issues []jiraIssueRow, extra []string) {
	ex := append([]string{}, extra...)
	sort.Strings(ex)
	for _, is := range issues {
		f := is.Fields
		fmt.Fprintf(b, "- %s | %s | %s | %s | %s | %s", is.Key, jiraVal(f["issuetype"]), jiraVal(f["status"]),
			jiraVal(f["assignee"]), shortDate(jiraVal(f["updated"])), jiraVal(f["summary"]))
		for _, k := range ex {
			fmt.Fprintf(b, " | %s=%s", k, jiraVal(f[k]))
		}
		b.WriteString("\n")
	}
}

type jiraIssueRow struct {
	Key    string         `json:"key"`
	Fields map[string]any `json:"fields"`
}

var listFields = []string{"summary", "status", "assignee", "issuetype", "updated"}

func (t *toolset) jiraSearch(a args) (string, error) {
	jql := a.str("jql")
	if jql == "" {
		return "", fmt.Errorf("нужен jql")
	}
	extra, err := t.extraFields(a)
	if err != nil {
		return "", err
	}
	start, _ := a.num("start_at")
	q := url.Values{"jql": {jql}, "maxResults": {strconv.Itoa(a.limit(20, 100))},
		"startAt": {strconv.Itoa(max0(start))}, "fields": {strings.Join(append(append([]string{}, listFields...), extra...), ",")}}
	var r struct {
		Total  int            `json:"total"`
		Issues []jiraIssueRow `json:"issues"`
	}
	if err := t.jget("/rest/api/2/search", q, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "JQL: %s\nНайдено: %d, показано %d с %d\n", jql, r.Total, len(r.Issues), max0(start))
	if max0(start)+len(r.Issues) < r.Total {
		fmt.Fprintf(&b, "(есть ещё — start_at=%d)\n", max0(start)+len(r.Issues))
	}
	renderIssueRows(&b, r.Issues, extra)
	return truncate(b.String(), "Сузь JQL или уменьши limit."), nil
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

// ---------- проекты, поля, метаданные ----------

func (t *toolset) jiraProjects(a args) (string, error) {
	var ps []struct {
		Key  string         `json:"key"`
		Name string         `json:"name"`
		Lead map[string]any `json:"lead"`
		Cat  map[string]any `json:"projectCategory"`
	}
	if err := t.jget("/rest/api/2/project", url.Values{"expand": {"lead"}}, &ps); err != nil {
		return "", err
	}
	filter := strings.ToLower(a.str("text"))
	var b strings.Builder
	n := 0
	for _, p := range ps {
		if filter != "" && !strings.Contains(strings.ToLower(p.Key+" "+p.Name), filter) {
			continue
		}
		n++
		fmt.Fprintf(&b, "- %s | %s | %s | %s\n", p.Key, p.Name, jiraVal(p.Lead), jiraVal(p.Cat))
	}
	return truncate(fmt.Sprintf("Проектов: %d\n", n)+b.String(), "Задай text для фильтра."), nil
}

func (t *toolset) jiraProject(a args) (string, error) {
	pk := strings.ToUpper(a.str("project"))
	if !reProject.MatchString(pk) {
		return "", fmt.Errorf("нужен project — ключ проекта")
	}
	var p struct {
		Key        string           `json:"key"`
		Name       string           `json:"name"`
		Lead       map[string]any   `json:"lead"`
		IssueTypes []map[string]any `json:"issueTypes"`
		Components []map[string]any `json:"components"`
		Versions   []map[string]any `json:"versions"`
	}
	if err := t.jget("/rest/api/2/project/"+pk, nil, &p); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s, руководитель %s\n\nТипы задач:\n", p.Key, p.Name, jiraVal(p.Lead))
	for _, it := range p.IssueTypes {
		sub := ""
		if it["subtask"] == true {
			sub = " (подзадача)"
		}
		fmt.Fprintf(&b, "- %s | %s%s\n", mstr(it, "id"), mstr(it, "name"), sub)
	}
	b.WriteString("\nКомпоненты:\n")
	for _, c := range p.Components {
		fmt.Fprintf(&b, "- %s | %s\n", mstr(c, "id"), mstr(c, "name"))
	}
	b.WriteString("\nВерсии:\n")
	for _, v := range p.Versions {
		st := "не выпущена"
		if v["released"] == true {
			st = "выпущена"
		}
		if v["archived"] == true {
			st += ", архив"
		}
		fmt.Fprintf(&b, "- %s | %s | %s | %s\n", mstr(v, "id"), mstr(v, "name"), st, mstr(v, "releaseDate"))
	}
	return truncate(b.String(), ""), nil
}

func (t *toolset) jiraFields(a args) (string, error) {
	var fs []struct {
		ID     string         `json:"id"`
		Name   string         `json:"name"`
		Custom bool           `json:"custom"`
		Schema map[string]any `json:"schema"`
	}
	if err := t.jget("/rest/api/2/field", nil, &fs); err != nil {
		return "", err
	}
	filter := strings.ToLower(a.str("text"))
	var b strings.Builder
	n := 0
	for _, f := range fs {
		if filter != "" && !strings.Contains(strings.ToLower(f.ID+" "+f.Name), filter) {
			continue
		}
		n++
		typ := mstr(f.Schema, "type")
		if it := mstr(f.Schema, "items"); it != "" {
			typ += "<" + it + ">"
		}
		if c := mstr(f.Schema, "custom"); c != "" {
			typ += " " + c[strings.LastIndex(c, ":")+1:]
		}
		fmt.Fprintf(&b, "- %s | %s | %s\n", f.ID, f.Name, typ)
	}
	return truncate(fmt.Sprintf("Полей: %d\n", n)+b.String(), "Задай text для фильтра."), nil
}

func renderMetaField(b *strings.Builder, id string, f map[string]any) {
	req := ""
	if f["required"] == true {
		req = " ОБЯЗАТЕЛЬНОЕ"
	}
	typ := mstr(f, "schema", "type")
	if it := mstr(f, "schema", "items"); it != "" {
		typ += "<" + it + ">"
	}
	if id == "" {
		id = mstr(f, "fieldId")
	}
	fmt.Fprintf(b, "- %s | %s | %s%s", id, mstr(f, "name"), typ, req)
	if av, ok := f["allowedValues"].([]any); ok && len(av) > 0 {
		vals := []string{}
		for i, v := range av {
			if i == 25 {
				vals = append(vals, fmt.Sprintf("… ещё %d", len(av)-25))
				break
			}
			m, _ := v.(map[string]any)
			s := jiraVal(m)
			if id2 := mstr(m, "id"); id2 != "" && id2 != s {
				s += " [" + id2 + "]"
			}
			vals = append(vals, s)
		}
		fmt.Fprintf(b, " | значения: %s", strings.Join(vals, "; "))
	}
	b.WriteString("\n")
}

func (t *toolset) jiraCreateMeta(a args) (string, error) {
	pk := strings.ToUpper(a.str("project"))
	if !reProject.MatchString(pk) {
		return "", fmt.Errorf("нужен project — ключ проекта")
	}
	itName := a.str("issuetype")
	var types struct {
		Values []map[string]any `json:"values"`
	}
	err := t.jget("/rest/api/2/issue/createmeta/"+pk+"/issuetypes", url.Values{"maxResults": {"100"}}, &types)
	if err != nil && strings.Contains(err.Error(), "HTTP 404") {
		return t.jiraCreateMetaLegacy(pk, itName)
	}
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if itName == "" {
		fmt.Fprintf(&b, "Типы задач проекта %s (укажи issuetype, чтобы увидеть поля):\n", pk)
		for _, it := range types.Values {
			fmt.Fprintf(&b, "- %s | %s\n", mstr(it, "id"), mstr(it, "name"))
		}
		return b.String(), nil
	}
	itID := ""
	for _, it := range types.Values {
		if strings.EqualFold(mstr(it, "name"), itName) || mstr(it, "id") == itName {
			itID = mstr(it, "id")
		}
	}
	if itID == "" {
		return "", fmt.Errorf("тип задачи %q не найден в проекте %s", itName, pk)
	}
	var fs struct {
		Values []map[string]any `json:"values"`
	}
	if err := t.jget("/rest/api/2/issue/createmeta/"+pk+"/issuetypes/"+itID, url.Values{"maxResults": {"500"}}, &fs); err != nil {
		return "", err
	}
	fmt.Fprintf(&b, "Поля для создания %s / %s:\n", pk, itName)
	sort.SliceStable(fs.Values, func(i, j int) bool { return fs.Values[i]["required"] == true && fs.Values[j]["required"] != true })
	for _, f := range fs.Values {
		renderMetaField(&b, "", f)
	}
	return truncate(b.String(), ""), nil
}

// Jira до 9.0: старый createmeta с expand.
func (t *toolset) jiraCreateMetaLegacy(pk, itName string) (string, error) {
	q := url.Values{"projectKeys": {pk}, "expand": {"projects.issuetypes.fields"}}
	if itName != "" {
		q.Set("issuetypeNames", itName)
	}
	var r struct {
		Projects []struct {
			IssueTypes []struct {
				ID     string                    `json:"id"`
				Name   string                    `json:"name"`
				Fields map[string]map[string]any `json:"fields"`
			} `json:"issuetypes"`
		} `json:"projects"`
	}
	if err := t.jget("/rest/api/2/issue/createmeta", q, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, p := range r.Projects {
		for _, it := range p.IssueTypes {
			if itName == "" {
				fmt.Fprintf(&b, "- %s | %s\n", it.ID, it.Name)
				continue
			}
			fmt.Fprintf(&b, "Поля для создания %s / %s:\n", pk, it.Name)
			ids := make([]string, 0, len(it.Fields))
			for id := range it.Fields {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool {
				ri, rj := it.Fields[ids[i]]["required"] == true, it.Fields[ids[j]]["required"] == true
				if ri != rj {
					return ri
				}
				return ids[i] < ids[j]
			})
			for _, id := range ids {
				renderMetaField(&b, id, it.Fields[id])
			}
		}
	}
	if b.Len() == 0 {
		return "", fmt.Errorf("метаданные создания для %s не найдены", pk)
	}
	return truncate(b.String(), ""), nil
}

func (t *toolset) jiraTransitions(a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	var r struct {
		Transitions []map[string]any `json:"transitions"`
	}
	if err := t.jget("/rest/api/2/issue/"+key+"/transitions", url.Values{"expand": {"transitions.fields"}}, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Переходы %s:\n", key)
	for _, tr := range r.Transitions {
		fmt.Fprintf(&b, "- %s | %s → %s", mstr(tr, "id"), mstr(tr, "name"), mstr(tr, "to", "name"))
		if fs, ok := tr["fields"].(map[string]any); ok {
			req := []string{}
			for id, f := range fs {
				if m, ok := f.(map[string]any); ok && m["required"] == true {
					req = append(req, id+" ("+mstr(m, "name")+")")
				}
			}
			sort.Strings(req)
			if len(req) > 0 {
				fmt.Fprintf(&b, " | обязательные поля: %s", strings.Join(req, ", "))
			}
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// ---------- история, время в статусах ----------

type jiraHistory struct {
	ID      string         `json:"id"`
	Author  map[string]any `json:"author"`
	Created string         `json:"created"`
	Items   []struct {
		Field      string `json:"field"`
		FromString string `json:"fromString"`
		ToString   string `json:"toString"`
	} `json:"items"`
}

type jiraChangelogIssue struct {
	Key    string         `json:"key"`
	Fields map[string]any `json:"fields"`
	Log    struct {
		Total      int           `json:"total"`
		MaxResults int           `json:"maxResults"`
		Histories  []jiraHistory `json:"histories"`
	} `json:"changelog"`
}

func (t *toolset) getChangelog(key string) (*jiraChangelogIssue, error) {
	var is jiraChangelogIssue
	q := url.Values{"expand": {"changelog"}, "fields": {"summary,status,created,resolutiondate"}}
	if err := t.jget("/rest/api/2/issue/"+key, q, &is); err != nil {
		return nil, err
	}
	sort.SliceStable(is.Log.Histories, func(i, j int) bool {
		ti, _ := parseJiraTime(is.Log.Histories[i].Created)
		tj, _ := parseJiraTime(is.Log.Histories[j].Created)
		return ti.Before(tj)
	})
	return &is, nil
}

func parseJiraTime(s string) (time.Time, error) {
	for _, l := range []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("не разобрана дата %q", s)
}

func (t *toolset) jiraChangelog(a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	is, err := t.getChangelog(key)
	if err != nil {
		return "", err
	}
	only := strings.ToLower(a.str("field"))
	var b strings.Builder
	fmt.Fprintf(&b, "История %s (%s), записей %d", key, jiraVal(is.Fields["summary"]), len(is.Log.Histories))
	if is.Log.Total > len(is.Log.Histories) {
		fmt.Fprintf(&b, " из %d — сервер отдал не все", is.Log.Total)
	}
	b.WriteString("\n")
	for _, h := range is.Log.Histories {
		for _, it := range h.Items {
			if only != "" && strings.ToLower(it.Field) != only {
				continue
			}
			fmt.Fprintf(&b, "- %s | %s | %s: %q → %q\n", shortDate(h.Created), jiraVal(h.Author), it.Field, it.FromString, it.ToString)
		}
	}
	return truncate(b.String(), "Задай field, напр. status или assignee."), nil
}

func lastStatusChange(hist []jiraHistory) time.Time {
	var last time.Time
	for _, h := range hist {
		for _, it := range h.Items {
			if it.Field == "status" {
				if at, err := parseJiraTime(h.Created); err == nil && at.After(last) {
					last = at
				}
			}
		}
	}
	return last
}

type statusSpan struct {
	Status string
	Total  time.Duration
	Visits int
}

// statusDurations — сколько задача провела в каждом статусе (календарное время).
func statusDurations(created time.Time, current string, hist []jiraHistory, now time.Time) ([]statusSpan, []string) {
	type change struct {
		at       time.Time
		from, to string
		who      string
	}
	changes := []change{}
	for _, h := range hist {
		at, err := parseJiraTime(h.Created)
		if err != nil {
			continue
		}
		for _, it := range h.Items {
			if it.Field == "status" {
				changes = append(changes, change{at, it.FromString, it.ToString, jiraVal(h.Author)})
			}
		}
	}
	status := current
	if len(changes) > 0 {
		status = changes[0].from
	}
	order := []string{}
	spans := map[string]*statusSpan{}
	add := func(st string, d time.Duration) {
		sp, ok := spans[st]
		if !ok {
			sp = &statusSpan{Status: st}
			spans[st] = sp
			order = append(order, st)
		}
		sp.Total += d
		sp.Visits++
	}
	lines := []string{}
	from := created
	for _, c := range changes {
		add(status, c.at.Sub(from))
		lines = append(lines, fmt.Sprintf("%s | %s → %s | %s", c.at.Format("2006-01-02 15:04"), c.from, c.to, c.who))
		status, from = c.to, c.at
	}
	add(status, now.Sub(from))
	out := []statusSpan{}
	for _, st := range order {
		out = append(out, *spans[st])
	}
	return out, lines
}

func fmtDur(d time.Duration) string {
	h := d.Hours()
	if h < 48 {
		return fmt.Sprintf("%.1f ч", h)
	}
	return fmt.Sprintf("%.1f дн (%.0f ч)", h/24, h)
}

func (t *toolset) jiraStatusTimes(a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	is, err := t.getChangelog(key)
	if err != nil {
		return "", err
	}
	created, err := parseJiraTime(jiraVal(is.Fields["created"]))
	if err != nil {
		return "", err
	}
	now := time.Now()
	endNote := "по сей момент"
	if r := jiraVal(is.Fields["resolutiondate"]); r != "" {
		if rt, err := parseJiraTime(r); err == nil {
			now, endNote = rt, "до решения "+rt.Format("2006-01-02 15:04")
		}
	} else if mstr(is.Fields, "status", "statusCategory", "key") == "done" {
		// даты решения нет (workflow её не ставит), статус финальный — считаем до перехода в него
		if last := lastStatusChange(is.Log.Histories); !last.IsZero() {
			now, endNote = last, "до перехода в финальный статус "+last.Format("2006-01-02 15:04")
		}
	}
	spans, lines := statusDurations(created, jiraVal(is.Fields["status"]), is.Log.Histories, now)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\nсоздана %s, сейчас «%s», всего %s (%s; календарное время)\n\nВремя в статусах:\n",
		key, jiraVal(is.Fields["summary"]), created.Format("2006-01-02 15:04"), jiraVal(is.Fields["status"]), fmtDur(now.Sub(created)), endNote)
	for _, s := range spans {
		fmt.Fprintf(&b, "- %s | %s | заходов %d\n", s.Status, fmtDur(s.Total), s.Visits)
	}
	if len(lines) > 0 {
		b.WriteString("\nПереходы:\n")
		for _, l := range lines {
			b.WriteString("- " + l + "\n")
		}
	}
	return truncate(b.String(), ""), nil
}

// ---------- трудозатраты, наблюдатели ----------

func (t *toolset) jiraWorklog(a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	var r struct {
		Total    int `json:"total"`
		Worklogs []struct {
			ID        string         `json:"id"`
			Author    map[string]any `json:"author"`
			Started   string         `json:"started"`
			TimeSpent string         `json:"timeSpent"`
			Seconds   int            `json:"timeSpentSeconds"`
			Comment   string         `json:"comment"`
		} `json:"worklogs"`
	}
	if err := t.jget("/rest/api/2/issue/"+key+"/worklog", nil, &r); err != nil {
		return "", err
	}
	byAuthor := map[string]int{}
	total := 0
	var b strings.Builder
	for _, w := range r.Worklogs {
		total += w.Seconds
		byAuthor[jiraVal(w.Author)] += w.Seconds
		fmt.Fprintf(&b, "- %s | %s | %s | %s | %s\n", w.ID, shortDate(w.Started), jiraVal(w.Author), w.TimeSpent, strings.ReplaceAll(w.Comment, "\n", " "))
	}
	head := fmt.Sprintf("Трудозатраты %s: записей %d, всего %.2f ч\n", key, len(r.Worklogs), float64(total)/3600)
	names := []string{}
	for n := range byAuthor {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		head += fmt.Sprintf("  %s: %.2f ч\n", n, float64(byAuthor[n])/3600)
	}
	return truncate(head+"\n"+b.String(), ""), nil
}

func (t *toolset) jiraWatchers(a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	var r struct {
		Count    int              `json:"watchCount"`
		Watchers []map[string]any `json:"watchers"`
	}
	if err := t.jget("/rest/api/2/issue/"+key+"/watchers", nil, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Наблюдателей у %s: %d\n", key, r.Count)
	for _, w := range r.Watchers {
		fmt.Fprintf(&b, "- %s | %s\n", mstr(w, "name"), mstr(w, "displayName"))
	}
	return b.String(), nil
}

func (t *toolset) jiraLinkTypes(a args) (string, error) {
	var r struct {
		Types []map[string]any `json:"issueLinkTypes"`
	}
	if err := t.jget("/rest/api/2/issueLinkType", nil, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Типы связей (name | outward | inward):\n")
	for _, lt := range r.Types {
		fmt.Fprintf(&b, "- %s | %s | %s\n", mstr(lt, "name"), mstr(lt, "outward"), mstr(lt, "inward"))
	}
	return b.String(), nil
}

func (t *toolset) jiraUsers(a args) (string, error) {
	qs := a.str("text")
	if qs == "" {
		return "", fmt.Errorf("нужен text — часть логина, имени или почты")
	}
	q := url.Values{"username": {qs}, "maxResults": {strconv.Itoa(a.limit(20, 100))}}
	path := "/rest/api/2/user/search"
	if pk := strings.ToUpper(a.str("project")); pk != "" {
		if !reProject.MatchString(pk) {
			return "", fmt.Errorf("недопустимый ключ проекта %q", pk)
		}
		q.Set("project", pk)
		path = "/rest/api/2/user/assignable/search"
	}
	var us []map[string]any
	if err := t.jget(path, q, &us); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Пользователей: %d (логин | имя | почта | активен)\n", len(us))
	for _, u := range us {
		fmt.Fprintf(&b, "- %s | %s | %s | %s\n", mstr(u, "name"), mstr(u, "displayName"), mstr(u, "emailAddress"), mstr(u, "active"))
	}
	return b.String(), nil
}

func (t *toolset) jiraFilters(a args) (string, error) {
	var fs []map[string]any
	if err := t.jget("/rest/api/2/filter/favourite", nil, &fs); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Избранные фильтры: %d\n", len(fs))
	for _, f := range fs {
		fmt.Fprintf(&b, "- %s | %s | %s | JQL: %s\n", mstr(f, "id"), mstr(f, "name"), mstr(f, "owner"), mstr(f, "jql"))
	}
	return b.String(), nil
}

// ---------- вложения ----------

func (t *toolset) jiraDownload(a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	want := a.str("name")
	wantID := a.str("attachment_id")
	if want == "" && wantID == "" && !a.boolean("all") {
		return "", fmt.Errorf("укажи name или attachment_id (из jira_issue) либо all=true — скачать все")
	}
	var is struct {
		Fields struct {
			Attachment []map[string]any `json:"attachment"`
		} `json:"fields"`
	}
	if err := t.jget("/rest/api/2/issue/"+key, url.Values{"fields": {"attachment"}}, &is); err != nil {
		return "", err
	}
	var b strings.Builder
	n := 0
	for _, at := range is.Fields.Attachment {
		name, id := mstr(at, "filename"), mstr(at, "id")
		if (wantID != "" && id != wantID) || (want != "" && name != want) {
			continue
		}
		p, err := t.jira.relPath(mstr(at, "content"))
		if err != nil {
			return "", err
		}
		lower := strings.ToLower(name)
		data, err := t.jira.fetchRaw(p, strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".htm"))
		if err != nil {
			fmt.Fprintf(&b, "- %s: ошибка %v\n", name, err)
			continue
		}
		saved, err := t.saveDownload(name, data)
		if err != nil {
			return "", err
		}
		n++
		fmt.Fprintf(&b, "- %s (%d байт) → %s\n", name, len(data), saved)
	}
	if n == 0 && b.Len() == 0 {
		return "", fmt.Errorf("у %s нет подходящих вложений (имя или attachment_id — из jira_issue)", key)
	}
	return fmt.Sprintf("Скачано из %s: %d\n", key, n) + b.String(), nil
}
