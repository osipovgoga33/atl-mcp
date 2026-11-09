package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type confContent struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Status string `json:"status"`
	Title  string `json:"title"`
	Space  struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"space"`
	Version struct {
		Number  int    `json:"number"`
		When    string `json:"when"`
		Message string `json:"message"`
		By      struct {
			DisplayName string `json:"displayName"`
			Username    string `json:"username"`
		} `json:"by"`
	} `json:"version"`
	Body struct {
		Storage struct {
			Value string `json:"value"`
		} `json:"storage"`
	} `json:"body"`
	Ancestors []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"ancestors"`
	Metadata struct {
		Labels struct {
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		} `json:"labels"`
	} `json:"metadata"`
	Extensions struct {
		FileSize  int64  `json:"fileSize"`
		MediaType string `json:"mediaType"`
	} `json:"extensions"`
	Links struct {
		WebUI    string `json:"webui"`
		Download string `json:"download"`
	} `json:"_links"`
}

type confList struct {
	Results []confContent `json:"results"`
	Size    int           `json:"size"`
	Total   int           `json:"totalSize"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

func (t *toolset) confURL(webui string) string {
	if webui == "" {
		return ""
	}
	return t.conf.base + webui
}

func cqlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func shortDate(s string) string {
	if len(s) >= 16 {
		return strings.Replace(s[:16], "T", " ", 1)
	}
	return s
}

func (t *toolset) confSearch(a args) (string, error) {
	cql := a.str("cql")
	if cql == "" {
		parts := []string{"type=page"}
		if sp := a.str("space"); sp != "" {
			if !reSpace.MatchString(sp) {
				return "", fmt.Errorf("недопустимый ключ пространства %q", sp)
			}
			parts = append(parts, "space="+cqlQuote(sp))
		}
		if txt := a.str("text"); txt != "" {
			parts = append(parts, "(title~"+cqlQuote(txt)+" OR text~"+cqlQuote(txt)+")")
		} else if a.str("title") != "" {
			parts = append(parts, "title~"+cqlQuote(a.str("title")))
		}
		if len(parts) == 1 {
			return "", fmt.Errorf("нужен text, title, space или cql")
		}
		cql = strings.Join(parts, " AND ") + " ORDER BY lastmodified DESC"
	}
	q := url.Values{"cql": {cql}, "limit": {strconv.Itoa(a.limit(20, 100))}, "expand": {"space,version"}}
	data, err := t.conf.do("GET", "/rest/api/content/search", q, nil)
	if err != nil {
		return "", err
	}
	var l confList
	if err := json.Unmarshal(data, &l); err != nil {
		return "", fmt.Errorf("Confluence: не разобран ответ поиска: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CQL: %s\nНайдено: %d", cql, len(l.Results))
	if l.Total > 0 {
		fmt.Fprintf(&b, " из %d", l.Total)
	}
	if l.Links.Next != "" {
		b.WriteString(" (есть ещё — сузь запрос или увеличь limit)")
	}
	b.WriteString("\n")
	for _, c := range l.Results {
		fmt.Fprintf(&b, "- %s | %s | %s | v%d %s %s | %s\n", c.ID, c.Title, c.Space.Key,
			c.Version.Number, shortDate(c.Version.When), c.Version.By.DisplayName, t.confURL(c.Links.WebUI))
	}
	return b.String(), nil
}

const pageExpand = "body.storage,version,space,ancestors,metadata.labels"

func (t *toolset) getPage(id string, expand string) (*confContent, error) {
	if !reID.MatchString(id) {
		return nil, fmt.Errorf("недопустимый id страницы %q (нужны цифры)", id)
	}
	data, err := t.conf.do("GET", "/rest/api/content/"+id, url.Values{"expand": {expand}}, nil)
	if err != nil {
		return nil, err
	}
	var c confContent
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("Confluence: не разобран ответ: %v", err)
	}
	return &c, nil
}

func (t *toolset) confPage(a args) (string, error) {
	var page *confContent
	id, space, title := a.str("id"), a.str("space"), a.str("title")
	switch {
	case id != "":
		p, err := t.getPage(id, pageExpand)
		if err != nil {
			return "", err
		}
		page = p
	case space != "" && title != "":
		if !reSpace.MatchString(space) {
			return "", fmt.Errorf("недопустимый ключ пространства %q", space)
		}
		q := url.Values{"spaceKey": {space}, "title": {title}, "expand": {pageExpand}}
		data, err := t.conf.do("GET", "/rest/api/content", q, nil)
		if err != nil {
			return "", err
		}
		var l confList
		if err := json.Unmarshal(data, &l); err != nil {
			return "", fmt.Errorf("Confluence: не разобран ответ: %v", err)
		}
		if len(l.Results) == 0 {
			return "", fmt.Errorf("страница %q в пространстве %s не найдена (заголовок должен совпадать точно; для поиска — conf_search)", title, space)
		}
		page = &l.Results[0]
	default:
		return "", fmt.Errorf("нужен id или space+title")
	}
	format := a.str("format")
	if format == "" {
		format = "text"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "id: %s\nзаголовок: %s\nпространство: %s\nversion: %d (%s, %s)\n", page.ID, page.Title,
		page.Space.Key, page.Version.Number, shortDate(page.Version.When), page.Version.By.DisplayName)
	if len(page.Ancestors) > 0 {
		path := []string{}
		for _, an := range page.Ancestors {
			path = append(path, an.Title+" ("+an.ID+")")
		}
		fmt.Fprintf(&b, "путь: %s\n", strings.Join(path, " / "))
	}
	if len(page.Metadata.Labels.Results) > 0 {
		ls := []string{}
		for _, l := range page.Metadata.Labels.Results {
			ls = append(ls, l.Name)
		}
		fmt.Fprintf(&b, "метки: %s\n", strings.Join(ls, ", "))
	}
	fmt.Fprintf(&b, "url: %s\nформат: %s\n\n", t.confURL(page.Links.WebUI), format)
	switch format {
	case "storage":
		b.WriteString(page.Body.Storage.Value)
	case "text":
		b.WriteString(storageToText(t.resolveUsers(page.Body.Storage.Value)))
	default:
		return "", fmt.Errorf("format: text или storage")
	}
	return truncate(b.String(), "Страница слишком велика для одного ответа; для правки целиком её нельзя использовать — сообщи пользователю."), nil
}

func (t *toolset) confChildren(a args) (string, error) {
	id := a.str("id")
	if !reID.MatchString(id) {
		return "", fmt.Errorf("недопустимый id страницы %q", id)
	}
	q := url.Values{"limit": {strconv.Itoa(a.limit(100, 200))}, "expand": {"version"}}
	data, err := t.conf.do("GET", "/rest/api/content/"+id+"/child/page", q, nil)
	if err != nil {
		return "", err
	}
	var l confList
	if err := json.Unmarshal(data, &l); err != nil {
		return "", fmt.Errorf("Confluence: не разобран ответ: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Дочерних страниц: %d", len(l.Results))
	if l.Links.Next != "" {
		b.WriteString(" (есть ещё)")
	}
	b.WriteString("\n")
	for _, c := range l.Results {
		fmt.Fprintf(&b, "- %s | %s | v%d %s %s\n", c.ID, c.Title, c.Version.Number,
			shortDate(c.Version.When), c.Version.By.DisplayName)
	}
	return b.String(), nil
}

func (t *toolset) confSpaces(a args) (string, error) {
	q := url.Values{"limit": {strconv.Itoa(a.limit(100, 500))}}
	data, err := t.conf.do("GET", "/rest/api/space", q, nil)
	if err != nil {
		return "", err
	}
	var l struct {
		Results []struct {
			Key  string `json:"key"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"results"`
		Links struct {
			Next string `json:"next"`
		} `json:"_links"`
	}
	if err := json.Unmarshal(data, &l); err != nil {
		return "", fmt.Errorf("Confluence: не разобран ответ: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Пространств: %d", len(l.Results))
	if l.Links.Next != "" {
		b.WriteString(" (есть ещё — увеличь limit)")
	}
	b.WriteString("\n")
	for _, s := range l.Results {
		fmt.Fprintf(&b, "- %s | %s | %s\n", s.Key, s.Name, s.Type)
	}
	return b.String(), nil
}

func (t *toolset) confAttachList(id, filename string) (*confList, error) {
	q := url.Values{"limit": {"200"}, "expand": {"version"}}
	if filename != "" {
		q.Set("filename", filename)
	}
	data, err := t.conf.do("GET", "/rest/api/content/"+id+"/child/attachment", q, nil)
	if err != nil {
		return nil, err
	}
	var l confList
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("Confluence: не разобран ответ: %v", err)
	}
	return &l, nil
}

func (t *toolset) confAttachments(a args) (string, error) {
	id := a.str("id")
	if !reID.MatchString(id) {
		return "", fmt.Errorf("недопустимый id страницы %q", id)
	}
	l, err := t.confAttachList(id, "")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Вложений: %d\n", len(l.Results))
	for _, c := range l.Results {
		fmt.Fprintf(&b, "- %s | %s | %s | %d байт | v%d %s\n", c.ID, c.Title, c.Extensions.MediaType,
			c.Extensions.FileSize, c.Version.Number, shortDate(c.Version.When))
	}
	return b.String(), nil
}

// ---------- запись ----------

func storageBody(body string) map[string]any {
	return map[string]any{"storage": map[string]any{"value": body, "representation": "storage"}}
}

func (t *toolset) confCreate(a args) (string, error) {
	title, body, space, parent := a.str("title"), a.raw("body"), a.str("space"), a.str("parent_id")
	if title == "" {
		return "", fmt.Errorf("нужен title")
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("нужен body (storage XHTML)")
	}
	if parent != "" {
		p, err := t.getPage(parent, "space")
		if err != nil {
			return "", fmt.Errorf("родитель %s: %v", parent, err)
		}
		if space == "" {
			space = p.Space.Key
		} else if !strings.EqualFold(space, p.Space.Key) {
			return "", fmt.Errorf("родитель %s лежит в пространстве %s, а не %s", parent, p.Space.Key, space)
		}
	}
	if space == "" || !reSpace.MatchString(space) {
		return "", fmt.Errorf("нужен корректный space (или parent_id)")
	}
	if err := t.spaceWritable(space); err != nil {
		return "", err
	}
	labels := a.list("labels")
	for _, l := range labels {
		if !reLabel.MatchString(l) {
			return "", fmt.Errorf("недопустимая метка %q (без пробелов и спецсимволов)", l)
		}
	}
	req := map[string]any{"type": "page", "title": title, "space": map[string]any{"key": space}, "body": storageBody(body)}
	if parent != "" {
		req["ancestors"] = []map[string]any{{"id": parent}}
	}
	data, err := t.conf.do("POST", "/rest/api/content", nil, req)
	if err != nil {
		return "", err
	}
	var c confContent
	if err := json.Unmarshal(data, &c); err != nil {
		return "", fmt.Errorf("Confluence: страница, вероятно, создана, но ответ не разобран: %v", err)
	}
	out := fmt.Sprintf("Создана страница %s «%s» в %s, version %d\nurl: %s", c.ID, c.Title, space, c.Version.Number, t.confURL(c.Links.WebUI))
	if len(labels) > 0 {
		if err := t.addLabels(c.ID, labels); err != nil {
			out += "\nМетки НЕ добавлены: " + err.Error()
		} else {
			out += "\nметки: " + strings.Join(labels, ", ")
		}
	}
	return out, nil
}

func (t *toolset) addLabels(id string, labels []string) error {
	payload := []map[string]any{}
	for _, l := range labels {
		payload = append(payload, map[string]any{"prefix": "global", "name": l})
	}
	_, err := t.conf.do("POST", "/rest/api/content/"+id+"/label", nil, payload)
	return err
}

func (t *toolset) confUpdate(a args) (string, error) {
	id, body := a.str("id"), a.raw("body")
	expected, ok := a.num("version")
	if !ok || expected <= 0 {
		return "", fmt.Errorf("нужен version — номер версии, прочитанный через conf_page перед правкой")
	}
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("нужен body — полное новое тело страницы в storage")
	}
	cur, err := t.getPage(id, "body.storage,version,space")
	if err != nil {
		return "", err
	}
	if err := t.spaceWritable(cur.Space.Key); err != nil {
		return "", err
	}
	if cur.Version.Number != expected {
		return "", fmt.Errorf("страница изменилась после чтения: сейчас version %d (%s, %s), передан %d. Перечитай conf_page format=storage и слей правки",
			cur.Version.Number, shortDate(cur.Version.When), cur.Version.By.DisplayName, expected)
	}
	oldLen, newLen := len([]rune(cur.Body.Storage.Value)), len([]rune(body))
	if oldLen > 500 && newLen*2 < oldLen && !a.boolean("allow_shrink") {
		return "", fmt.Errorf("новое тело (%d симв.) более чем вдвое короче текущего (%d) — похоже на обрывок. Если сокращение намеренное, передай allow_shrink=true", newLen, oldLen)
	}
	title := a.str("title")
	if title == "" {
		title = cur.Title
	}
	ver := map[string]any{"number": cur.Version.Number + 1, "minorEdit": a.boolean("minor_edit")}
	if m := a.str("message"); m != "" {
		ver["message"] = m
	}
	req := map[string]any{"id": cur.ID, "type": "page", "title": title,
		"space": map[string]any{"key": cur.Space.Key}, "version": ver, "body": storageBody(body)}
	data, err := t.conf.do("PUT", "/rest/api/content/"+cur.ID, nil, req)
	if err != nil {
		return "", err
	}
	var c confContent
	_ = json.Unmarshal(data, &c)
	return fmt.Sprintf("Обновлена страница %s «%s»: version %d → %d\nurl: %s", cur.ID, title, cur.Version.Number, c.Version.Number, t.confURL(c.Links.WebUI)), nil
}

func (t *toolset) confLabel(a args) (string, error) {
	id := a.str("id")
	labels := a.list("labels")
	if len(labels) == 0 {
		return "", fmt.Errorf("нужен labels")
	}
	for _, l := range labels {
		if !reLabel.MatchString(l) {
			return "", fmt.Errorf("недопустимая метка %q", l)
		}
	}
	cur, err := t.getPage(id, "space")
	if err != nil {
		return "", err
	}
	if err := t.spaceWritable(cur.Space.Key); err != nil {
		return "", err
	}
	if err := t.addLabels(cur.ID, labels); err != nil {
		return "", err
	}
	return fmt.Sprintf("Странице %s добавлены метки: %s", cur.ID, strings.Join(labels, ", ")), nil
}

var reUserRef = regexp.MustCompile(`<ri:user\s+ri:(userkey|username)="([^"]+)"\s*/>`)

// resolveUsers — подставляет имена вместо ключей пользователей (для format=text).
// Не больше 30 запросов на страницу; при ошибке остаётся «@пользователь».
func (t *toolset) resolveUsers(s string) string {
	if t.userNames == nil {
		t.userNames = map[string]string{}
	}
	lookups := 0
	return reUserRef.ReplaceAllStringFunc(s, func(m string) string {
		g := reUserRef.FindStringSubmatch(m)
		kind, val := g[1], g[2]
		ck := kind + ":" + val
		name, ok := t.userNames[ck]
		if !ok && lookups < 30 {
			lookups++
			param := "key"
			if kind == "username" {
				param = "username"
			}
			var u struct {
				DisplayName string `json:"displayName"`
			}
			if data, err := t.conf.do("GET", "/rest/api/user", url.Values{param: {val}}, nil); err == nil {
				_ = json.Unmarshal(data, &u)
			}
			name = u.DisplayName
			t.userNames[ck] = name
		}
		if name == "" {
			return m
		}
		return "@" + html.EscapeString(name)
	})
}
