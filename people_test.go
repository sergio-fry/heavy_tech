package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writePerson создаёт папку участника: person.yaml + файлы аватара/описания.
// files — имя файла → содержимое (person.yaml пишется всегда, даже если задан в files).
func writePerson(t *testing.T, dir, slug, meta string, files map[string]string) {
	t.Helper()
	sub := filepath.Join(dir, "people", slug)
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(sub, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sub, "person.yaml"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pngBytes — минимальный валидный PNG-заголовок; для тестов достаточно байтов,
// их никто не декодирует.
var pngBytes = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// addPeopleFixtures — Иван Петров (аватар + description.md) и Анна Сидорова
// (описание полем person.yaml, без аватара).
func addPeopleFixtures(t *testing.T, dir string) {
	t.Helper()
	writePerson(t, dir, "ivan-petrov", `name: Иван Петров
avatar: photo.png
description: будет переопределён description.md
`, map[string]string{
		"photo.png":      string(pngBytes),
		"description.md": "Подкастер и **инженер**.",
	})
	writePerson(t, dir, "anna-sidorova", `name: Анна Сидорова
description: Продюсер подкаста.
`, nil)
}

// --- сканирование участников ---

func TestScanPeople(t *testing.T) {
	dir := writePodcastDir(t)
	addPeopleFixtures(t, dir)

	people, err := ScanPeople(dir)
	if err != nil {
		t.Fatalf("ScanPeople: %v", err)
	}
	if len(people) != 2 {
		t.Fatalf("найдено %d участников, хотели 2: %+v", len(people), people)
	}
	// сортировка по slug: anna-sidorova < ivan-petrov
	if people[0].Slug != "anna-sidorova" || people[1].Slug != "ivan-petrov" {
		t.Fatalf("порядок: %q, %q", people[0].Slug, people[1].Slug)
	}
	anna := people[0]
	if anna.Name != "Анна Сидорова" {
		t.Fatalf("имя участницы: %q", anna.Name)
	}
	if anna.Description != "Продюсер подкаста." {
		t.Fatalf("описание участницы: %q", anna.Description)
	}
	if anna.AvatarExt != "" || anna.AvatarPath != "" {
		t.Fatalf("у участницы без аватара не должно быть avatar: %q %q", anna.AvatarExt, anna.AvatarPath)
	}
	ivan := people[1]
	if ivan.Name != "Иван Петров" {
		t.Fatalf("имя участника: %q", ivan.Name)
	}
	if ivan.AvatarExt != ".png" {
		t.Fatalf("расширение аватара: %q", ivan.AvatarExt)
	}
	if filepath.Base(ivan.AvatarPath) != "photo.png" {
		t.Fatalf("путь аватара: %q", ivan.AvatarPath)
	}
	if ivan.Description != "Подкастер и **инженер**." {
		t.Fatalf("description.md должен быть приоритетнее поля description: %q", ivan.Description)
	}
}

func TestScanPeopleNoDir(t *testing.T) {
	dir := writePodcastDir(t)
	people, err := ScanPeople(dir)
	if err != nil {
		t.Fatalf("ScanPeople без папки people: %v", err)
	}
	if people != nil {
		t.Fatalf("ожидали nil, получили %+v", people)
	}
}

func TestScanPeopleErrors(t *testing.T) {
	cases := []struct {
		name  string
		slug  string
		meta  string
		files map[string]string
	}{
		{"битый slug", "Иван", "name: Иван\n", nil},
		{"нет person.yaml", "ivan", "", nil},
		{"пустой name", "ivan", "name: \"  \"\n", nil},
		{"лишний файл", "ivan", "name: Иван\n", map[string]string{"extra.txt": "x"}},
		{"аватара нет", "ivan", "name: Иван\navatar: photo.png\n", nil},
		{"плохое расширение аватара", "ivan", "name: Иван\navatar: photo.txt\n", map[string]string{"photo.txt": "x"}},
		{"несуществующий тип avatar", "ivan", "name: Иван\navatar: 42\n", map[string]string{"photo.png": string(pngBytes)}},
		{"несуществующий тип name", "ivan", "name: [1, 2]\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writePodcastDir(t)
			writePerson(t, dir, tc.slug, tc.meta, tc.files)
			if _, err := ScanPeople(dir); err == nil {
				t.Fatal("ожидали ошибку сканирования")
			}
		})
	}
}

func TestScanPeopleDuplicateName(t *testing.T) {
	dir := writePodcastDir(t)
	writePerson(t, dir, "ivan", "name: Иван Петров\n", nil)
	writePerson(t, dir, "ivan-dubl", "name: Иван Петров\n", nil)
	if _, err := ScanPeople(dir); err == nil {
		t.Fatal("ожидали ошибку: имя участника должно быть уникальным")
	}
}

func TestScanPeopleNonDirEntry(t *testing.T) {
	dir := writePodcastDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "people"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "people", "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ScanPeople(dir); err == nil {
		t.Fatal("ожидали ошибку: файл среди папок участников")
	}
}

// --- связь «выпуск ↔ участник» и хранение ---

func TestSyncPeopleAndLinks(t *testing.T) {
	dir := writePodcastDir(t)
	addEpisodeFixtures(t, dir)
	addPeopleFixtures(t, dir)
	// эпизод 1: ведущий Иван, гость Анна и неизвестный гость; эпизод 2: только гость Иван
	writeEpisode(t, dir, "1", `title: Первый выпуск
published_at: 2026-10-01
host: Иван Петров
guests:
  - Анна Сидорова
  - Незнакомец Без Профиля
audio:
  file: zapis.aac
  duration: 1872
`, map[string]string{"zapis.aac": strings.Repeat("A", 12345)})
	writeEpisode(t, dir, "2", `title: Vtoroy
published_at: 2026-10-15
guests:
  - Иван Петров
audio:
  file: zapis.mp3
video:
  file: efir.mp4
`, map[string]string{
		"zapis.mp3": strings.Repeat("B", 20000),
		"efir.mp4":  strings.Repeat("C", 5000),
	})

	st := newTestStore(t)
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatalf("SyncPodcastDir: %v", err)
	}

	people, err := st.ListPeople()
	if err != nil {
		t.Fatal(err)
	}
	if len(people) != 2 {
		t.Fatalf("в индексе %d участников, хотели 2", len(people))
	}
	ivanIndex := slices.IndexFunc(people, func(p PersonView) bool { return p.Slug == "ivan-petrov" })
	if ivanIndex < 0 {
		t.Fatal("ivan-petrov потерялся")
	}
	// выпуски новые сверху: 2 (2026-10-15), 1 (2026-10-01)
	if !slices.Equal(people[ivanIndex].Episodes, []int64{2, 1}) {
		t.Fatalf("выпуски Ивана: %v", people[ivanIndex].Episodes)
	}
	annaIndex := slices.IndexFunc(people, func(p PersonView) bool { return p.Slug == "anna-sidorova" })
	if annaIndex < 0 || !slices.Equal(people[annaIndex].Episodes, []int64{1}) {
		t.Fatalf("выпуски Анны: %+v", people[annaIndex])
	}

	p, err := st.PersonBySlug("ivan-petrov")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Иван Петров" || p.AvatarExt != ".png" {
		t.Fatalf("PersonBySlug: %+v", p)
	}
	if _, err := st.PersonBySlug("net-takogo"); err == nil {
		t.Fatal("ожидали ErrNoRows для неизвестного slug")
	}

	episodes, roles, err := st.PersonEpisodes("ivan-petrov")
	if err != nil {
		t.Fatal(err)
	}
	if len(episodes) != 2 || episodes[0].Number != 2 || episodes[1].Number != 1 {
		t.Fatalf("выпуски участника: %d шт: %+v", len(episodes), episodes)
	}
	if roles[1] != RoleHost || roles[2] != RoleGuest {
		t.Fatalf("роли: %v", roles)
	}
}

func TestSyncPeopleUpdateAndRemoval(t *testing.T) {
	dir := writePodcastDir(t)
	addEpisodeFixtures(t, dir)
	writePerson(t, dir, "ivan", "name: Иван Петров\n", nil)

	st := newTestStore(t)
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}

	// участник переименован в person.yaml — slug прежний, имя новое
	writePerson(t, dir, "ivan", "name: Иван Петров-\n", nil)
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	p, err := st.PersonBySlug("ivan")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Иван Петров-" {
		t.Fatalf("имя не обновилось: %q", p.Name)
	}

	// участник удалён из папки — строка из индекса уходит (CASCADE чистит связи)
	if err := os.RemoveAll(filepath.Join(dir, "people", "ivan")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PersonBySlug("ivan"); err == nil {
		t.Fatal("удалённый участник остался в индексе")
	}
}

func TestSyncIdempotentWithPeople(t *testing.T) {
	dir := writePodcastDir(t)
	addEpisodeFixtures(t, dir)
	addPeopleFixtures(t, dir)
	st := newTestStore(t)
	if _, _, _, err := SyncPodcastDir(dir, st); err != nil {
		t.Fatal(err)
	}
	added, updated, _, err := SyncPodcastDir(dir, st)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || updated != 0 {
		t.Fatalf("повторный рескан не идемпотентен: added=%d updated=%d", added, updated)
	}
}

// --- HTTP: страница участника, аватар, API, интерактивные участники ---

func newPeopleEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	addPeopleFixtures(t, env.dir)
	// эпизод 1: ведущий Иван, гости Анна и неизвестный
	writeEpisode(t, env.dir, "1", `title: Первый выпуск
published_at: 2026-10-01
host: Иван Петров
guests:
  - Анна Сидорова
  - Незнакомец Без Профиля
description: О чём этот подкаст
audio:
  file: zapis.aac
  duration: 1872
`, map[string]string{"zapis.aac": strings.Repeat("A", 12345)})
	if _, _, _, err := SyncPodcastDir(env.dir, env.store); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestPersonPage(t *testing.T) {
	env := newPeopleEnv(t)
	rec := env.do(t, "GET", "/people/ivan-petrov", "Mozilla/5.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d, хотели 200", rec.Code)
	}
	html := rec.Body.String()
	for _, want := range []string{
		"Иван Петров",
		"Иван Петров — Heavy Tech",
		`src="` + testBaseURL + `/people/ivan-petrov/avatar.png"`,
		"Подкастер и <strong>инженер</strong>.",
		"Выпуски с участием",
		`href="` + testBaseURL + `/episodes/1"`,
		"Ведущий",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("на странице участника нет %q", want)
		}
	}
	// markdown описания не должен остаться сырым
	if strings.Contains(html, "**инженер**") {
		t.Fatal("описание не отрендерено из markdown")
	}

	// канонический URL и уникальный title есть, страница участника ищется в sitemap
	rec = env.do(t, "GET", "/people/anna-sidorova", "Mozilla/5.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("страница Анны: код %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Продюсер подкаста.") {
		t.Fatal("нет описания Анны")
	}

	rec = env.do(t, "GET", "/people/net-takogo", "Mozilla/5.0", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("неизвестный участник: код %d, хотели 404", rec.Code)
	}
}

func TestPersonAvatar(t *testing.T) {
	env := newPeopleEnv(t)
	rec := env.do(t, "GET", "/people/ivan-petrov/avatar.png", "Mozilla/5.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d, хотели 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type %q, хотели image/png", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=300") {
		t.Fatalf("Cache-Control %q", cc)
	}
	if rec.Body.Bytes()[1] != 'P' {
		t.Fatal("тело аватара не совпадает с файлом")
	}

	// оригинальное имя файла не раздаётся — только каноническое avatar.{ext}
	rec = env.do(t, "GET", "/people/ivan-petrov/photo.png", "Mozilla/5.0", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("оригинальное имя файла: код %d, хотели 404", rec.Code)
	}
	// у участника без аватара — 404
	rec = env.do(t, "GET", "/people/anna-sidorova/avatar.png", "Mozilla/5.0", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("аватар без avatar: код %d, хотели 404", rec.Code)
	}
	// неизвестный участник — 404
	rec = env.do(t, "GET", "/people/net/avatar.png", "Mozilla/5.0", nil, nil)
	if rec.Code != 404 {
		t.Fatalf("аватар неизвестного: код %d, хотели 404", rec.Code)
	}
}

func TestPeopleAPIEndpoint(t *testing.T) {
	env := newPeopleEnv(t)
	rec := env.do(t, "GET", "/api/people", "curl/8", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d, хотели 200", rec.Code)
	}
	var people []apiPerson
	if err := json.Unmarshal(rec.Body.Bytes(), &people); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(people) != 2 {
		t.Fatalf("%d участников, хотели 2", len(people))
	}
	ivanIndex := slices.IndexFunc(people, func(p apiPerson) bool { return p.Slug == "ivan-petrov" })
	if ivanIndex < 0 {
		t.Fatal("ivan-petrov отсутствует")
	}
	ivan := people[ivanIndex]
	if ivan.Name != "Иван Петров" {
		t.Fatalf("имя: %q", ivan.Name)
	}
	if ivan.URL != testBaseURL+"/people/ivan-petrov" {
		t.Fatalf("url: %q", ivan.URL)
	}
	if ivan.Avatar != testBaseURL+"/people/ivan-petrov/avatar.png" {
		t.Fatalf("avatar: %q", ivan.Avatar)
	}
	if !slices.Equal(ivan.Episodes, []int64{1}) {
		t.Fatalf("episodes: %v", ivan.Episodes)
	}
	annaIndex := slices.IndexFunc(people, func(p apiPerson) bool { return p.Slug == "anna-sidorova" })
	if annaIndex < 0 {
		t.Fatal("anna-sidorova отсутствует")
	}
	if people[annaIndex].Avatar != "" {
		t.Fatalf("avatar без аватара: %q", people[annaIndex].Avatar)
	}
}

func TestAPIEpisodesParticipantsEntities(t *testing.T) {
	env := newPeopleEnv(t)
	rec := env.do(t, "GET", "/api/episodes", "curl/8", nil, nil)
	var eps []apiEpisode
	if err := json.Unmarshal(rec.Body.Bytes(), &eps); err != nil {
		t.Fatalf("json: %v", err)
	}
	e1Index := slices.IndexFunc(eps, func(e apiEpisode) bool { return e.Number == 1 })
	if e1Index < 0 {
		t.Fatal("эпизод 1 отсутствует")
	}
	e1 := eps[e1Index]
	// host/guests остаются строками, как раньше
	if e1.Host != "Иван Петров" || !slices.Equal(e1.Guests, []string{"Анна Сидорова", "Незнакомец Без Профиля"}) {
		t.Fatalf("host/guests изменились: %q / %v", e1.Host, e1.Guests)
	}
	// participants: ведущий первым, затем гости в порядке
	if len(e1.Participants) != 3 {
		t.Fatalf("participants: %d, хотели 3: %+v", len(e1.Participants), e1.Participants)
	}
	host := e1.Participants[0]
	if host.Role != RoleHost || host.Name != "Иван Петров" || host.Slug != "ivan-petrov" ||
		host.URL != testBaseURL+"/people/ivan-petrov" ||
		host.Avatar != testBaseURL+"/people/ivan-petrov/avatar.png" {
		t.Fatalf("ведущий в participants: %+v", host)
	}
	guest := e1.Participants[1]
	if guest.Role != RoleGuest || guest.Slug != "anna-sidorova" || guest.Avatar != "" {
		t.Fatalf("известная гостья: %+v", guest)
	}
	unknown := e1.Participants[2]
	if unknown.Role != RoleGuest || unknown.Name != "Незнакомец Без Профиля" ||
		unknown.Slug != "" || unknown.URL != "" || unknown.Avatar != "" {
		t.Fatalf("неизвестный гость не должен иметь сущности: %+v", unknown)
	}
}

// TestInteractiveParticipants — имена известных участников становятся ссылками
// на их страницы, неизвестные остаются текстом.
func TestInteractiveParticipants(t *testing.T) {
	env := newPeopleEnv(t)

	html := env.do(t, "GET", "/episodes/1", "Mozilla/5.0", nil, nil).Body.String()
	if !strings.Contains(html, `<a href="`+testBaseURL+`/people/ivan-petrov">Иван Петров</a>`) {
		t.Fatal("ведущий не ссылка на странице выпуска")
	}
	if !strings.Contains(html, `<a href="`+testBaseURL+`/people/anna-sidorova">Анна Сидорова</a>`) {
		t.Fatal("известная гостья не ссылка на странице выпуска")
	}
	if strings.Contains(html, `Незнакомец Без Профиля</a>`) {
		t.Fatal("неизвестный гость не должен быть ссылкой")
	}
	if !strings.Contains(html, "Незнакомец Без Профиля") {
		t.Fatal("неизвестный гость потерялся")
	}

	// карточка на главной тоже интерактивна
	home := env.do(t, "GET", "/", "Mozilla/5.0", nil, nil).Body.String()
	if !strings.Contains(home, `href="`+testBaseURL+`/people/ivan-petrov"`) {
		t.Fatal("на карточке главной нет ссылки на участника")
	}
}

func TestSitemapIncludesPeople(t *testing.T) {
	env := newPeopleEnv(t)
	rec := env.do(t, "GET", "/sitemap.xml", "curl/8", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d, хотели 200", rec.Code)
	}
	for _, want := range []string{
		testBaseURL + "/people/ivan-petrov",
		testBaseURL + "/people/anna-sidorova",
	} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("в sitemap нет %q", want)
		}
	}
}

func TestPersonPageEmpty(t *testing.T) {
	env := newTestEnv(t)
	writePerson(t, env.dir, "nikto", "name: Никто Нираз\n", nil)
	if _, _, _, err := SyncPodcastDir(env.dir, env.store); err != nil {
		t.Fatal(err)
	}
	rec := env.do(t, "GET", "/people/nikto", "Mozilla/5.0", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("код %d, хотели 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Пока нет выпусков с участием") {
		t.Fatal("нет заглушки для участника без выпусков")
	}
	if !strings.Contains(rec.Body.String(), "person-fallback") {
		t.Fatal("нет заглушки аватара")
	}
}

// httptest используется в тестах выше через env.do; добавим явную ссылку,
// чтобы импорт не оказался лишним при рефакторинге.
var _ = httptest.NewRecorder
