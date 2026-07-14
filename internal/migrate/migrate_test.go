package migrate

import (
	"errors"
	"testing"
	"testing/fstest"

	"github.com/ebnsina/sabab-api/migrations"
)

// The migrations we actually ship must load and pass validation. Without this,
// a misnamed file is only discovered when someone runs `make migrate`.
func TestEmbeddedMigrationsAreValid(t *testing.T) {
	for _, dir := range []string{migrations.PostgresDir, migrations.ClickHouseDir} {
		t.Run(dir, func(t *testing.T) {
			loaded, err := Load(migrations.FS, dir)
			if err != nil {
				t.Fatalf("Load(%s): %v", dir, err)
			}
			if len(loaded) == 0 {
				t.Fatalf("no migrations found in %s", dir)
			}
			// Every ClickHouse file must split into at least one statement, or
			// the driver would send an empty query.
			if dir == migrations.ClickHouseDir {
				for _, m := range loaded {
					if len(splitStatements(m.SQL)) == 0 {
						t.Errorf("%s produced no executable statements", m.Filename)
					}
				}
			}
		})
	}
}

func TestLoadOrdersByVersion(t *testing.T) {
	// Deliberately out of order in the map: ordering must come from the
	// filename, not from directory iteration order.
	fsys := fstest.MapFS{
		"pg/0010_later.sql":  {Data: []byte("SELECT 10;")},
		"pg/0002_second.sql": {Data: []byte("SELECT 2;")},
		"pg/0001_init.sql":   {Data: []byte("SELECT 1;")},
	}

	got, err := Load(fsys, "pg")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"0001", "0002", "0010"}
	if len(got) != len(want) {
		t.Fatalf("want %d migrations, got %d", len(want), len(got))
	}
	for i, version := range want {
		if got[i].Version != version {
			t.Errorf("position %d: want version %s, got %s", i, version, got[i].Version)
		}
	}
	if got[0].Name != "init" {
		t.Errorf("want name %q, got %q", "init", got[0].Name)
	}
}

func TestLoadRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		fsys fstest.MapFS
	}{
		{
			name: "filename not matching NNNN_description.sql",
			fsys: fstest.MapFS{"pg/init.sql": {Data: []byte("SELECT 1;")}},
		},
		{
			name: "uppercase in description",
			fsys: fstest.MapFS{"pg/0001_Init.sql": {Data: []byte("SELECT 1;")}},
		},
		{
			// Two files claiming 0001 makes "which ran?" unanswerable.
			name: "duplicate version",
			fsys: fstest.MapFS{
				"pg/0001_init.sql":  {Data: []byte("SELECT 1;")},
				"pg/0001_other.sql": {Data: []byte("SELECT 2;")},
			},
		},
		{
			name: "empty file",
			fsys: fstest.MapFS{"pg/0001_init.sql": {Data: []byte("   \n")}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(tc.fsys, "pg"); err == nil {
				t.Fatal("want an error, got nil")
			}
		})
	}
}

// A migration edited after it was applied must be a hard failure, not a silent
// skip: that is how a laptop and production quietly end up with different
// schemas.
func TestRunRejectsChangedChecksum(t *testing.T) {
	fsys := fstest.MapFS{"pg/0001_init.sql": {Data: []byte("SELECT 1;")}}
	loaded, err := Load(fsys, "pg")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	d := &fakeDriver{applied: map[string]string{"0001": "a-different-checksum"}}
	err = Run(t.Context(), d, loaded, discardLogger())
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("want ErrChecksumMismatch, got %v", err)
	}
	if len(d.appliedNow) != 0 {
		t.Errorf("nothing should have been applied, got %v", d.appliedNow)
	}
}

func TestRunAppliesOnlyPending(t *testing.T) {
	fsys := fstest.MapFS{
		"pg/0001_init.sql": {Data: []byte("SELECT 1;")},
		"pg/0002_more.sql": {Data: []byte("SELECT 2;")},
	}
	loaded, err := Load(fsys, "pg")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// 0001 is already applied with the matching checksum, so only 0002 runs.
	d := &fakeDriver{applied: map[string]string{"0001": loaded[0].Checksum}}
	if err := Run(t.Context(), d, loaded, discardLogger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(d.appliedNow) != 1 || d.appliedNow[0] != "0002" {
		t.Errorf("want only 0002 applied, got %v", d.appliedNow)
	}
	if !d.unlocked {
		t.Error("the migration lock was not released")
	}
}

// Run on an up-to-date database must be a no-op, since every service may call
// it at boot.
func TestRunIsIdempotent(t *testing.T) {
	fsys := fstest.MapFS{"pg/0001_init.sql": {Data: []byte("SELECT 1;")}}
	loaded, err := Load(fsys, "pg")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	d := &fakeDriver{applied: map[string]string{"0001": loaded[0].Checksum}}
	if err := Run(t.Context(), d, loaded, discardLogger()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(d.appliedNow) != 0 {
		t.Errorf("want no migrations applied, got %v", d.appliedNow)
	}
}
