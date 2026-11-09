package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Пакетные действия Jira: одно изменение на список задач (keys) или на выборку
// JQL. Одна задача за запрос, ошибка по одной задаче не останавливает остальные.
// dry_run=true — только показать, что изменится, без единого не-GET запроса.
// Бюджет времени: клиент ждёт ответ около минуты, поэтому после bulkBudget
// действие останавливается и возвращает, что не успело, — повтор того же
// вызова пропустит уже сделанное.

const bulkMax = 50

type bulkIssue struct {
	Key    string
	Fields map[string]any
}

// bulkTargets — задачи для пакетного действия. Ровно одно из keys | jql.
// Все проверки (формат ключей, потолок, белый список проектов) — до записи.
func (t *toolset) bulkTargets(a args, fields []string) ([]bulkIssue, error) {
	keys := a.list("keys")
	jql := a.str("jql")
	switch {
	case len(keys) > 0 && jql != "":
		return nil, fmt.Errorf("задай что-то одно: keys или jql")
	case len(keys) == 0 && jql == "":
		return nil, fmt.Errorf("нужен keys (список ключей) или jql")
	}
	if len(keys) > 0 {
		if len(keys) > bulkMax {
			return nil, fmt.Errorf("не больше %d задач за раз, передано %d", bulkMax, len(keys))
		}
		seenKey := map[string]bool{}
		uniq := []string{}
		for _, k := range keys {
			k = strings.ToUpper(k)
			if !reIssueKey.MatchString(k) {
				return nil, fmt.Errorf("недопустимый ключ %q", k)
			}
			if err := t.jiraWritable(k); err != nil {
				return nil, err
			}
			if !seenKey[k] {
				seenKey[k] = true
				uniq = append(uniq, k)
			}
		}
		keys = uniq
		jql = "key in (" + strings.Join(keys, ",") + ")"
	}
	all := append([]string{"summary", "status", "assignee"}, fields...)
	q := url.Values{"jql": {jql}, "maxResults": {strconv.Itoa(bulkMax + 1)}, "fields": {strings.Join(all, ",")}}
	var r struct {
		Total  int            `json:"total"`
		Issues []jiraIssueRow `json:"issues"`
	}
	if err := t.jget("/rest/api/2/search", q, &r); err != nil {
		return nil, err
	}
	if r.Total > bulkMax {
		return nil, fmt.Errorf("под выборку попадает %d задач, за раз можно не больше %d — сузь JQL или разбей на части", r.Total, bulkMax)
	}
	if len(r.Issues) == 0 {
		return nil, fmt.Errorf("задач не найдено (JQL: %s)", jql)
	}
	byKey := map[string]bulkIssue{}
	for _, is := range r.Issues {
		if err := t.jiraWritable(is.Key); err != nil {
			return nil, err
		}
		byKey[is.Key] = bulkIssue{Key: is.Key, Fields: is.Fields}
	}
	out := []bulkIssue{}
	if len(keys) > 0 {
		missing := []string{}
		for _, k := range keys {
			if is, ok := byKey[k]; ok {
				out = append(out, is)
			} else {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("не найдены или нет прав на просмотр: %s — ничего не изменено", strings.Join(missing, ", "))
		}
		return out, nil
	}
	for _, is := range r.Issues {
		out = append(out, byKey[is.Key])
	}
	return out, nil
}

// overBudget — пора ли остановиться: хотя бы одна задача уже обработана.
func (t *toolset) overBudget(start time.Time, i int) bool {
	return i > 0 && time.Since(start) > t.bulkBudget
}

func notDone(b *strings.Builder, issues []bulkIssue) {
	keys := []string{}
	for _, is := range issues {
		keys = append(keys, is.Key)
	}
	fmt.Fprintf(b, "НЕ УСПЕЛ за отведённое время, задач: %d — %s. Повтори тот же вызов: сделанное пропустится.\n", len(keys), strings.Join(keys, ", "))
}

func shortTitle(is bulkIssue) string {
	s := []rune(mstr(is.Fields, "summary"))
	if len(s) > 60 {
		return string(s[:60]) + "…"
	}
	return string(s)
}

func showVal(v any) string {
	if s := jiraVal(v); s != "" {
		return s
	}
	return "—"
}

func (t *toolset) jiraBulkUpdate(a args) (string, error) {
	fields, err := issueFields(a)
	if err != nil {
		return "", err
	}
	update, err := objArg(a, "update")
	if err != nil {
		return "", err
	}
	if len(fields) == 0 && len(update) == 0 {
		return "", fmt.Errorf("нечего менять: задай due_date/priority/assignee/… или fields/update")
	}
	names := []string{}
	for k := range fields {
		if !reField.MatchString(k) {
			return "", fmt.Errorf("недопустимое имя поля %q", k)
		}
		names = append(names, k)
	}
	for k := range update {
		if !reField.MatchString(k) {
			return "", fmt.Errorf("недопустимое имя поля %q", k)
		}
	}
	start := time.Now()
	issues, err := t.bulkTargets(a, names)
	if err != nil {
		return "", err
	}
	dry := a.boolean("dry_run")
	body := map[string]any{}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	if len(update) > 0 {
		body["update"] = update
	}
	var b strings.Builder
	if dry {
		fmt.Fprintf(&b, "ПРЕДПРОСМОТР, ничего не изменено. Задач: %d\n", len(issues))
	}
	done, same, failed, rest := 0, 0, []string{}, 0
	for i, is := range issues {
		if !dry && t.overBudget(start, i) {
			rest = len(issues) - i
			notDone(&b, issues[i:])
			break
		}
		diff := []string{}
		changed := len(update) > 0
		for _, k := range names {
			was, now := showVal(is.Fields[k]), showVal(fields[k])
			if was != now {
				changed = true
			}
			diff = append(diff, fmt.Sprintf("%s: %s → %s", k, was, now))
		}
		if len(update) > 0 {
			raw, _ := json.Marshal(update)
			diff = append(diff, "update "+string(raw))
		}
		line := fmt.Sprintf("- %s | %s | %s | %s", is.Key, mstr(is.Fields, "status"), shortTitle(is), strings.Join(diff, "; "))
		if !changed {
			same++
			fmt.Fprintf(&b, "%s | уже так, пропущено\n", line)
			continue
		}
		if dry {
			fmt.Fprintln(&b, line)
			continue
		}
		if _, err := t.jira.do("PUT", "/rest/api/2/issue/"+is.Key, nil, body); err != nil {
			failed = append(failed, is.Key)
			fmt.Fprintf(&b, "%s | ОШИБКА: %v\n", line, err)
			continue
		}
		done++
		fmt.Fprintf(&b, "%s | ок\n", line)
	}
	if dry {
		fmt.Fprintf(&b, "Будет изменено: %d, уже как надо: %d. Для выполнения — тот же вызов без dry_run.", len(issues)-same, same)
	} else {
		fmt.Fprintf(&b, "Итого: изменено %d, уже как надо %d, ошибок %d", done, same, len(failed))
		if len(failed) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(failed, ", "))
		}
		if rest > 0 {
			fmt.Fprintf(&b, ", не успел %d", rest)
		}
	}
	return truncate(b.String(), "Разбей список на части."), nil
}

type transitionMatch struct {
	ID, Name, To string
	Avail        []string
}

// findTransition — переход по id, названию или целевому статусу.
func (t *toolset) findTransition(key, want string) (transitionMatch, error) {
	var r struct {
		Transitions []map[string]any `json:"transitions"`
	}
	if err := t.jget("/rest/api/2/issue/"+key+"/transitions", nil, &r); err != nil {
		return transitionMatch{}, err
	}
	m := transitionMatch{}
	for _, tr := range r.Transitions {
		m.Avail = append(m.Avail, fmt.Sprintf("%s «%s» → %s", mstr(tr, "id"), mstr(tr, "name"), mstr(tr, "to", "name")))
	}
	for _, pass := range []func(map[string]any) bool{
		func(x map[string]any) bool { return mstr(x, "id") == want },
		func(x map[string]any) bool { return strings.EqualFold(mstr(x, "name"), want) },
		func(x map[string]any) bool { return strings.EqualFold(mstr(x, "to", "name"), want) },
	} {
		for _, tr := range r.Transitions {
			if pass(tr) {
				m.ID, m.Name, m.To = mstr(tr, "id"), mstr(tr, "name"), mstr(tr, "to", "name")
				return m, nil
			}
		}
	}
	return m, nil
}

// transitionBody — тело перехода: поля, резолюция, комментарий.
func transitionBody(a args, id string) (map[string]any, error) {
	body := map[string]any{"transition": map[string]any{"id": id}}
	fields, err := objArg(a, "fields")
	if err != nil {
		return nil, err
	}
	if res := a.str("resolution"); res != "" {
		fields["resolution"] = map[string]any{"name": res}
	}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	if c := a.raw("comment"); strings.TrimSpace(c) != "" {
		body["update"] = map[string]any{"comment": []any{map[string]any{"add": map[string]any{"body": c}}}}
	}
	return body, nil
}

func (t *toolset) jiraBulkTransition(a args) (string, error) {
	want := a.str("transition")
	if want == "" {
		return "", fmt.Errorf("нужен transition — id, название перехода или целевой статус")
	}
	if _, err := transitionBody(a, "0"); err != nil {
		return "", err
	}
	start := time.Now()
	issues, err := t.bulkTargets(a, nil)
	if err != nil {
		return "", err
	}
	dry := a.boolean("dry_run")
	var b strings.Builder
	if dry {
		fmt.Fprintf(&b, "ПРЕДПРОСМОТР, ничего не изменено. Задач: %d\n", len(issues))
	}
	done, same, failed, rest := 0, 0, []string{}, 0
	for i, is := range issues {
		if t.overBudget(start, i) {
			rest = len(issues) - i
			notDone(&b, issues[i:])
			break
		}
		status := mstr(is.Fields, "status")
		head := fmt.Sprintf("- %s | %s | %s", is.Key, status, shortTitle(is))
		if strings.EqualFold(status, want) {
			same++
			fmt.Fprintf(&b, "%s | уже в этом статусе, пропущено\n", head)
			continue
		}
		m, err := t.findTransition(is.Key, want)
		if err != nil {
			failed = append(failed, is.Key)
			fmt.Fprintf(&b, "%s | ОШИБКА: %v\n", head, err)
			continue
		}
		if m.ID == "" {
			failed = append(failed, is.Key)
			fmt.Fprintf(&b, "%s | переход %q недоступен. Доступны: %s\n", head, want, strings.Join(m.Avail, "; "))
			continue
		}
		if dry {
			fmt.Fprintf(&b, "%s | «%s» → %s\n", head, m.Name, m.To)
			continue
		}
		body, _ := transitionBody(a, m.ID)
		if _, err := t.jira.do("POST", "/rest/api/2/issue/"+is.Key+"/transitions", nil, body); err != nil {
			failed = append(failed, is.Key)
			fmt.Fprintf(&b, "%s | ОШИБКА: %v\n", head, err)
			continue
		}
		done++
		fmt.Fprintf(&b, "%s | «%s» → %s, ок\n", head, m.Name, m.To)
	}
	if dry {
		fmt.Fprintf(&b, "Переход доступен: %d, уже в статусе: %d, недоступен: %d", len(issues)-same-len(failed)-rest, same, len(failed))
		if rest > 0 {
			fmt.Fprintf(&b, ", не проверено %d", rest)
		}
		b.WriteString(". Для выполнения — тот же вызов без dry_run.")
	} else {
		fmt.Fprintf(&b, "Итого: переведено %d, уже в статусе %d, не переведено %d", done, same, len(failed))
		if len(failed) > 0 {
			fmt.Fprintf(&b, " (%s)", strings.Join(failed, ", "))
		}
		if rest > 0 {
			fmt.Fprintf(&b, ", не успел %d", rest)
		}
	}
	return truncate(b.String(), "Разбей список на части."), nil
}
