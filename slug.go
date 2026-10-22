package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// slugMaxLen — предел длины slug в имени файла: длинный заголовок не должен
// превращать URL в нечитаемую простыню.
const slugMaxLen = 60

// translitRU — посимвольный транслит русского алфавита в латиницу.
var translitRU = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}

// Slugify превращает заголовок в slug: транслит, нижний регистр, только
// [a-z0-9-], повторные разделители схлопываются, обрезка по границе слова.
func Slugify(title string) string {
	var b strings.Builder
	dash := true // начало строки — как дефис: ведущий разделитель не пишем
	for _, r := range strings.ToLower(title) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		default:
			part, cyrillic := translitRU[r]
			if cyrillic && part == "" {
				continue // ъ/ь не дают символа и не создают дефис
			}
			if cyrillic {
				b.WriteString(part)
				dash = false
				continue
			}
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	return truncateSlug(strings.Trim(b.String(), "-"), slugMaxLen)
}

// truncateSlug обрезает slug по последнему дефису в пределах max символов.
// Слово длиннее лимита режется жёстко: иначе от него не осталось бы ничего.
func truncateSlug(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	if i := strings.LastIndexByte(cut, '-'); i > 0 {
		cut = cut[:i]
	}
	return strings.Trim(cut, "-")
}

// MediaFilename — имя файла в публичном URL: номер (не меньше трёх цифр), slug
// заголовка и реальное расширение носителя. Пустой slug даёт имя из номера.
func MediaFilename(number int64, title string, f EpisodeFile) string {
	ext := strings.ToLower(filepath.Ext(f.FilePath))
	if slug := Slugify(title); slug != "" {
		return fmt.Sprintf("%03d-%s%s", number, slug, ext)
	}
	return fmt.Sprintf("%03d%s", number, ext)
}
