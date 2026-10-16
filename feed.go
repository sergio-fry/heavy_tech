package main

import (
	"encoding/xml"
	"strconv"
	"time"
)

const itunesNS = "http://www.itunes.com/dtds/podcast-1.0.dtd"

// Теги itunes:* заданы с префиксом, а не через URL-namespace: encoding/xml
// при маршалинге отдаёт префиксные теги (де-факто стандарт агрегаторов), хотя
// обратный unmarshal по таким тегам не работает — в тестах используются
// отдельные структуры с URL-неймспейсами.

// defaultBitrate — оценка длительности при неизвестном битрейте: 128 kbps.
const defaultBitrate = 128 * 1000 / 8 // байт в секунду

// EffectiveDuration — длительность эпизода: из sidecar либо оценка по размеру.
func EffectiveDuration(e Episode) int64 {
	if e.DurationSeconds > 0 {
		return e.DurationSeconds
	}
	if e.FileSize <= 0 {
		return 0
	}
	return (e.FileSize + defaultBitrate - 1) / defaultBitrate
}

type rssFeed struct {
	XMLName xml.Name `xml:"rss"`
	Version string   `xml:"version,attr"`
	Itunes  string   `xml:"xmlns:itunes,attr"`
	Channel channel  `xml:"channel"`
}

type channel struct {
	Title         string       `xml:"title"`
	Link          string       `xml:"link"`
	Description   string       `xml:"description"`
	Author        string       `xml:"itunes:author,omitempty"`
	Owner         *owner       `xml:"itunes:owner,omitempty"`
	Image         *itunesImage `xml:"itunes:image,omitempty"`
	Category      *category    `xml:"itunes:category,omitempty"`
	Explicit      string       `xml:"itunes:explicit"`
	LastBuildDate string       `xml:"lastBuildDate"`
	Items         []item       `xml:"item"`
}

type owner struct {
	Name  string `xml:"itunes:name,omitempty"`
	Email string `xml:"itunes:email"`
}

type itunesImage struct {
	Href string `xml:"href,attr"`
}

type category struct {
	Text string `xml:"text,attr"`
}

type guid struct {
	IsPermaLink string `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

type enclosure struct {
	URL    string `xml:"url,attr"`
	Length int64  `xml:"length,attr"`
	Type   string `xml:"type,attr"`
}

type item struct {
	Title       string    `xml:"title"`
	GUID        guid      `xml:"guid"`
	PubDate     string    `xml:"pubDate"`
	Description string    `xml:"description"`
	Enclosure   enclosure `xml:"enclosure"`
	Duration    string    `xml:"itunes:duration,omitempty"`
}

// BuildFeed собирает RSS 2.0 с iTunes-расширением. views — эпизоды с агрегатами,
// уже отсортированные от новых к старым. Все URL абсолютные.
func BuildFeed(ch Channel, baseURL string, views []EpisodeView, now time.Time) ([]byte, error) {
	link := ch.Link
	if link == "" {
		link = baseURL
	}
	c := channel{
		Title:         ch.Title,
		Link:          link,
		Description:   ch.Description,
		Author:        ch.Author,
		Explicit:      strconv.FormatBool(ch.Explicit),
		LastBuildDate: now.UTC().Format(time.RFC1123Z),
	}
	if ch.Email != "" {
		c.Owner = &owner{Name: ch.Author, Email: ch.Email}
	}
	if ch.Image != "" {
		c.Image = &itunesImage{Href: ch.Image}
	}
	if ch.Category != "" {
		c.Category = &category{Text: ch.Category}
	}
	for _, v := range views {
		audioURL := baseURL + "/audio/" + v.Slug
		c.Items = append(c.Items, item{
			Title:       v.Title,
			GUID:        guid{IsPermaLink: "true", Value: audioURL},
			PubDate:     v.PublishedAt.UTC().Format(time.RFC1123Z),
			Description: v.Description,
			Enclosure: enclosure{
				URL:    audioURL,
				Length: v.FileSize,
				Type:   v.Mime,
			},
			Duration: strconv.FormatInt(EffectiveDuration(v.Episode), 10),
		})
	}
	out, err := xml.MarshalIndent(rssFeed{Version: "2.0", Itunes: itunesNS, Channel: c}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), out...), nil
}
