// Каналы 18+ в TVHub не показываются: категории «18+», «XXX», «Для взрослых» и похожие
// из любого плейлиста просто убираются из списка.
package main

import "regexp"

var adultGroupRe = regexp.MustCompile(`(?i)(^|[^a-zа-я0-9])(xxx|adult|18\s*\+|\+\s*18|для взрослых|эрот|erotic|porn|порно)`)

func isAdultGroup(g string) bool { return adultGroupRe.MatchString(" " + g) }

// withoutAdult убирает из списка каналы 18+.
func withoutAdult(list []Channel) []Channel {
	out := make([]Channel, 0, len(list))
	for _, c := range list {
		if !isAdultGroup(c.Group) {
			out = append(out, c)
		}
	}
	return out
}
