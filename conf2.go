package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ---------- комментарии ----------

func (t *toolset) confComments(a args) (string, error) {
	id := a.str("id")
	if !reID.MatchString(id) {
		return "", fmt.Errorf("недопустимый id страницы %q", id)
	}
	q := url.Values{"limit": {strconv.Itoa(a.limit(100, 200))}, "depth": {"all"},
		"expand": {"body.storage,version,ancestors"}}
	data, err := t.conf.do("GET", "/rest/api/content/"+id+"/child/comment", q, nil)
	if err != nil {
		return "", err
	}
	var l confList
	if err := json.Unmarshal(data, &l); err != nil {
		return "", fmt.Errorf("Confluence: не разобран ответ: %v", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Комментариев: %d", len(l.Results))
	if l.Links.Next != "" {
		b.WriteString(" (есть ещё — увеличь limit)")
	}
	b.WriteString("\n")
	for _, c := range l.Results {
		reply := ""
		if n := len(c.Ancestors); n > 0 {
			reply = " ответ на " + c.Ancestors[n-1].ID
		}
		fmt.Fprintf(&b, "--- %s%s | %s %s\n%s\n", c.ID, reply, c.Version.By.DisplayName,
			shortDate(c.Version.When), storageToText(c.Body.Storage.Value))
	}
	return truncate(b.String(), "Уменьши limit."), nil
}

func (t *toolset) confComment(a args) (string, error) {
	id, body, parent := a.str("id"), a.raw("body"), a.str("parent_comment_id")
	if strings.TrimSpace(body) == "" {
		return "", fmt.Errorf("нужен body (storage XHTML, напр. <p>текст</p>)")
	}
	if parent != "" && !reID.MatchString(parent) {
		return "", fmt.Errorf("недопустимый parent_comment_id %q", parent)
	}
	page, err := t.getPage(id, "space")
	if err != nil {
		return "", err
	}
	if err := t.spaceWritable(page.Space.Key); err != nil {
		return "", err
	}
	req := map[string]any{"type": "comment", "container": map[string]any{"id": page.ID, "type": "page"},
		"body": storageBody(body)}
	if parent != "" {
		req["ancestors"] = []map[string]any{{"id": parent}}
	}
	data, err := t.conf.do("POST", "/rest/api/content", nil, req)
	if err != nil {
		return "", err
	}
	var c confContent
	_ = json.Unmarshal(data, &c)
	what := "Комментарий"
	if parent != "" {
		what = "Ответ на " + parent
	}
	return fmt.Sprintf("%s %s добавлен к странице %s\nurl: %s", what, c.ID, page.ID, t.confURL(page.Links.WebUI)), nil
}

// ---------- перенос ----------

func (t *toolset) confMove(a args) (string, error) {
	id, parent := a.str("id"), a.str("parent_id")
	if !reID.MatchString(parent) {
		return "", fmt.Errorf("нужен parent_id — id новой родительской страницы")
	}
	if id == parent {
		return "", fmt.Errorf("страницу нельзя сделать родителем самой себя")
	}
	cur, err := t.getPage(id, "body.storage,version,space,ancestors")
	if err != nil {
		return "", err
	}
	if n := len(cur.Ancestors); n > 0 && cur.Ancestors[n-1].ID == parent {
		return fmt.Sprintf("Страница %s уже лежит под %s — ничего не изменено", cur.ID, parent), nil
	}
	np, err := t.getPage(parent, "space,ancestors")
	if err != nil {
		return "", fmt.Errorf("новый родитель %s: %v", parent, err)
	}
	if !strings.EqualFold(cur.Space.Key, np.Space.Key) {
		return "", fmt.Errorf("перенос между пространствами (%s → %s) не поддерживается — только внутри пространства", cur.Space.Key, np.Space.Key)
	}
	for _, an := range np.Ancestors {
		if an.ID == cur.ID {
			return "", fmt.Errorf("страница %s — предок %s; перенос под собственного потомка невозможен", cur.ID, parent)
		}
	}
	if err := t.spaceWritable(cur.Space.Key); err != nil {
		return "", err
	}
	if v, ok := a.num("version"); ok && v > 0 && v != cur.Version.Number {
		return "", fmt.Errorf("страница изменилась: сейчас version %d, передан %d", cur.Version.Number, v)
	}
	oldParent := "корень пространства"
	if n := len(cur.Ancestors); n > 0 {
		oldParent = cur.Ancestors[n-1].Title + " (" + cur.Ancestors[n-1].ID + ")"
	}
	ver := map[string]any{"number": cur.Version.Number + 1, "minorEdit": true, "message": "Перенос под " + np.Title}
	req := map[string]any{"id": cur.ID, "type": "page", "title": cur.Title,
		"space": map[string]any{"key": cur.Space.Key}, "version": ver,
		"ancestors": []map[string]any{{"id": parent}}, "body": storageBody(cur.Body.Storage.Value)}
	if _, err := t.conf.do("PUT", "/rest/api/content/"+cur.ID, nil, req); err != nil {
		return "", err
	}
	return fmt.Sprintf("Страница %s «%s» перенесена: %s → %s (%s), version %d → %d",
		cur.ID, cur.Title, oldParent, np.Title, np.ID, cur.Version.Number, cur.Version.Number+1), nil
}

// ---------- вложения ----------

const maxAttach = 50 << 20

// Разрешённые расширения: защита от выгрузки произвольных локальных файлов
// (ключи, конфиги) по чужой подсказке.
var attachExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true, ".bmp": true,
	".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true, ".doc": true, ".xls": true,
	".txt": true, ".csv": true, ".md": true, ".json": true, ".xml": true, ".html": true,
	".drawio": true, ".puml": true, ".bpmn": true, ".vsdx": true, ".abap": true, ".sql": true, ".zip": true,
}

// upload — содержимое вложения из аргументов: ровно один источник, проверенное имя.
type upload struct {
	name, ctype string
	data        []byte
}

func loadUpload(a args) (*upload, error) {
	path, name := a.str("path"), a.str("name")
	content, b64 := a.raw("content"), a.str("content_base64")
	sources := 0
	for _, s := range []string{path, content, b64} {
		if s != "" {
			sources++
		}
	}
	if sources != 1 {
		return nil, fmt.Errorf("нужен ровно один источник: path (файл на этом компьютере), content (текст, напр. SVG) или content_base64")
	}
	if name == "" {
		if path == "" {
			return nil, fmt.Errorf("для content/content_base64 нужен name — имя файла с расширением")
		}
		name = filepath.Base(path)
	}
	if !reFileName.MatchString(name) {
		return nil, fmt.Errorf("недопустимое имя файла %q", name)
	}
	ext := strings.ToLower(filepath.Ext(name))
	if !attachExt[ext] {
		return nil, fmt.Errorf("расширение %q не разрешено для вложений", ext)
	}
	var data []byte
	switch {
	case path != "":
		if pe := strings.ToLower(filepath.Ext(path)); !attachExt[pe] {
			return nil, fmt.Errorf("файл %q: расширение %q не разрешено для вложений", path, pe)
		}
		st, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("файл не найден: %v", err)
		}
		if st.IsDir() || st.Size() > maxAttach {
			return nil, fmt.Errorf("файл %q — каталог или больше %d МБ", path, maxAttach>>20)
		}
		if data, err = os.ReadFile(path); err != nil {
			return nil, err
		}
	case content != "":
		data = []byte(content)
	default:
		var err error
		if data, err = base64.StdEncoding.DecodeString(b64); err != nil {
			return nil, fmt.Errorf("content_base64 не декодируется: %v", err)
		}
	}
	if len(data) == 0 || len(data) > maxAttach {
		return nil, fmt.Errorf("пустое вложение или больше %d МБ", maxAttach>>20)
	}
	ctype := mime.TypeByExtension(ext)
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	return &upload{name: name, ctype: ctype, data: data}, nil
}

// multipartBody — тело multipart с полем file и доп. полями.
func multipartBody(u *upload, fields map[string]string) (string, []byte) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	h := make(textproto.MIMEHeader)
	// Имя — как шлют браузеры: UTF-8 в кавычках. RFC 2231 (filename*=) Jira/Confluence Server
	// разбирают не всегда, и кириллическое имя портится.
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(u.name)
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+q+`"`)
	h.Set("Content-Type", u.ctype)
	fw, _ := mw.CreatePart(h)
	_, _ = fw.Write(u.data)
	for k, v := range fields {
		if v != "" {
			_ = mw.WriteField(k, v)
		}
	}
	_ = mw.Close()
	return mw.FormDataContentType(), body.Bytes()
}

func (t *toolset) confAttach(a args) (string, error) {
	id := a.str("id")
	u, err := loadUpload(a)
	if err != nil {
		return "", err
	}
	page, err := t.getPage(id, "space")
	if err != nil {
		return "", err
	}
	if err := t.spaceWritable(page.Space.Key); err != nil {
		return "", err
	}
	// есть ли вложение с таким именем — тогда новая версия, а не ошибка
	q := url.Values{"filename": {u.name}, "expand": {"version"}}
	ex, err := t.conf.do("GET", "/rest/api/content/"+page.ID+"/child/attachment", q, nil)
	if err != nil {
		return "", err
	}
	var exl confList
	_ = json.Unmarshal(ex, &exl)
	target := "/rest/api/content/" + page.ID + "/child/attachment"
	action := "добавлено"
	for _, c := range exl.Results {
		if c.Title == u.name {
			target += "/" + c.ID + "/data"
			action = fmt.Sprintf("обновлено (было v%d)", c.Version.Number)
		}
	}
	ct, body := multipartBody(u, map[string]string{"comment": a.str("message"), "minorEdit": "true"})
	if _, err := t.conf.send("POST", target, nil, ct, body); err != nil {
		return "", err
	}
	embed := fmt.Sprintf(`<ac:link><ri:attachment ri:filename="%s"/></ac:link>`, xmlAttr(u.name))
	if strings.HasPrefix(u.ctype, "image/") {
		embed = fmt.Sprintf(`<ac:image><ri:attachment ri:filename="%s"/></ac:image>`, xmlAttr(u.name))
	}
	return fmt.Sprintf("Вложение «%s» (%d байт) %s на странице %s.\nВставка в тело страницы (storage): %s",
		u.name, len(u.data), action, page.ID, embed), nil
}

func xmlAttr(s string) string {
	r := strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}
