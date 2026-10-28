// Русские названия телеканалов: словарь известных каналов + обратная транслитерация.
package main

import (
	"regexp"
	"strings"
	"unicode"
)

// Ключи — нормализованные латинские названия (см. nameKey).
var ruChannelNames = map[string]string{
	// Федеральные
	"perviy kanal": "Первый канал", "pervyy kanal": "Первый канал", "pervyi kanal": "Первый канал", "perviy": "Первый канал",
	"channel one": "Первый канал", "channel one russia": "Первый канал", "1tv": "Первый канал", "first channel": "Первый канал",
	"rossiya 1": "Россия 1", "russia 1": "Россия 1", "rossia 1": "Россия 1", "rossiya1": "Россия 1",
	"rossiya 24": "Россия 24", "russia 24": "Россия 24", "rossiya k": "Россия-Культура", "rossiya kultura": "Россия-Культура",
	"kultura": "Россия-Культура", "russia k": "Россия-Культура",
	"ntv": "НТВ", "ntv mir": "НТВ Мир", "ntv pravo": "НТВ Право", "ntv serial": "НТВ Сериал", "ntv stil": "НТВ Стиль",
	"5 kanal": "Пятый канал", "5 channel": "Пятый канал", "pyatyy kanal": "Пятый канал", "pyatiy kanal": "Пятый канал", "channel 5": "Пятый канал",
	"ren tv": "РЕН ТВ", "rentv": "РЕН ТВ", "sts": "СТС", "sts love": "СТС Love", "sts kids": "СТС Kids",
	"tnt": "ТНТ", "tnt4": "ТНТ4", "tnt 4": "ТНТ4", "tnt music": "ТНТ Music",
	"tv3": "ТВ-3", "tv 3": "ТВ-3", "tv-3": "ТВ-3", "pyatnitsa": "Пятница", "pyatnica": "Пятница", "friday": "Пятница",
	"zvezda": "Звезда", "zvezda plus": "Звезда Плюс", "mir": "Мир", "mir 24": "Мир 24", "mir24": "Мир 24", "otr": "ОТР",
	"tv tsentr": "ТВ Центр", "tv centr": "ТВ Центр", "tvc": "ТВ Центр", "tv center": "ТВ Центр", "tvts": "ТВ Центр",
	"domashniy": "Домашний", "domashnij": "Домашний", "domashny": "Домашний",
	"match tv": "Матч ТВ", "match": "Матч ТВ", "match strana": "Матч! Страна", "match arena": "Матч! Арена", "match igra": "Матч! Игра",
	"match futbol 1": "Матч! Футбол 1", "match futbol 2": "Матч! Футбол 2", "match futbol 3": "Матч! Футбол 3", "match premier": "Матч Премьер",
	"match boets": "Матч! Боец", "match planeta": "Матч! Планета",
	"karusel": "Карусель", "carousel": "Карусель", "muz tv": "Муз-ТВ", "muztv": "Муз-ТВ", "muz-tv": "Муз-ТВ",
	"spas": "Спас", "che": "Че", "yu": "Ю", "u": "Ю", "super": "Суббота!", "subbota": "Суббота!",
	"izvestiya": "Известия", "izvestia": "Известия", "rbk": "РБК", "rbc": "РБК", "rbc tv": "РБК",
	"360": "360°", "360 podmoskovye": "360° Подмосковье", "moskva 24": "Москва 24", "moscow 24": "Москва 24",
	"moskva doverie": "Москва Доверие", "doverie": "Москва Доверие", "russia today": "RT", "rt": "RT", "rt doc": "RT Документальный",
	"rt d": "RT Документальный", "dozhd": "Дождь", "tv rain": "Дождь", "ru tv": "RU.TV", "rutv": "RU.TV",
	"europa plus tv": "Европа Плюс ТВ", "europa plus": "Европа Плюс ТВ", "bridge tv": "Bridge TV", "mtv russia": "MTV Россия",
	"soyuz": "Союз", "tnv": "ТНВ", "tnv planeta": "ТНВ Планета", "8 kanal": "8 канал", "8 channel": "8 канал",
	"o": "О!", "o!": "О!", "mult": "Мульт", "multimuzyka": "Мультимузыка", "detskiy mir": "Детский мир", "ani": "Ani",
	"tlum hd": "Тлум HD", "gulli girl": "Gulli Girl", "smaylik": "Смайлик", "rodnoe kino": "Родное кино",
	"dom kino": "Дом Кино", "dom kino premium": "Дом Кино Премиум", "kinohit": "Кинохит", "kinopremera": "Кинопремьера",
	"kinosemya": "Киносемья", "kinosvidanie": "Киносвидание", "kinokomediya": "Кинокомедия", "kinoseriya": "Киносерия",
	"kinomiks": "Киномикс", "kinosat": "Киносат", "nashe novoe kino": "Наше новое кино", "indiyskoe kino": "Индийское кино",
	"russkiy illyuzion": "Русский иллюзион", "illyuzion": "Иллюзион+", "eurokino": "Еврокино", "mosfilm": "Мосфильм. Золотая коллекция",
	"kinoujas": "Киноужас", "kino tv": "Кино ТВ", "fenix kino": "Феникс+ Кино", "telecafe": "Телекафе", "eda": "Еда",
	"eda premium": "Еда Премиум", "usadba": "Усадьба", "zdorovoe tv": "Здоровое ТВ", "psikhologiya 21": "Психология21",
	"moya planeta": "Моя планета", "nauka": "Наука", "nauka 2.0": "Наука", "istoriya": "История", "istoria": "История",
	"zhivaya planeta": "Живая планета", "zhivaya priroda": "Живая природа", "zoopark": "Зоопарк", "ohota i rybalka": "Охота и рыбалка",
	"okhota i rybalka": "Охота и рыбалка", "drayv": "Драйв", "dikiy": "Дикий", "doktor": "Доктор", "ugar": "Угар",
	"shokiruyushchee": "Шокирующее", "kukhnya tv": "Кухня ТВ", "kuhnya tv": "Кухня ТВ", "teatr": "Театр", "pobeda": "Победа",
	"zhara": "Жара", "shanson tv": "Шансон ТВ", "muzyka pervogo": "Музыка Первого", "vremya": "Время", "dom kino russia": "Дом Кино",
	"sarafan": "Сарафан", "nostalgiya": "Ностальгия", "nostalgia": "Ностальгия", "retro": "Ретро", "krasnaya liniya": "Красная линия",
	"lyubimoe kino": "Любимое кино", "pobeda tv": "Победа", "bober": "Бобёр", "tv1000": "ТВ1000", "tv1000 russkoe kino": "ТВ1000 Русское кино",
	"tv1000 action": "ТВ1000 Action", "tv1000 kino": "ТВ1000", "sport 1": "Спорт 1", "boks tv": "Бокс ТВ", "start": "Старт",
	"telekanal 2x2": "2х2", "2x2": "2х2", "vmeste rf": "Вместе-РФ", "kvn tv": "КВН ТВ", "yumor box": "Юмор Box", "humor box": "Юмор Box",
	"nickelodeon": "Никелодеон", "nick jr": "Ник Джуниор", "disney": "Дисней", "disney channel": "Дисней", "cartoon network": "Картун Нетворк",
	"cartoonito": "Картунито", "boomerang": "Бумеранг", "jim jam": "Джим Джем", "baby tv": "Бэби ТВ", "gulli": "Галли",
	"discovery": "Дискавери", "discovery channel": "Дискавери", "discovery science": "Дискавери Сайнс", "animal planet": "Энимал Плэнет",
	"national geographic": "Нэшнл Джиографик", "nat geo": "Нэшнл Джиографик", "nat geo wild": "Нэшнл Джиографик Дикая природа",
	"viasat history": "Виасат История", "viasat explore": "Виасат Эксплор", "viasat nature": "Виасат Нэйчер",
	"history": "History", "da vinci": "Да Винчи", "euronews": "Евроньюс", "euronews russian": "Евроньюс", "bbc world news": "Би-би-си",
	"cnn": "Си-эн-эн", "fox": "Фокс", "fox life": "Фокс Лайф", "paramount comedy": "Парамаунт Комеди", "amedia premium": "Амедиа Премиум",
	"amedia hit": "Амедиа Хит", "amedia 1": "Амедиа 1", "amedia 2": "Амедиа 2", "eurosport": "Евроспорт", "eurosport 1": "Евроспорт 1",
	"eurosport 2": "Евроспорт 2", "setanta sports": "Сетанта Спорт", "mezzo": "Меццо", "fashion tv": "Fashion TV",
	"vh1": "VH1", "mtv": "MTV", "bridge": "Bridge TV", "travel channel": "Трэвел", "travel+adventure": "Трэвел+Эдвенчер",
	"tlc": "TLC", "investigation discovery": "Ай Ди Икстрим", "sony channel": "Сони", "sony turbo": "Сони Турбо", "sony sci-fi": "Сони Сай-Фай",
	"kinopoisk": "Кинопоиск", "okko sport": "Okko Спорт", "rutube": "Rutube", "vk video": "VK Видео",
	"belarus 24": "Беларусь 24", "belarus 1": "Беларусь 1", "mir belarus": "Мир Беларусь", "kazakhstan": "Казахстан",
	"telekanal sankt-peterburg": "Санкт-Петербург", "sankt-peterburg": "Санкт-Петербург", "peterburg": "Санкт-Петербург",
	"78": "78", "kanal 78": "78", "rtvi": "RTVI", "rtg tv": "RTG TV", "bbc": "Би-би-си", "tvc international": "ТВ Центр Международный", "perviy kanal hd": "Первый канал HD",
}

// Отдельные слова и сокращения
var ruWords = map[string]string{
	"tv": "ТВ", "telekanal": "Телеканал", "kanal": "канал", "channel": "канал", "news": "Новости", "novosti": "Новости",
	"kino": "Кино", "music": "Музыка", "muzyka": "Музыка", "kids": "Детский", "sport": "Спорт", "sports": "Спорт",
	"plus": "Плюс", "international": "Международный", "world": "Мир", "info": "Инфо", "life": "Лайф", "hd": "HD", "4k": "4K",
	"region": "Регион", "gorod": "Город", "oblast": "область", "kray": "край", "respublika": "Республика",
	"premium": "Премиум", "online": "Онлайн", "radio": "Радио", "live": "Live", "fm": "FM",
}

var (
	junkRe   = regexp.MustCompile(`(?i)\s*[\(\[]\s*(\d{3,4}p|geo[- ]?blocked|not 24/7|24/7|backup|offline|test|ru|rus|russia)\s*[\)\]]`)
	spaceRe  = regexp.MustCompile(`\s+`)
	keyStrip = regexp.MustCompile(`[^\p{L}\p{N}+! ]+`)
)

// cleanName убирает служебные пометки iptv-org: «(1080p)», «[Geo-blocked]», «[Not 24/7]»…
func cleanName(s string) string {
	for i := 0; i < 3; i++ {
		s = junkRe.ReplaceAllString(s, "")
	}
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

func nameKey(s string) string {
	s = strings.ToLower(cleanName(s))
	s = strings.ReplaceAll(s, "ё", "е")
	s = keyStrip.ReplaceAllString(s, " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(s, " "))
}

func hasLatin(s string) bool {
	for _, r := range s {
		if r < 128 && unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// russifyName возвращает русское название канала.
func russifyName(orig string) string {
	name := cleanName(orig)
	if !hasLatin(name) {
		return name
	}
	key := nameKey(name)
	if v, ok := ruChannelNames[key]; ok {
		return v
	}
	// «Название HD», «Название +2» и т.п.: ищем основу в словаре, хвост сохраняем
	words := strings.Fields(name)
	for cut := len(words) - 1; cut >= 1; cut-- {
		if v, ok := ruChannelNames[nameKey(strings.Join(words[:cut], " "))]; ok {
			return v + " " + translitWords(words[cut:])
		}
	}
	return translitWords(words)
}

var acronymMap = map[rune]string{
	'A': "А", 'B': "Б", 'C': "С", 'D': "Д", 'E': "Е", 'F': "Ф", 'G': "Г", 'H': "Х", 'I': "И", 'J': "Ж", 'K': "К", 'L': "Л", 'M': "М",
	'N': "Н", 'O': "О", 'P': "П", 'Q': "К", 'R': "Р", 'S': "С", 'T': "Т", 'U': "У", 'V': "В", 'W': "В", 'X': "Х", 'Y': "Ы", 'Z': "З",
}

func translitWords(words []string) string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		low := strings.ToLower(w)
		switch {
		case !hasLatin(w):
			out = append(out, w)
		case ruWords[low] != "":
			out = append(out, ruWords[low])
		case len(w) <= 4 && strings.ToUpper(w) == w && !strings.ContainsAny(w, "0123456789"):
			// Аббревиатура: OTR → ОТР
			var b strings.Builder
			for _, r := range w {
				if v, ok := acronymMap[r]; ok {
					b.WriteString(v)
				} else {
					b.WriteRune(r)
				}
			}
			out = append(out, b.String())
		default:
			out = append(out, translitWord(w))
		}
	}
	return strings.Join(out, " ")
}

var translitPairs = []struct{ lat, cyr string }{
	{"shch", "щ"}, {"sch", "щ"}, {"zh", "ж"}, {"kh", "х"}, {"ts", "ц"}, {"ch", "ч"}, {"sh", "ш"},
	{"yu", "ю"}, {"ya", "я"}, {"yo", "ё"}, {"ye", "е"}, {"iy", "ий"}, {"yy", "ый"}, {"ey", "ей"}, {"ay", "ай"},
	{"oy", "ой"}, {"uy", "уй"}, {"ia", "ия"}, {"ph", "ф"}, {"x", "кс"}, {"j", "дж"}, {"w", "в"}, {"q", "к"}, {"c", "к"},
	{"a", "а"}, {"b", "б"}, {"v", "в"}, {"g", "г"}, {"d", "д"}, {"e", "е"}, {"z", "з"}, {"i", "и"}, {"k", "к"}, {"l", "л"},
	{"m", "м"}, {"n", "н"}, {"o", "о"}, {"p", "п"}, {"r", "р"}, {"s", "с"}, {"t", "т"}, {"u", "у"}, {"f", "ф"}, {"h", "х"},
}

const vowels = "aeiouy"

// translitWord — обратная транслитерация: Pyatnitsa → Пятница, Domashniy → Домашний.
func translitWord(w string) string {
	low := strings.ToLower(w)
	var b strings.Builder
	for i := 0; i < len(low); {
		ch := low[i]
		if ch >= 128 || !(ch >= 'a' && ch <= 'z') {
			// не латиница: оставляем как есть (цифры, знаки, кириллица)
			r := []rune(low[i:])[0]
			b.WriteRune(r)
			i += len(string(r))
			continue
		}
		matched := false
		for _, p := range translitPairs {
			if strings.HasPrefix(low[i:], p.lat) {
				// «ay», «oy»…: если дальше гласная, это «а» + «я» (Mayak → Маяк)
				if len(p.lat) == 2 && p.lat[1] == 'y' && i+2 < len(low) && strings.IndexByte(vowels, low[i+2]) >= 0 {
					continue
				}
				b.WriteString(p.cyr)
				i += len(p.lat)
				matched = true
				break
			}
		}
		if !matched { // одиночная «y»: после гласной — «й», иначе — «ы»
			if ch == 'y' {
				if i > 0 && strings.IndexByte(vowels, low[i-1]) >= 0 {
					b.WriteString("й")
				} else {
					b.WriteString("ы")
				}
			}
			i++
		}
	}
	res := []rune(b.String())
	if len(res) > 0 && unicode.IsUpper([]rune(w)[0]) {
		res[0] = unicode.ToUpper(res[0])
	}
	return string(res)
}
