package main

import (
	"html"
	"regexp"
	"strings"
)

var (
	reCDATA     = regexp.MustCompile(`(?s)<!\[CDATA\[(.*?)\]\]>`)
	reStatus    = regexp.MustCompile(`(?s)<ac:structured-macro[^>]*ac:name="status"[^>]*>(.*?)</ac:structured-macro>`)
	reTitleParm = regexp.MustCompile(`(?s)<ac:parameter[^>]*ac:name="title"[^>]*>(.*?)</ac:parameter>`)
	reParam     = regexp.MustCompile(`(?s)<ac:parameter[^>]*>.*?</ac:parameter>`)
	reTable     = regexp.MustCompile(`(?s)<table[^>]*>(.*?)</table>`)
	reRow       = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	reCellFull  = regexp.MustCompile(`(?s)<t[dh][^>]*>(.*?)</t[dh]>`)
	reMacroName = regexp.MustCompile(`<ac:structured-macro[^>]*ac:name="([^"]+)"[^>]*>`)
	reBlockEnd  = regexp.MustCompile(`(?i)</(p|h[1-6]|li|tr|div|pre|blockquote|ac:structured-macro|table)>|<br\s*/?>`)
	reHeading   = regexp.MustCompile(`(?i)<h([1-6])[^>]*>`)
	reLi        = regexp.MustCompile(`(?i)<li[^>]*>`)
	reLink      = regexp.MustCompile(`<ri:page[^>]*ri:content-title="([^"]*)"[^>]*/?>`)
	reUser      = regexp.MustCompile(`<ri:user[^>]*/?>`)
	reTag       = regexp.MustCompile(`(?s)<[^>]+>`)
	reBlankRuns = regexp.MustCompile(`\n[ \t]*\n(\s*\n)+`)
	reSpaces    = regexp.MustCompile(`[ \t]+`)
)

// storageToText — компактное чтение storage-формата для обзора.
// Для правки страницы нужен format=storage, не это.
func storageToText(s string) string {
	s = reCDATA.ReplaceAllStringFunc(s, func(m string) string {
		inner := reCDATA.FindStringSubmatch(m)[1]
		return "\n```\n" + html.EscapeString(inner) + "\n```\n"
	})
	// статус-макрос: [статус: работает], а не «Greenработает»
	s = reStatus.ReplaceAllStringFunc(s, func(m string) string {
		if t := reTitleParm.FindStringSubmatch(m); t != nil {
			return "[статус: " + strings.TrimSpace(t[1]) + "]"
		}
		return "[статус]"
	})
	s = reParam.ReplaceAllString(s, "")
	// таблица: строка таблицы — одна строка текста, ячейки через |
	s = reTable.ReplaceAllStringFunc(s, func(m string) string {
		var b strings.Builder
		b.WriteString("\n")
		for _, row := range reRow.FindAllStringSubmatch(m, -1) {
			cells := []string{}
			for _, c := range reCellFull.FindAllStringSubmatch(row[1], -1) {
				cells = append(cells, strings.Join(strings.Fields(blockText(c[1])), " "))
			}
			if len(cells) > 0 {
				b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
			}
		}
		// результат пойдёт через общую очистку ещё раз — экранируем обратно
		return html.EscapeString(b.String())
	})
	return blockText(s)
}

func blockText(s string) string {
	s = reMacroName.ReplaceAllString(s, "\n[макрос $1] ")
	s = reHeading.ReplaceAllStringFunc(s, func(m string) string {
		n := reHeading.FindStringSubmatch(m)[1]
		return "\n" + strings.Repeat("#", int(n[0]-'0')) + " "
	})
	s = reLi.ReplaceAllString(s, "\n- ")
	s = reLink.ReplaceAllString(s, "[[$1]]")
	s = reUser.ReplaceAllString(s, "@пользователь")
	s = reBlockEnd.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	s = reSpaces.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	s = strings.Join(lines, "\n")
	s = reBlankRuns.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
