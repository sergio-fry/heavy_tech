package main

import "strconv"

// MediaURL — канонический URL носителя с именем файла: один адрес обслуживает
// и стриминг, и скачивание. Им пользуются сайт, фиды (enclosure) и API.
func MediaURL(baseURL, kind string, number int64, title string, file EpisodeFile) string {
	return baseURL + "/" + kind + "/" + strconv.FormatInt(number, 10) + "/" + MediaFilename(number, title, file)
}

// MediaGUID — стабильный идентификатор выпуска в фиде: только номер, без slug,
// чтобы переименование заголовка не меняло guid подписок.
func MediaGUID(baseURL, kind string, number int64) string {
	return baseURL + "/" + kind + "/" + strconv.FormatInt(number, 10)
}

// CoverURL — канонический абсолютный URL обложки выпуска с именем файла. Имя
// декоративно: отдача по /cover/{номер}[/любое-имя] одна и та же (design D4).
func CoverURL(baseURL string, number int64, title, coverPath string) string {
	return baseURL + "/cover/" + strconv.FormatInt(number, 10) + "/" + CoverFilename(number, title, coverPath)
}
