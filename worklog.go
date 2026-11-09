package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Трудозатраты: режим остатка оценки и правка своей записи.
//
// По умолчанию Jira уменьшает остаток оценки задачи на списанное (auto). Когда
// списывает не исполнитель (руководитель — организационные часы сверх оценки),
// это бывает и нужно, и нет, поэтому режим задаётся явно:
//   auto  — остаток пересчитывает Jira (по умолчанию);
//   leave — остаток не трогать;
//   new   — поставить остаток new_estimate.
// Правка — только своих записей: чужие часы двигать нельзя, это чужой табель.

var reWorklogID = regexp.MustCompile(`^\d{1,18}$`)

func adjustQuery(a args) (url.Values, error) {
	q := url.Values{}
	mode := strings.ToLower(a.str("adjust_estimate"))
	switch mode {
	case "", "auto":
		if a.str("new_estimate") != "" {
			return nil, fmt.Errorf("new_estimate задаётся только с adjust_estimate=new")
		}
		return q, nil
	case "leave":
		if a.str("new_estimate") != "" {
			return nil, fmt.Errorf("new_estimate задаётся только с adjust_estimate=new")
		}
		q.Set("adjustEstimate", "leave")
	case "new":
		ne := a.str("new_estimate")
		if !reTimeSpent.MatchString(ne) {
			return nil, fmt.Errorf("для adjust_estimate=new нужен new_estimate в формате Jira: 1h 30m, 2d, 0m")
		}
		q.Set("adjustEstimate", "new")
		q.Set("newEstimate", strings.TrimSpace(ne))
	default:
		return nil, fmt.Errorf("adjust_estimate: auto, leave или new")
	}
	return q, nil
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
	q, err := adjustQuery(a)
	if err != nil {
		return "", err
	}
	body := map[string]any{"timeSpent": strings.TrimSpace(ts), "started": started}
	if c := a.raw("comment"); c != "" {
		body["comment"] = c
	}
	data, err := t.jira.do("POST", "/rest/api/2/issue/"+key+"/worklog", q, body)
	if err != nil {
		return "", err
	}
	var w struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &w)
	return fmt.Sprintf("%s: списано %s (начало %s), запись %s%s", key, ts, started[:16], w.ID, adjustNote(q)), nil
}

func adjustNote(q url.Values) string {
	switch q.Get("adjustEstimate") {
	case "leave":
		return "; остаток не тронут"
	case "new":
		return "; остаток = " + q.Get("newEstimate")
	}
	return "; остаток пересчитала Jira"
}

type worklogRec struct {
	ID      string `json:"id"`
	Started string `json:"started"`
	Spent   string `json:"timeSpent"`
	Comment string `json:"comment"`
	Author  struct {
		Name        string `json:"name"`
		Key         string `json:"key"`
		DisplayName string `json:"displayName"`
	} `json:"author"`
}

func (t *toolset) jiraWorklogUpdate(a args) (string, error) {
	key, err := writableKey(t, a)
	if err != nil {
		return "", err
	}
	id := a.str("worklog_id")
	if !reWorklogID.MatchString(id) {
		return "", fmt.Errorf("нужен worklog_id — номер записи (из jira_worklog)")
	}
	body := map[string]any{}
	if ts := a.str("time_spent"); ts != "" {
		if !reTimeSpent.MatchString(ts) {
			return "", fmt.Errorf("time_spent в формате Jira: 1h 30m, 2d, 45m")
		}
		body["timeSpent"] = strings.TrimSpace(ts)
	}
	if s := a.str("started"); s != "" {
		started, err := parseStarted(s, time.Now())
		if err != nil {
			return "", err
		}
		body["started"] = started
	}
	if _, ok := a["comment"]; ok {
		body["comment"] = a.raw("comment")
	}
	q, err := adjustQuery(a)
	if err != nil {
		return "", err
	}
	if len(body) == 0 {
		return "", fmt.Errorf("нечего менять: time_spent, started или comment")
	}
	path := "/rest/api/2/issue/" + key + "/worklog/" + id
	var was worklogRec
	if err := t.jget(path, nil, &was); err != nil {
		return "", err
	}
	var me struct {
		Name string `json:"name"`
		Key  string `json:"key"`
	}
	if err := t.jget("/rest/api/2/myself", nil, &me); err != nil {
		return "", err
	}
	if !strings.EqualFold(was.Author.Name, me.Name) {
		return "", fmt.Errorf("запись %s списал %s, а не вы (%s) — чужие трудозатраты не правлю", id, was.Author.DisplayName, me.Name)
	}
	if _, err := t.jira.do("PUT", path, q, body); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s: запись %s изменена", key, id)
	if v, ok := body["timeSpent"]; ok {
		fmt.Fprintf(&b, "; время %s → %s", was.Spent, v)
	}
	if v, ok := body["started"].(string); ok {
		fmt.Fprintf(&b, "; начало %s → %s", short16(was.Started), short16(v))
	}
	if v, ok := body["comment"]; ok {
		fmt.Fprintf(&b, "; комментарий «%s» → «%s»", was.Comment, v)
	}
	b.WriteString(adjustNote(q))
	return b.String(), nil
}

func short16(s string) string {
	s = strings.Replace(s, "T", " ", 1)
	if len(s) > 16 {
		return s[:16]
	}
	return s
}
