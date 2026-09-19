package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/client-iracing/internal/irsdk"
)

func aFile(t *testing.T) string {
	t.Helper()
	vars := []irsdk.Var{
		{Type: irsdk.Double, Offset: 0, Count: 1, Name: "SessionTime", Unit: "s", Desc: "Seconds since session start"},
		{Type: irsdk.Float, Offset: 8, Count: 1, Name: "Speed", Unit: "m/s"},
		{Type: irsdk.Int, Offset: 12, Count: 1, Name: "Gear"},
		{Type: irsdk.BitField, Offset: 16, Count: 1, Name: "SessionFlags"},
		{Type: irsdk.Float, Offset: 20, Count: 64, Name: "CarIdxLapDistPct"},
	}
	bufLen := 276
	table := irsdk.NewTable(vars)
	rows := make([][]byte, 0, 130)
	for i := range 130 {
		rows = append(rows, irsdk.NewRow(table, bufLen).Set("SessionTime", 0, float64(i)/60).Set("Speed", 0, 40).
			Set("Gear", 0, 3).SetBits("SessionFlags", 4).Bytes())
	}
	session := "WeekendInfo:\n TrackName: okayama full\n TrackDisplayName: Okayama\n TrackLength: 3.65 km\n" +
		"SessionInfo:\n Sessions:\n - SessionNum: 0\n   SessionType: Offline Testing\n   SessionLaps: unlimited\n"
	var buf bytes.Buffer
	sub := irsdk.DiskSubHeader{SessionStartDate: time.Date(2024, 10, 19, 21, 2, 12, 0, time.UTC), SessionLapCount: 2}
	require.NoError(t, irsdk.WriteFile(&buf, vars, bufLen, []byte(session), sub, rows))
	path := filepath.Join(t.TempDir(), "t.ibt")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

func TestDumpSaysWhatIsInTheFile(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	path := aFile(t)

	var out bytes.Buffer
	r.NoError(run([]string{path}, &out))
	s := out.String()
	r.Contains(s, "header: version 2, status 1, 60 Hz, 5 variables, tick 276 bytes, 1 buffer(s)")
	r.Contains(s, "file: 130 ticks, 2 laps")
	r.Contains(s, "started 2024-10-19 21:02:12 UTC")
	r.Contains(s, "session: Okayama (okayama full), 3650 m")
	r.Contains(s, "session 0: Offline Testing, laps unlimited")
	r.Contains(s, "CarIdxLapDistPct")
	r.Contains(s, "[64]")
	r.Contains(s, "Seconds since session start")
	r.NotContains(s, "ticks, one in")

	out.Reset()
	r.NoError(run([]string{"-ticks", "3", "-every", "60", path}, &out))
	s = out.String()
	r.Contains(s, "ticks, one in 60:")
	r.Contains(s, "SessionTime\tSessionNum\tLap")
	lines := bytes.Count(out.Bytes(), []byte("\n"))
	r.Contains(s, "\t40\t-\t-\t3\t-\t-\t-\t-\t-\t-\t0x4\n", "a variable the file lacks is a dash; a bitfield is hex")
	r.Contains(s, "\n1\t-\t-\t-\t40", "the second printed tick is a second in")
	r.Positive(lines)
	r.Contains(s, "\n2\t-", "the third tick, then the file ends before a fourth")

	out.Reset()
	r.NoError(run([]string{"-yaml", path}, &out))
	r.True(bytes.HasPrefix(out.Bytes(), []byte("WeekendInfo:\n")))

	// Mistakes.
	r.Error(run(nil, &out), "no file")
	r.Error(run([]string{"-bogus"}, &out), "an unknown flag")
	r.Error(run([]string{filepath.Join(t.TempDir(), "none.ibt")}, &out))
}
