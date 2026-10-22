package main

import "testing"

// TestMediaURLAndGUID — именованный URL для стриминга/скачивания и номерной
// стабильный guid для фидов.
func TestMediaURLAndGUID(t *testing.T) {
	file := EpisodeFile{Kind: KindVideo, FilePath: "/data/podcast/39/efir.mp4"}
	wantURL := "https://pod.example.com/video/39/039-vse-flomastery-raznye.mp4"
	if got := MediaURL("https://pod.example.com", KindVideo, 39, "Все фломастеры разные", file); got != wantURL {
		t.Fatalf("MediaURL = %q, хотели %q", got, wantURL)
	}
	wantGUID := "https://pod.example.com/video/39"
	if got := MediaGUID("https://pod.example.com", KindVideo, 39); got != wantGUID {
		t.Fatalf("MediaGUID = %q, хотели %q", got, wantGUID)
	}

	audio := EpisodeFile{Kind: KindAudio, FilePath: "/p/7/zapis.m4a"}
	if got := MediaURL("https://host", KindAudio, 7, "🔥", audio); got != "https://host/audio/7/007.m4a" {
		t.Fatalf("MediaURL без slug = %q", got)
	}
}
