package irsdk_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/client-iracing/internal/irsdk"
)

// A recording reads back through the same decoder: the head patched with the
// count and the clock, every tick as written.
func TestARecordingReadsBack(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	vars, bufLen := aTable()
	table := irsdk.NewTable(vars)
	path := filepath.Join(t.TempDir(), "rec.ibt")
	start := time.Date(2026, 9, 18, 22, 0, 0, 0, time.UTC)
	rec, err := irsdk.NewRecorder(path, table, bufLen, []byte(aSession), start)
	r.NoError(err)
	r.Equal(bufLen, rec.BufLen())
	r.Equal(len(vars), rec.Vars())

	for i := range 5 {
		row := irsdk.NewRow(table, bufLen).Set("SessionTime", 0, 500+float64(i)/60).Set("Speed", 0, float64(i)).Bytes()
		r.NoError(rec.Write(row))
		rec.Clock(500 + float64(i)/60)
	}
	r.Equal(5, rec.Written())
	r.Error(rec.Write(make([]byte, 3)), "a tick of the wrong size")
	r.NoError(rec.Close())

	f, err := irsdk.OpenFile(path)
	r.NoError(err)
	defer f.Close() //nolint:errcheck // read only
	r.Equal(5, f.Sub.SessionRecordCount)
	r.Equal(5, f.Header.Bufs[0].TickCount)
	r.Equal(start, f.Sub.SessionStartDate)
	r.InDelta(500, f.Sub.SessionStartTime, 1e-9)
	r.InDelta(500+4.0/60, f.Sub.SessionEndTime, 1e-9)
	r.Equal("spa gp", f.Session.WeekendInfo.TrackName)
	for i := range 5 {
		v, nerr := f.Next()
		r.NoError(nerr)
		r.InDelta(float64(i), v.Float("Speed"), 1e-6)
	}
	_, err = f.Next()
	r.ErrorIs(err, io.EOF)

	// A file the recorder never closed still reads to where the ticks stop.
	cut := filepath.Join(t.TempDir(), "cut.ibt")
	rec, err = irsdk.NewRecorder(cut, table, bufLen, nil, start)
	r.NoError(err)
	r.NoError(rec.Write(make([]byte, bufLen)))
	r.NoError(rec.Write(make([]byte, bufLen)))
	f2, err := irsdk.OpenFile(cut)
	r.NoError(err)
	r.Zero(f2.Sub.SessionRecordCount, "not patched yet")
	_, err = f2.Next()
	r.NoError(err)
	_, err = f2.Next()
	r.NoError(err)
	_, err = f2.Next()
	r.ErrorIs(err, io.EOF)
	r.NoError(f2.Close())
	r.NoError(rec.Close())

	// What cannot be recorded.
	_, err = irsdk.NewRecorder(filepath.Join(t.TempDir(), "no", "dir", "x.ibt"), table, bufLen, nil, start)
	r.Error(err)
	_, err = irsdk.NewRecorder(filepath.Join(t.TempDir(), "bad.ibt"), table, 0, nil, start)
	r.Error(err, "a zero tick")
	_, err = os.Stat(filepath.Join(t.TempDir(), "bad.ibt"))
	r.Error(err, "and no file left behind")

	// Closing a recorder whose file is gone reports it.
	gone := filepath.Join(t.TempDir(), "gone.ibt")
	rec, err = irsdk.NewRecorder(gone, table, bufLen, nil, start)
	r.NoError(err)
	r.NoError(rec.Close())
	r.Error(rec.Close(), "closed twice")
}
