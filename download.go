package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var reBadFileChars = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

func (t *toolset) downloadDir() (string, error) {
	dir := t.cfg.DownloadDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, "Downloads", "atl-mcp")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("папка загрузок %s: %v", dir, err)
	}
	return dir, nil
}

// saveDownload — сохраняет в папку загрузок, не перезаписывая: имя (1).ext и т.д.
func (t *toolset) saveDownload(name string, data []byte) (string, error) {
	dir, err := t.downloadDir()
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(reBadFileChars.ReplaceAllString(filepath.Base(name), "_"))
	if name == "" || name == "." || name == ".." {
		name = "attachment"
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	path := filepath.Join(dir, name)
	for i := 1; ; i++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			_, werr := f.Write(data)
			cerr := f.Close()
			if werr != nil {
				return "", werr
			}
			return path, cerr
		}
		if !os.IsExist(err) {
			return "", err
		}
		if i > 999 {
			return "", fmt.Errorf("не удалось подобрать свободное имя для %s", name)
		}
		path = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
	}
}

// relPath — путь относительно корня сервера; токен не уходит на чужой хост.
func (c *client) relPath(link string) (string, error) {
	if strings.HasPrefix(link, "/") && !strings.HasPrefix(link, "//") {
		return link, nil
	}
	if strings.HasPrefix(link, c.base+"/") {
		return strings.TrimPrefix(link, c.base), nil
	}
	return "", fmt.Errorf("ссылка %q ведёт не на %s — скачивание с чужого адреса запрещено", link, c.base)
}

func (t *toolset) confDownload(a args) (string, error) {
	id, name := a.str("id"), a.str("name")
	if !reID.MatchString(id) {
		return "", fmt.Errorf("недопустимый id страницы %q", id)
	}
	if name == "" {
		return "", fmt.Errorf("нужен name — имя вложения (список: conf_attachments)")
	}
	l, err := t.confAttachList(id, name)
	if err != nil {
		return "", err
	}
	for _, c := range l.Results {
		if c.Title == name {
			p, err := t.conf.relPath(c.Links.Download)
			if err != nil {
				return "", err
			}
			data, err := t.conf.fetchRaw(p, strings.HasSuffix(strings.ToLower(name), ".html") || strings.HasSuffix(strings.ToLower(name), ".htm"))
			if err != nil {
				return "", err
			}
			saved, err := t.saveDownload(name, data)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Скачано «%s» (%d байт) → %s", name, len(data), saved), nil
		}
	}
	return "", fmt.Errorf("на странице %s нет вложения %q", id, name)
}
