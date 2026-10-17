# Spec Delta: podcast-serving

## MODIFIED Requirements

### Requirement: RSS-фид совместим с подкаст-приложениями

`GET /feed.xml` MUST возвращать валидный RSS 2.0 с iTunes-расширением: метаданные канала из `podcast.yaml`; элементы — опубликованные эпизоды, новые сверху; у каждого элемента enclosure с точным размером файла в байтах и корректным MIME-типом, стабильный guid (абсолютный URL аудиофайла), `pubDate` в RFC 1123, `itunes:duration`, когда длительность известна. Все URL в фиде MUST быть абсолютными. MIME-тип в enclosure MUST соответствовать формату файла эпизода (для `.mp3` — `audio/mpeg`, для `.aac` — `audio/aac`, для `.m4a` — `audio/x-m4a`).

#### Scenario: Фид отдаёт эпизоды в правильном порядке и с корректным enclosure

- **WHEN** в индексе есть эпизоды за 2026-10-01 и 2026-10-15, запрошен `GET /feed.xml`
- **THEN** XML валиден, первый item — эпизод 2026-10-15, у каждого item enclosure с URL `https://<host>/audio/<slug>`, точным размером в байтах, типом `audio/mpeg`, guid равен enclosure URL

#### Scenario: Пустой канал тоже валиден

- **WHEN** в папке нет эпизодов, запрошен `GET /feed.xml`
- **THEN** возвращается валидный RSS с метаданными канала и без items

#### Scenario: Эпизод в AAC получает MIME по формату файла

- **WHEN** в индексе есть эпизод из файла `2026-10-20-vypusk.aac` (MIME `audio/aac`) и эпизод из `2026-10-21-vypusk.m4a` (MIME `audio/x-m4a`), запрошен `GET /feed.xml`
- **THEN** enclosure первого item имеет тип `audio/aac`, второго — `audio/x-m4a`; URL, guid и размер в байтах определяются так же, как для MP3

### Requirement: Аудио стримится с поддержкой Range

`GET /audio/{slug}` MUST отдавать содержимое файла эпизода с корректным Content-Type и полной поддержкой HTTP Range (206 Partial Content для диапазонов, устойчивость к докачкам подкаст-приложений). Content-Type MUST соответствовать MIME-типу эпизода, сохранённому в индексе. Для неизвестного слага MUST возвращаться 404.

#### Scenario: Полное скачивание

- **WHEN** запрошен `GET /audio/pervyy-vypusk` без Range
- **THEN** ответ 200 с полным содержимым файла и Content-Type `audio/mpeg`

#### Scenario: Докачка с середины

- **WHEN** запрошен `GET /audio/pervyy-vypusk` с `Range: bytes=1000-1999`
- **THEN** ответ 206 со 1000 запрошенными байтами и корректным `Content-Range`

#### Scenario: Неизвестный эпизод

- **WHEN** запрошен `GET /audio/net-takogo`
- **THEN** ответ 404

#### Scenario: Content-Type соответствует формату эпизода

- **WHEN** запрошен `GET /audio/vtoroy` — эпизод из файла `2026-10-02-vtoroy.aac` — без Range
- **THEN** ответ 200 с полным содержимым файла и Content-Type `audio/aac`; Range-запросы к этому эпизоду работают так же, как для MP3
