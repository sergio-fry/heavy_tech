package main

import (
	"path/filepath"
	"testing"
)

// newTestStore — база в temp-каталоге с накатанными миграциями.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := OpenStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestOpenMigrations — после наката есть все таблицы и соль хеширования.
func TestOpenMigrations(t *testing.T) {
	st := newTestStore(t)
	for _, table := range []string{"episodes", "download_events", "play_events", "meta"} {
		var name string
		err := st.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Fatalf("таблица %s не создана: %v", table, err)
		}
	}
	var salt string
	if err := st.db.QueryRow(`SELECT value FROM meta WHERE key = 'hash_salt'`).Scan(&salt); err != nil {
		t.Fatalf("соль не создана: %v", err)
	}
	if len(salt) != 32 { // 16 байт hex
		t.Fatalf("неожиданная соль: %q", salt)
	}
}

// TestHashStableAndSalted — хеши детерминированы в пределах инстанса и разные между инстансами.
func TestHashStableAndSalted(t *testing.T) {
	a := newTestStore(t)
	b := newTestStore(t)
	h1 := a.hash("203.0.113.7")
	h2 := a.hash("203.0.113.7")
	if string(h1) != string(h2) {
		t.Fatal("хеш в пределах инстанса нестабилен")
	}
	if string(h1) == string(b.hash("203.0.113.7")) {
		t.Fatal("хеши разных инстансов совпали — соль не работает")
	}
}
