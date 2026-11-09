package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	reTimeSpent = regexp.MustCompile(`^(\s*\d+(\.\d+)?\s*[wdhm])+\s*$`)
	reDate      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reURL       = regexp.MustCompile(`^https?://[^\s]+$`)
)

// jiraWritable — белый список проектов для записи (ATL_JIRA_WRITE_PROJECTS).
func (t *toolset) jiraWritable(keyOrProject string) error {
	p := strings.ToUpper(keyOrProject)
	if i := strings.LastIndex(p, "-"); i > 0 && reIssueKey.MatchString(p) {
		p = p[:i]
	}
	if len(t.cfg.JiraWrite) == 0 || t.cfg.JiraWrite[p] {
		return nil
	}
	keys := []string{}
	for k := range t.cfg.JiraWrite {
		keys = append(keys, k)
	}
	return fmt.Errorf("запись в проект %s запрещена настройкой ATL_JIRA_WRITE_PROJECTS (разрешены: %s)", p, strings.Join(keys, ", "))
}

func writableKey(t *toolset, a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	return key, t.jiraWritable(key)
}

func objArg(a args, k string) (map[string]any, error) {
	v, ok := a[k]
	if !ok || v == nil {
		return map[string]any{}, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil, fmt.Errorf("%s: нужен JSON-объект: %v", k, err)
		}
		return m, nil
	}
	return nil, fmt.Errorf("%s: нужен объект", k)
}

func userRef(u string) (any, error) {
	switch strings.ToLower(u) {
	case "none", "null", "-":
		return nil, nil
	}
	if !reJiraUser.MatchString(u) {
		return nil, fmt.Errorf("недопустимый логин %q", u)
	}
	return map[string]any{"name": u}, nil
}

// issueFields — поля из удобных параметров плюс fields как есть (кастомные поля).
func issueFields(a args) (map[string]any, error) {
	fields, err := objArg(a, "fields")
	if err != nil {
		return nil, err
	}
	if s := a.str("summary"); s != "" {
		fields["summary"] = s
	}
	if _, ok := a["description"]; ok {
		fields["description"] = a.raw("description")
	}
	if p := a.str("priority"); p != "" {
		fields["priority"] = map[string]any{"name": p}
	}
	if u := a.str("assignee"); u != "" {
		ref, err := userRef(u)
		if err != nil {
			return nil, err
		}
		fields["assignee"] = ref
	}
	if d := a.str("due_date"); d != "" {
		if !reDate.MatchString(d) {
			return nil, fmt.Errorf("due_date: ГГГГ-ММ-ДД")
		}
		fields["duedate"] = d
	}
	if ls, ok := a["labels"]; ok && ls != nil {
		fields["labels"] = a.list("labels")
	}
	if cs := a.list("components"); len(cs) > 0 {
		arr := []map[string]any{}
		for _, c := range cs {
			arr = append(arr, map[string]any{"name": c})
		}
		fields["components"] = arr
	}
	if vs := a.list("fix_versions"); len(vs) > 0 {
		arr := []map[string]any{}
		for _, v := range vs {
			arr = append(arr, map[string]any{"name": v})
		}
		fields["fixVersions"] = arr
	}
	return fields, nil
}

func (t *toolset) jiraCreate(a args) (string, error) {
	pk := strings.ToUpper(a.str("project"))
	if !reProject.MatchString(pk) {
		return "", fmt.Errorf("нужен project — ключ проекта")
	}
	if err := t.jiraWritable(pk); err != nil {
		return "", err
	}
	it := a.str("issuetype")
	if it == "" {
		return "", fmt.Errorf("нужен issuetype — имя или id типа (список: jira_create_meta)")
	}
	if a.str("summary") == "" {
		return "", fmt.Errorf("нужен summary")
	}
	fields, err := issueFields(a)
	if err != nil {
		return "", err
	}
	fields["project"] = map[string]any{"key": pk}
	if reID.MatchString(it) {
		fields["issuetype"] = map[string]any{"id": it}
	} else {
		fields["issuetype"] = map[string]any{"name": it}
	}
	if p := strings.ToUpper(a.str("parent")); p != "" {
		if !reIssueKey.MatchString(p) {
			return "", fmt.Errorf("недопустимый parent %q", p)
		}
		fields["parent"] = map[string]any{"key": p}
	}
	data, err := t.jira.do("POST", "/rest/api/2/issue", nil, map[string]any{"fields": fields})
	if err != nil {
		return "", err
	}
	var r struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(data, &r)
	return fmt.Sprintf("Создана задача %s\nurl: %s/browse/%s", r.Key, t.jira.base, r.Key), nil
}

func (t *toolset) jiraUpdate(a args) (string, error) {
	key, err := writableKey(t, a)
	if err != nil {
		return "", err
	}
	fields, err := issueFields(a)
	if err != nil {
		return "", err
	}
	update, err := objArg(a, "update")
	if err != nil {
		return "", err
	}
	if len(fields) == 0 && len(update) == 0 {
		return "", fmt.Errorf("нечего менять: задай summary/description/… или fields/update")
	}
	body := map[string]any{}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	if len(update) > 0 {
		body["update"] = update
	}
	if _, err := t.jira.do("PUT", "/rest/api/2/issue/"+key, nil, body); err != nil {
		return "", err
	}
	names := []string{}
	for k := range fields {
		names = append(names, k)
	}
	for k := range update {
		names = append(names, k)
	}
	return fmt.Sprintf("Задача %s обновлена: %s", key, strings.Join(names, ", ")), nil
}

func (t *toolset) jiraTransition(a args) (string, error) {
	key, err := writableKey(t, a)
	if err != nil {
		return "", err
	}
	want := a.str("transition")
	if want == "" {
		return "", fmt.Errorf("нужен transition — id, название перехода или целевой статус (список: jira_transitions)")
	}
	m, err := t.findTransition(key, want)
	if err != nil {
		return "", err
	}
	if m.ID == "" {
		return "", fmt.Errorf("переход %q недоступен для %s. Доступны: %s", want, key, strings.Join(m.Avail, "; "))
	}
	body, err := transitionBody(a, m.ID)
	if err != nil {
		return "", err
	}
	if _, err := t.jira.do("POST", "/rest/api/2/issue/"+key+"/transitions", nil, body); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: выполнен переход «%s» → %s", key, m.Name, m.To), nil
}

func (t *toolset) jiraAssign(a args) (string, error) {
	key, err := writableKey(t, a)
	if err != nil {
		return "", err
	}
	u := a.str("user")
	if u == "" {
		return "", fmt.Errorf("нужен user — логин (или none — снять исполнителя)")
	}
	ref, err := userRef(u)
	if err != nil {
		return "", err
	}
	body := map[string]any{"name": nil}
	if ref != nil {
		body["name"] = u
	}
	if _, err := t.jira.do("PUT", "/rest/api/2/issue/"+key+"/assignee", nil, body); err != nil {
		return "", err
	}
	if ref == nil {
		return key + ": исполнитель снят", nil
	}
	return fmt.Sprintf("%s: исполнитель %s", key, u), nil
}

func (t *toolset) jiraComment(a args) (string, error) {
	key, err := writableKey(t, a)
	if err != nil {
		return "", err
	}
	body := a.raw("body")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("нужен body")
	}
	if cid := a.str("comment_id"); cid != "" {
		if !reID.MatchString(cid) {
			return "", fmt.Errorf("недопустимый comment_id %q", cid)
		}
		if _, err := t.jira.do("PUT", "/rest/api/2/issue/"+key+"/comment/"+cid, nil, map[string]any{"body": body}); err != nil {
			return "", err
		}
		return fmt.Sprintf("Комментарий %s у %s изменён", cid, key), nil
	}
	data, err := t.jira.do("POST", "/rest/api/2/issue/"+key+"/comment", nil, map[string]any{"body": body})
	if err != nil {
		return "", err
	}
	var c struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &c)
	return fmt.Sprintf("Комментарий %s добавлен к %s\nurl: %s/browse/%s", c.ID, key, t.jira.base, key), nil
}

// parseStarted — дата начала работы в формат Jira; без зоны — локальное время.
func parseStarted(s string, now time.Time) (string, error) {
	const jf = "2006-01-02T15:04:05.000-0700"
	if s == "" {
		return now.Format(jf), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format(jf), nil
	}
	for _, l := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, s, now.Location()); err == nil {
			if l == "2006-01-02" {
				t = t.Add(9 * time.Hour)
			}
			return t.Format(jf), nil
		}
	}
	return "", fmt.Errorf("started: ГГГГ-ММ-ДД, ГГГГ-ММ-ДД ЧЧ:ММ или RFC3339")
}

func (t *toolset) jiraWorklogAdd(a args) (string, error) {
	key, err := writableKey(t, a)
	if err != nil {
		return "", err
	}
	ts := a.str("time_spent")
	if !reTimeSpent.MatchString(ts) {
		return "", fmt.Errorf("time_spent в формате Jira: 1h 30m, 2d, 45m")
	}
	started, err := parseStarted(a.str("started"), time.Now())
	if err != nil {
		return "", err
	}
	body := map[string]any{"timeSpent": strings.TrimSpace(ts), "started": started}
	if c := a.raw("comment"); c != "" {
		body["comment"] = c
	}
	data, err := t.jira.do("POST", "/rest/api/2/issue/"+key+"/worklog", nil, body)
	if err != nil {
		return "", err
	}
	var w struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &w)
	return fmt.Sprintf("%s: списано %s (начало %s), запись %s", key, ts, started[:16], w.ID), nil
}

func (t *toolset) jiraLink(a args) (string, error) {
	in, out := strings.ToUpper(a.str("inward_key")), strings.ToUpper(a.str("outward_key"))
	if !reIssueKey.MatchString(in) || !reIssueKey.MatchString(out) {
		return "", fmt.Errorf("нужны inward_key и outward_key (ключи задач)")
	}
	lt := a.str("link_type")
	if lt == "" {
		return "", fmt.Errorf("нужен link_type — имя типа связи (список: jira_link_types)")
	}
	if err := t.jiraWritable(in); err != nil {
		return "", err
	}
	if err := t.jiraWritable(out); err != nil {
		return "", err
	}
	body := map[string]any{"type": map[string]any{"name": lt}, "inwardIssue": map[string]any{"key": in}, "outwardIssue": map[string]any{"key": out}}
	if c := a.raw("comment"); c != "" {
		body["comment"] = map[string]any{"body": c}
	}
	if _, err := t.jira.do("POST", "/rest/api/2/issueLink", nil, body); err != nil {
		return "", err
	}
	return fmt.Sprintf("Связь «%s»: %s → %s создана", lt, out, in), nil
}

func (t *toolset) jiraLinkRemove(a args) (string, error) {
	id, err := idArg(a, "link_id")
	if err != nil {
		return "", err
	}
	var l struct {
		In  map[string]any `json:"inwardIssue"`
		Out map[string]any `json:"outwardIssue"`
		Typ map[string]any `json:"type"`
	}
	if err := t.jget("/rest/api/2/issueLink/"+id, nil, &l); err != nil {
		return "", err
	}
	for _, k := range []string{mstr(l.In, "key"), mstr(l.Out, "key")} {
		if err := t.jiraWritable(k); err != nil {
			return "", err
		}
	}
	if _, err := t.jira.do("DELETE", "/rest/api/2/issueLink/"+id, nil, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("Связь %s «%s» %s — %s удалена", id, mstr(l.Typ, "name"), mstr(l.Out, "key"), mstr(l.In, "key")), nil
}

func (t *toolset) jiraRemoteLink(a args) (string, error) {
	key, err := writableKey(t, a)
	if err != nil {
		return "", err
	}
	u, title := a.str("url"), a.str("title")
	if !reURL.MatchString(u) {
		return "", fmt.Errorf("нужен url (http/https)")
	}
	if title == "" {
		title = u
	}
	obj := map[string]any{"url": u, "title": title}
	if s := a.str("summary"); s != "" {
		obj["summary"] = s
	}
	if _, err := t.jira.do("POST", "/rest/api/2/issue/"+key+"/remotelink", nil, map[string]any{"object": obj}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: добавлена ссылка «%s» → %s", key, title, u), nil
}

func (t *toolset) jiraAttach(a args) (string, error) {
	key, err := writableKey(t, a)
	if err != nil {
		return "", err
	}
	u, err := loadUpload(a)
	if err != nil {
		return "", err
	}
	ct, body := multipartBody(u, nil)
	if _, err := t.jira.send("POST", "/rest/api/2/issue/"+key+"/attachments", nil, ct, body); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: вложение «%s» (%d байт) добавлено", key, u.name, len(u.data)), nil
}

func (t *toolset) jiraWatcher(a args, add bool) (string, error) {
	key, err := writableKey(t, a)
	if err != nil {
		return "", err
	}
	u := a.str("user")
	if !reJiraUser.MatchString(u) {
		return "", fmt.Errorf("нужен user — логин")
	}
	if add {
		raw, _ := json.Marshal(u)
		if _, err := t.jira.send("POST", "/rest/api/2/issue/"+key+"/watchers", nil, "application/json", raw); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: %s добавлен в наблюдатели", key, u), nil
	}
	q := map[string][]string{"username": {u}}
	if _, err := t.jira.do("DELETE", "/rest/api/2/issue/"+key+"/watchers", q, nil); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s: %s убран из наблюдателей", key, u), nil
}

func keysArg(t *toolset, a args) ([]string, error) {
	keys := a.list("keys")
	if len(keys) == 0 {
		return nil, fmt.Errorf("нужен keys — список ключей задач")
	}
	if len(keys) > 50 {
		return nil, fmt.Errorf("не больше 50 задач за раз")
	}
	for i, k := range keys {
		k = strings.ToUpper(k)
		if !reIssueKey.MatchString(k) {
			return nil, fmt.Errorf("недопустимый ключ %q", k)
		}
		if err := t.jiraWritable(k); err != nil {
			return nil, err
		}
		keys[i] = k
	}
	return keys, nil
}

func (t *toolset) jiraSprintAdd(a args) (string, error) {
	id, err := idArg(a, "sprint_id")
	if err != nil {
		return "", err
	}
	keys, err := keysArg(t, a)
	if err != nil {
		return "", err
	}
	if _, err := t.jira.do("POST", "/rest/agile/1.0/sprint/"+id+"/issue", nil, map[string]any{"issues": keys}); err != nil {
		return "", err
	}
	return fmt.Sprintf("В спринт %s перенесены: %s", id, strings.Join(keys, ", ")), nil
}

func (t *toolset) jiraBacklog(a args) (string, error) {
	keys, err := keysArg(t, a)
	if err != nil {
		return "", err
	}
	if _, err := t.jira.do("POST", "/rest/agile/1.0/backlog/issue", nil, map[string]any{"issues": keys}); err != nil {
		return "", err
	}
	return "В бэклог перенесены: " + strings.Join(keys, ", "), nil
}

func (t *toolset) jiraSprintCreate(a args) (string, error) {
	board, err := idArg(a, "board_id")
	if err != nil {
		return "", err
	}
	name := a.str("name")
	if name == "" {
		return "", fmt.Errorf("нужен name")
	}
	body := map[string]any{"name": name, "originBoardId": board}
	for k, f := range map[string]string{"start_date": "startDate", "end_date": "endDate"} {
		if v := a.str(k); v != "" {
			s, err := parseStarted(v, time.Now())
			if err != nil {
				return "", fmt.Errorf("%s: %v", k, err)
			}
			body[f] = s
		}
	}
	if g := a.str("goal"); g != "" {
		body["goal"] = g
	}
	data, err := t.jira.do("POST", "/rest/agile/1.0/sprint", nil, body)
	if err != nil {
		return "", err
	}
	var r map[string]any
	_ = json.Unmarshal(data, &r)
	return fmt.Sprintf("Создан спринт %s «%s» на доске %s", mstr(r, "id"), name, board), nil
}

func (t *toolset) jiraVersionCreate(a args) (string, error) {
	pk := strings.ToUpper(a.str("project"))
	if !reProject.MatchString(pk) {
		return "", fmt.Errorf("нужен project")
	}
	if err := t.jiraWritable(pk); err != nil {
		return "", err
	}
	name := a.str("name")
	if name == "" {
		return "", fmt.Errorf("нужен name")
	}
	body := map[string]any{"project": pk, "name": name}
	for k, f := range map[string]string{"start_date": "startDate", "release_date": "releaseDate"} {
		if v := a.str(k); v != "" {
			if !reDate.MatchString(v) {
				return "", fmt.Errorf("%s: ГГГГ-ММ-ДД", k)
			}
			body[f] = v
		}
	}
	if d := a.str("description"); d != "" {
		body["description"] = d
	}
	data, err := t.jira.do("POST", "/rest/api/2/version", nil, body)
	if err != nil {
		return "", err
	}
	var r map[string]any
	_ = json.Unmarshal(data, &r)
	return fmt.Sprintf("Создана версия %s «%s» в %s", mstr(r, "id"), name, pk), nil
}
