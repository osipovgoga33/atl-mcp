package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ---------- Agile ----------

func (t *toolset) jiraBoards(a args) (string, error) {
	q := url.Values{"maxResults": {strconv.Itoa(a.limit(50, 100))}}
	if n := a.str("text"); n != "" {
		q.Set("name", n)
	}
	if pk := strings.ToUpper(a.str("project")); pk != "" {
		if !reProject.MatchString(pk) {
			return "", fmt.Errorf("недопустимый ключ проекта %q", pk)
		}
		q.Set("projectKeyOrId", pk)
	}
	if ty := a.str("type"); ty != "" {
		if ty != "scrum" && ty != "kanban" {
			return "", fmt.Errorf("type: scrum или kanban")
		}
		q.Set("type", ty)
	}
	var r struct {
		Values []map[string]any `json:"values"`
		IsLast bool             `json:"isLast"`
	}
	if err := t.jget("/rest/agile/1.0/board", q, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Досок: %d", len(r.Values))
	if !r.IsLast {
		b.WriteString(" (есть ещё — уточни text/project)")
	}
	b.WriteString("\n")
	for _, v := range r.Values {
		fmt.Fprintf(&b, "- %s | %s | %s\n", mstr(v, "id"), mstr(v, "name"), mstr(v, "type"))
	}
	return b.String(), nil
}

func idArg(a args, k string) (string, error) {
	v := a.str(k)
	if !reID.MatchString(v) {
		return "", fmt.Errorf("нужен %s — число", k)
	}
	return v, nil
}

func (t *toolset) jiraSprints(a args) (string, error) {
	board, err := idArg(a, "board_id")
	if err != nil {
		return "", err
	}
	q := url.Values{"maxResults": {strconv.Itoa(a.limit(50, 100))}}
	if st := a.str("state"); st != "" {
		for _, s := range strings.Split(st, ",") {
			if s = strings.TrimSpace(s); s != "active" && s != "future" && s != "closed" {
				return "", fmt.Errorf("state: active, future, closed (через запятую)")
			}
		}
		q.Set("state", st)
	}
	var r struct {
		Values []map[string]any `json:"values"`
		IsLast bool             `json:"isLast"`
	}
	if err := t.jget("/rest/agile/1.0/board/"+board+"/sprint", q, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Спринтов: %d", len(r.Values))
	if !r.IsLast {
		b.WriteString(" (есть ещё)")
	}
	b.WriteString("\n")
	for _, v := range r.Values {
		fmt.Fprintf(&b, "- %s | %s | %s | %s — %s | %s\n", mstr(v, "id"), mstr(v, "name"), mstr(v, "state"),
			shortDate(mstr(v, "startDate")), shortDate(mstr(v, "endDate")), mstr(v, "goal"))
	}
	return b.String(), nil
}

func (t *toolset) agileIssues(path string, a args) (string, error) {
	extra, err := t.extraFields(a)
	if err != nil {
		return "", err
	}
	start, _ := a.num("start_at")
	q := url.Values{"maxResults": {strconv.Itoa(a.limit(50, 100))}, "startAt": {strconv.Itoa(max0(start))},
		"fields": {strings.Join(append(append([]string{}, listFields...), extra...), ",")}}
	if j := a.str("jql"); j != "" {
		q.Set("jql", j)
	}
	var r struct {
		Total  int            `json:"total"`
		Issues []jiraIssueRow `json:"issues"`
	}
	if err := t.jget(path, q, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Задач: %d, показано %d с %d\n", r.Total, len(r.Issues), max0(start))
	if max0(start)+len(r.Issues) < r.Total {
		fmt.Fprintf(&b, "(есть ещё — start_at=%d)\n", max0(start)+len(r.Issues))
	}
	renderIssueRows(&b, r.Issues, extra)
	return truncate(b.String(), ""), nil
}

func (t *toolset) jiraSprintIssues(a args) (string, error) {
	id, err := idArg(a, "sprint_id")
	if err != nil {
		return "", err
	}
	return t.agileIssues("/rest/agile/1.0/sprint/"+id+"/issue", a)
}

func (t *toolset) jiraBoardIssues(a args) (string, error) {
	id, err := idArg(a, "board_id")
	if err != nil {
		return "", err
	}
	return t.agileIssues("/rest/agile/1.0/board/"+id+"/issue", a)
}

// ---------- Dev Info (Bitbucket/GitLab через dev-status) ----------

func (t *toolset) jiraDevInfo(a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	var is struct {
		ID string `json:"id"`
	}
	if err := t.jget("/rest/api/2/issue/"+key, url.Values{"fields": {"summary"}}, &is); err != nil {
		return "", err
	}
	var sum struct {
		Summary map[string]struct {
			Overall map[string]any            `json:"overall"`
			ByType  map[string]map[string]any `json:"byInstanceType"`
		} `json:"summary"`
	}
	if err := t.jget("/rest/dev-status/1.0/issue/summary", url.Values{"issueId": {is.ID}}, &sum); err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return "", fmt.Errorf("dev-status недоступен: у этой Jira нет интеграции с Bitbucket/GitLab")
		}
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Разработка по %s:\n", key)
	found := false
	for _, dt := range []string{"repository", "branch", "pullrequest"} {
		s, ok := sum.Summary[dt]
		if !ok {
			continue
		}
		for app := range s.ByType {
			var det struct {
				Detail []struct {
					Repositories []map[string]any `json:"repositories"`
					Branches     []map[string]any `json:"branches"`
					PullRequests []map[string]any `json:"pullRequests"`
				} `json:"detail"`
			}
			q := url.Values{"issueId": {is.ID}, "applicationType": {app}, "dataType": {dt}}
			if err := t.jget("/rest/dev-status/1.0/issue/detail", q, &det); err != nil {
				fmt.Fprintf(&b, "%s/%s: %v\n", app, dt, err)
				continue
			}
			for _, d := range det.Detail {
				for _, r := range d.Repositories {
					found = true
					fmt.Fprintf(&b, "\nРепозиторий %s (%s)\n", mstr(r, "name"), mstr(r, "url"))
					if cs, ok := r["commits"].([]any); ok {
						for _, c := range cs {
							m, _ := c.(map[string]any)
							msg := strings.SplitN(mstr(m, "message"), "\n", 2)[0]
							fmt.Fprintf(&b, "- %s | %s | %s | %s\n", mstr(m, "displayId"), shortDate(mstr(m, "authorTimestamp")), mstr(m, "author", "name"), msg)
						}
					}
				}
				for _, br := range d.Branches {
					found = true
					fmt.Fprintf(&b, "- ветка %s | %s\n", mstr(br, "name"), mstr(br, "repository", "name"))
				}
				for _, pr := range d.PullRequests {
					found = true
					fmt.Fprintf(&b, "- PR %s | %s | %s | %s | %s\n", mstr(pr, "id"), mstr(pr, "status"), mstr(pr, "author", "name"), mstr(pr, "name"), mstr(pr, "url"))
				}
			}
		}
	}
	if !found {
		b.WriteString("Связанных коммитов, веток и PR нет.\n")
	}
	return truncate(b.String(), ""), nil
}

// ---------- Service Desk ----------

func (t *toolset) jsmDesks(a args) (string, error) {
	var r struct {
		Values []map[string]any `json:"values"`
	}
	if err := t.jget("/rest/servicedeskapi/servicedesk", url.Values{"limit": {"100"}}, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Сервис-деск: %d (id | проект | название)\n", len(r.Values))
	for _, v := range r.Values {
		fmt.Fprintf(&b, "- %s | %s | %s\n", mstr(v, "id"), mstr(v, "projectKey"), mstr(v, "projectName"))
	}
	return b.String(), nil
}

func (t *toolset) jsmQueues(a args) (string, error) {
	desk, err := idArg(a, "desk_id")
	if err != nil {
		return "", err
	}
	var r struct {
		Values []map[string]any `json:"values"`
	}
	if err := t.jget("/rest/servicedeskapi/servicedesk/"+desk+"/queue", url.Values{"includeCount": {"true"}, "limit": {"100"}}, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Очередей: %d (id | название | задач | JQL)\n", len(r.Values))
	for _, v := range r.Values {
		fmt.Fprintf(&b, "- %s | %s | %s | %s\n", mstr(v, "id"), mstr(v, "name"), mstr(v, "issueCount"), mstr(v, "jql"))
	}
	return b.String(), nil
}

func (t *toolset) jsmQueueIssues(a args) (string, error) {
	desk, err := idArg(a, "desk_id")
	if err != nil {
		return "", err
	}
	queue, err := idArg(a, "queue_id")
	if err != nil {
		return "", err
	}
	start, _ := a.num("start_at")
	var r struct {
		Size   int            `json:"size"`
		IsLast bool           `json:"isLastPage"`
		Values []jiraIssueRow `json:"values"`
	}
	q := url.Values{"limit": {strconv.Itoa(a.limit(50, 100))}, "start": {strconv.Itoa(max0(start))}}
	if err := t.jget("/rest/servicedeskapi/servicedesk/"+desk+"/queue/"+queue+"/issue", q, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Задач в очереди: %d с %d", len(r.Values), max0(start))
	if !r.IsLast {
		fmt.Fprintf(&b, " (есть ещё — start_at=%d)", max0(start)+len(r.Values))
	}
	b.WriteString("\n")
	renderIssueRows(&b, r.Values, nil)
	return truncate(b.String(), ""), nil
}

func (t *toolset) jsmSLA(a args) (string, error) {
	key, err := issueKey(a)
	if err != nil {
		return "", err
	}
	var r struct {
		Values []map[string]any `json:"values"`
	}
	if err := t.jget("/rest/servicedeskapi/request/"+key+"/sla", nil, &r); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SLA %s:\n", key)
	for _, v := range r.Values {
		fmt.Fprintf(&b, "- %s\n", mstr(v, "name"))
		if oc, ok := v["ongoingCycle"].(map[string]any); ok {
			fmt.Fprintf(&b, "  текущий цикл: цель %s, осталось %s, нарушен=%s, на паузе=%s\n",
				mstr(oc, "goalDuration", "friendly"), mstr(oc, "remainingTime", "friendly"), mstr(oc, "breached"), mstr(oc, "paused"))
		}
		if cc, ok := v["completedCycles"].([]any); ok {
			for _, c := range cc {
				m, _ := c.(map[string]any)
				fmt.Fprintf(&b, "  завершён: затрачено %s из %s, нарушен=%s\n", mstr(m, "elapsedTime", "friendly"), mstr(m, "goalDuration", "friendly"), mstr(m, "breached"))
			}
		}
	}
	return b.String(), nil
}
