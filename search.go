// Поиск каналов: по названию (русскому и из плейлиста) и по номеру.
package main

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// searchNorm — строка для сравнения: строчные буквы, «ё» как «е», без знаков препинания.
func searchNorm(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "ё", "е")
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
		} else if !space && b.Len() > 0 {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}

// searchScore: 0 — номер, 1 — название начинается так, 2 — слово начинается так, 3 — встречается внутри.
func searchScore(c Channel, q string) int {
	if strconv.Itoa(c.N) == q {
		return 0
	}
	best := -1
	for _, name := range []string{c.Name, c.Orig} {
		n := searchNorm(name)
		if n == "" {
			continue
		}
		sc := -1
		switch {
		case strings.HasPrefix(n, q):
			sc = 1
		case strings.Contains(" "+n, " "+q):
			sc = 2
		case strings.Contains(strings.ReplaceAll(n, " ", ""), strings.ReplaceAll(q, " ", "")):
			sc = 3
		}
		if sc >= 0 && (best < 0 || sc < best) {
			best = sc
		}
	}
	return best
}

func searchChannels(list []Channel, query string, limit int) []Channel {
	q := searchNorm(query)
	if q == "" {
		return nil
	}
	type hit struct {
		c  Channel
		sc int
	}
	var hits []hit
	for _, c := range list {
		if sc := searchScore(c, q); sc >= 0 {
			hits = append(hits, hit{c, sc})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].sc < hits[j].sc })
	out := make([]Channel, 0, min(limit, len(hits)))
	for i := 0; i < len(hits) && i < limit; i++ {
		out = append(out, hits[i].c)
	}
	return out
}
