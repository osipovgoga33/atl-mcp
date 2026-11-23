package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// conf_patch - правка страницы фрагментами: сервер сам читает текущее тело и
// версию, применяет замены и пишет version+1. Полное тело через чат не ходит -
// нужно для больших страниц (реестры на сотни КБ), где conf_update непрактичен.

type patchOp struct {
	Old, New   string
	Occurrence int // с 1; 0 - фрагмент обязан быть уникальным
}

type patchApplied struct {
	N          int
	Pos        int // в символах, в теле на момент операции
	Count      int // сколько вхождений было
	OldLen     int
	NewLen     int
	Before     string // контекст до правки
	After      string // контекст после правки
	Occurrence int
}

const (
	maxPatchOps    = 50
	patchContext   = 150 // символов вокруг замены в dryRun
	patchShowLimit = 600 // длиннее - фрагмент в контексте сокращается
)

func parsePatchOps(a args) ([]patchOp, error) {
	raw, ok := a["operations"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("нужен operations: [{oldString, newString, occurrence}]")
	}
	if s, isStr := raw.(string); isStr { // клиент прислал массив строкой JSON
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return nil, fmt.Errorf("operations: ожидается массив объектов {oldString, newString, occurrence}")
		}
		raw = v
	}
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf("operations: ожидается непустой массив объектов {oldString, newString, occurrence}")
	}
	if len(list) > maxPatchOps {
		return nil, fmt.Errorf("operations: не больше %d операций за вызов, передано %d", maxPatchOps, len(list))
	}
	ops := make([]patchOp, 0, len(list))
	for i, x := range list {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("операция %d: ожидается объект {oldString, newString, occurrence}", i+1)
		}
		for k := range m {
			switch k {
			case "oldString", "newString", "occurrence":
			default:
				return nil, fmt.Errorf("операция %d: неизвестное поле %q (есть oldString, newString, occurrence)", i+1, k)
			}
		}
		op := patchOp{}
		oldS, ok1 := m["oldString"].(string)
		newS, ok2 := m["newString"].(string)
		if !ok1 || oldS == "" {
			return nil, fmt.Errorf("операция %d: нужен непустой oldString - точный фрагмент storage", i+1)
		}
		if !ok2 {
			if _, present := m["newString"]; present {
				return nil, fmt.Errorf("операция %d: newString должен быть строкой", i+1)
			}
			return nil, fmt.Errorf("операция %d: нужен newString (пустая строка - удалить фрагмент)", i+1)
		}
		if oldS == newS {
			return nil, fmt.Errorf("операция %d: newString совпадает с oldString - правка ничего не меняет", i+1)
		}
		op.Old, op.New = oldS, newS
		if v, present := m["occurrence"]; present && v != nil {
			n, ok := v.(float64)
			if !ok || n != float64(int(n)) || n < 1 {
				return nil, fmt.Errorf("операция %d: occurrence - целое с 1", i+1)
			}
			op.Occurrence = int(n)
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// indexAll - байтовые позиции всех непересекающихся вхождений.
func indexAll(s, sub string) []int {
	var out []int
	for from := 0; ; {
		i := strings.Index(s[from:], sub)
		if i < 0 {
			return out
		}
		out = append(out, from+i)
		from += i + len(sub)
	}
}

// shorten - длинный фрагмент показывается началом и концом.
func shorten(s string) string {
	r := []rune(s)
	if len(r) <= patchShowLimit {
		return s
	}
	h := patchShowLimit / 2
	return string(r[:h]) + fmt.Sprintf(" …[ещё %d симв.]… ", len(r)-2*h) + string(r[len(r)-h:])
}

// tailRunes / headRunes - до n символов с конца / с начала, не разрезая UTF-8.
func tailRunes(s string, n int) string {
	i := len(s)
	for k := 0; k < n && i > 0; k++ {
		_, sz := utf8.DecodeLastRuneInString(s[:i])
		i -= sz
	}
	return s[i:]
}

func headRunes(s string, n int) string {
	i := 0
	for k := 0; k < n && i < len(s); k++ {
		_, sz := utf8.DecodeRuneInString(s[i:])
		i += sz
	}
	return s[:i]
}

// applyPatch - все операции по порядку, каждая над результатом предыдущей.
// Любая неудача - ошибка, тело не возвращается: запись только целиком.
func applyPatch(body string, ops []patchOp) (string, []patchApplied, error) {
	applied := make([]patchApplied, 0, len(ops))
	for i, op := range ops {
		hits := indexAll(body, op.Old)
		switch {
		case len(hits) == 0:
			return "", nil, fmt.Errorf("операция %d: oldString не найден в текущем теле (%d симв.). Фрагмент сравнивается с storage как есть, с пробелами и сущностями; сверь через atl conf_page format=storage find=…",
				i+1, utf8.RuneCountInString(body))
		case op.Occurrence == 0 && len(hits) > 1:
			return "", nil, fmt.Errorf("операция %d: oldString встречается %d раз - расширь фрагмент до уникального или укажи occurrence (с 1)", i+1, len(hits))
		case op.Occurrence > len(hits):
			return "", nil, fmt.Errorf("операция %d: occurrence %d, а вхождений %d", i+1, op.Occurrence, len(hits))
		}
		k := 0
		if op.Occurrence > 0 {
			k = op.Occurrence - 1
		}
		p := hits[k]
		pre, post := body[:p], body[p+len(op.Old):]
		ctxBefore, ctxAfter := tailRunes(pre, patchContext), headRunes(post, patchContext)
		applied = append(applied, patchApplied{
			N: i + 1, Pos: utf8.RuneCountInString(pre), Count: len(hits), Occurrence: op.Occurrence,
			OldLen: utf8.RuneCountInString(op.Old), NewLen: utf8.RuneCountInString(op.New),
			Before: ctxBefore + "⟦" + shorten(op.Old) + "⟧" + ctxAfter,
			After:  ctxBefore + "⟦" + shorten(op.New) + "⟧" + ctxAfter,
		})
		body = pre + op.New + post
	}
	return body, applied, nil
}

func (t *toolset) confPatch(a args) (string, error) {
	id := a.str("id")
	if !reID.MatchString(id) {
		return "", fmt.Errorf("нужен id страницы (цифры)")
	}
	ops, err := parsePatchOps(a)
	if err != nil {
		return "", err
	}
	dry := a.boolean("dryRun") || a.boolean("dry_run")
	const attempts = 2 // при конфликте версии - перечитать и повторить один раз
	var lastConflict error
	for try := 1; try <= attempts; try++ {
		cur, err := t.getPage(id, "body.storage,version,space")
		if err != nil {
			return "", err
		}
		if err := t.spaceWritable(cur.Space.Key); err != nil {
			return "", err
		}
		oldBody := cur.Body.Storage.Value
		newBody, applied, err := applyPatch(oldBody, ops)
		if err != nil {
			if try > 1 {
				return "", fmt.Errorf("после конфликта версии страница перечитана (version %d), но правка к ней не применилась - ничего не записано. %v", cur.Version.Number, err)
			}
			return "", fmt.Errorf("ничего не записано. %v", err)
		}
		oldLen, newLen := utf8.RuneCountInString(oldBody), utf8.RuneCountInString(newBody)
		if oldLen > 500 && newLen*2 < oldLen && !a.boolean("allow_shrink") {
			return "", fmt.Errorf("после правки тело (%d симв.) более чем вдвое короче текущего (%d) - ничего не записано. Если так и задумано, передай allow_shrink=true", newLen, oldLen)
		}
		if dry {
			return patchReport(cur, applied, oldLen, newLen, true, 0, try), nil
		}
		ver := map[string]any{"number": cur.Version.Number + 1, "minorEdit": a.boolean("minor_edit")}
		if m := a.str("message"); m != "" {
			ver["message"] = m
		}
		req := map[string]any{"id": cur.ID, "type": "page", "title": cur.Title,
			"space": map[string]any{"key": cur.Space.Key}, "version": ver, "body": storageBody(newBody)}
		data, err := t.conf.do("PUT", "/rest/api/content/"+cur.ID, nil, req)
		if err != nil {
			var he *httpErr
			if errors.As(err, &he) && he.Code == 409 {
				lastConflict = err
				continue
			}
			return "", err
		}
		var c confContent
		_ = json.Unmarshal(data, &c)
		got := c.Version.Number
		if got == 0 {
			got = cur.Version.Number + 1
		}
		out := patchReport(cur, applied, oldLen, newLen, false, got, try)
		if c.Links.WebUI != "" {
			out += "\nurl: " + t.confURL(c.Links.WebUI)
		}
		return out, nil
	}
	return "", fmt.Errorf("страницу дважды изменили между чтением и записью - ничего не записано, повтори позже. %v", lastConflict)
}

func patchReport(cur *confContent, applied []patchApplied, oldLen, newLen int, dry bool, newVer, try int) string {
	var b strings.Builder
	if dry {
		fmt.Fprintf(&b, "Сухой прогон, ничего не записано. Страница %s «%s», version %d.\n", cur.ID, cur.Title, cur.Version.Number)
		fmt.Fprintf(&b, "Применились бы все операции: %d; тело %d -> %d симв.\n", len(applied), oldLen, newLen)
	} else {
		fmt.Fprintf(&b, "Обновлена страница %s «%s»: version %d -> %d, применено операций: %d; тело %d -> %d симв.\n",
			cur.ID, cur.Title, cur.Version.Number, newVer, len(applied), oldLen, newLen)
		if try > 1 {
			b.WriteString("Был конфликт версии: страница перечитана, правка применена повторно.\n")
		}
	}
	for _, p := range applied {
		occ := "уникальный"
		if p.Occurrence > 0 {
			occ = fmt.Sprintf("вхождение %d из %d", p.Occurrence, p.Count)
		}
		fmt.Fprintf(&b, "%d) символ %d, %s: %d -> %d симв.\n", p.N, p.Pos, occ, p.OldLen, p.NewLen)
		if dry {
			fmt.Fprintf(&b, "   было:  %s\n   стало: %s\n", p.Before, p.After)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
