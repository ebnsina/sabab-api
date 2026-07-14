package migrate

import (
	"context"
	"io"
	"log/slog"
)

// fakeDriver records what Run asked it to do, so the tests can assert on
// behaviour without needing a live database.
type fakeDriver struct {
	applied    map[string]string // version -> checksum, already in the database
	appliedNow []string          // versions Run applied during the test
	unlocked   bool
}

func (d *fakeDriver) Name() string { return "fake" }

func (d *fakeDriver) EnsureVersionTable(context.Context) error { return nil }

func (d *fakeDriver) AppliedVersions(context.Context) (map[string]string, error) {
	return d.applied, nil
}

func (d *fakeDriver) Apply(_ context.Context, m Migration) error {
	d.appliedNow = append(d.appliedNow, m.Version)
	return nil
}

func (d *fakeDriver) Lock(context.Context) (func(context.Context) error, error) {
	return func(context.Context) error {
		d.unlocked = true
		return nil
	}, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
