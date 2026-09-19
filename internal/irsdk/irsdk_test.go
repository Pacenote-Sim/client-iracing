package irsdk_test

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/client-iracing/internal/irsdk"
)

// The fixtures are written by this package's own writer in iRacing's layout,
// worked from the SDK's header file, until a real .ibt file replaces them.
// The reading and the writing agreeing proves the two halves consistent; the
// offsets asserted by hand prove them iRacing's.

// aTable is a variable table like a slice of iRacing's: every type, an array.
func aTable() ([]irsdk.Var, int) {
	vars := []irsdk.Var{
		{Type: irsdk.Double, Offset: 0, Count: 1, Name: "SessionTime", Desc: "Seconds since session start", Unit: "s"},
		{Type: irsdk.Int, Offset: 8, Count: 1, Name: "Lap", Desc: "Laps started count", Unit: ""},
		{Type: irsdk.Float, Offset: 12, Count: 1, Name: "Speed", Desc: "GPS vehicle speed", Unit: "m/s"},
		{Type: irsdk.Bool, Offset: 16, Count: 1, Name: "IsOnTrack", Desc: "1=Car on track physics running", Unit: ""},
		{Type: irsdk.BitField, Offset: 20, Count: 1, Name: "SessionFlags", Desc: "Session flags", Unit: "irsdk_Flags"},
		{Type: irsdk.Float, Offset: 24, Count: 64, Name: "CarIdxLapDistPct", Desc: "Percentage distance around lap by car index", Unit: "%"},
		{Type: irsdk.Char, Offset: 280, Count: 1, Name: "Gear", Desc: "-1=reverse 0=neutral", Unit: ""},
	}
	return vars, 284
}

const aSession = "---\nWeekendInfo:\n TrackName: spa gp\n TrackDisplayName: Circuit de Spa-Francorchamps\n" +
	" TrackConfigName: Grand Prix\n TrackLength: 7.00 km\n TrackID: 124\n WeekendOptions:\n  IsFixedSetup: 0\n" +
	"SessionInfo:\n Sessions:\n - SessionNum: 0\n   SessionType: Practice\n   SessionLaps: unlimited\n" +
	" - SessionNum: 1\n   SessionType: Race\n   SessionLaps: 12\n" +
	"DriverInfo:\n DriverCarIdx: 3\n Drivers:\n - CarIdx: 0\n   UserName: Somebody Else\n   CarScreenName: BMW M4 GT3\n" +
	"   CarClassShortName: GT3\n - CarIdx: 3\n   UserName: Jos\xe9 Mar\xeda\n   CarScreenName: Porsche 992 GT3 R\n" +
	"   CarPath: porsche992rgt3\n   CarClassShortName: GT3\n   CarClassID: 2708\n" +
	"SplitTimeInfo:\n Sectors:\n - SectorNum: 0\n   SectorStartPct: 0\n - SectorNum: 1\n   SectorStartPct: 0.35\n" +
	" - SectorNum: 2\n   SectorStartPct: 0.71\n...\n"

// writeFixture writes a file with n ticks, the session clock counting from
// start at 60 Hz, and returns its path.
func writeFixture(t *testing.T, n int, session string) string {
	t.Helper()
	vars, bufLen := aTable()
	table := irsdk.NewTable(vars)
	rows := make([][]byte, 0, n)
	for i := range n {
		row := irsdk.NewRow(table, bufLen).
			Set("SessionTime", 0, 100+float64(i)/60).
			Set("Lap", 0, float64(1+i/120)).
			Set("Speed", 0, 50+float64(i)).
			Set("IsOnTrack", 0, 1).
			Set("Gear", 0, 4).
			Set("CarIdxLapDistPct", 3, 0.25).
			SetBits("SessionFlags", 0x4)
		rows = append(rows, row.Bytes())
	}
	var buf bytes.Buffer
	sub := irsdk.DiskSubHeader{
		SessionStartDate: time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC),
		SessionStartTime: 100, SessionEndTime: 100 + float64(n-1)/60, SessionLapCount: 1,
	}
	require.NoError(t, irsdk.WriteFile(&buf, vars, bufLen, []byte(session+"\x00\x00"), sub, rows))
	path := filepath.Join(t.TempDir(), "session.ibt")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

// The header has the offsets the SDK's header file gives it, byte for byte.
func TestTheLayoutIsIRacings(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	path := writeFixture(t, 2, aSession)
	raw, err := os.ReadFile(path)
	r.NoError(err)

	h, err := irsdk.ParseHeader(raw)
	r.NoError(err)
	r.Equal(2, h.Ver)
	r.True(h.Connected())
	r.Equal(60, h.TickRate)
	r.Equal(7, h.NumVars)
	r.Equal(irsdk.HeaderSize+irsdk.DiskSubHeaderSize, h.VarHeaderOffset, "the table follows the two headers")
	r.Equal(h.VarHeaderOffset+7*irsdk.VarHeaderSize, h.SessionInfoOffset, "the text follows the table")
	r.Equal(len(aSession)+2, h.SessionInfoLen)
	r.Equal(1, h.NumBuf)
	r.Equal(284, h.BufLen)
	r.Equal(h.SessionInfoOffset+h.SessionInfoLen, h.Bufs[0].BufOffset, "the ticks follow the text")
	r.Equal(2, h.Bufs[0].TickCount)
	r.Equal(h.Bufs[0], h.Newest())
	r.Equal(h.Bufs[0].BufOffset+h.BufLen, h.Extent())

	sub, err := irsdk.ParseDiskSubHeader(raw[irsdk.HeaderSize:])
	r.NoError(err)
	r.Equal(time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC), sub.SessionStartDate)
	r.InDelta(100, sub.SessionStartTime, 1e-9)
	r.Equal(2, sub.SessionRecordCount)
	r.Equal(1, sub.SessionLapCount)

	vars, err := irsdk.ParseVars(raw, h)
	r.NoError(err)
	r.Len(vars, 7)
	r.Equal(irsdk.Var{Type: irsdk.Float, Offset: 24, Count: 64, Name: "CarIdxLapDistPct", Desc: "Percentage distance around lap by car index", Unit: "%"}, vars[5])
	r.Equal("double", vars[0].Type.String())
	r.Equal(8, irsdk.Double.Size())
	r.Equal(1, irsdk.Char.Size())
	r.Equal(0, irsdk.VarType(9).Size())
	r.Equal("type 9", irsdk.VarType(9).String())
	for _, ty := range []irsdk.VarType{irsdk.Char, irsdk.Bool, irsdk.Int, irsdk.BitField, irsdk.Float, irsdk.Double} {
		r.NotEmpty(ty.String())
	}

	// The newest of several buffers is the one with the highest tick count,
	// wherever it sits.
	h.NumBuf = 3
	h.Bufs[1] = irsdk.VarBuf{TickCount: 9, BufOffset: 5000}
	h.Bufs[2] = irsdk.VarBuf{TickCount: 7, BufOffset: 6000}
	r.Equal(irsdk.VarBuf{TickCount: 9, BufOffset: 5000}, h.Newest())
	r.Equal(6000+284, h.Extent())
}

// Bytes that are not a header are refused with a reason, before anything is
// read through them.
func TestWhatIsNotAHeader(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	_, err := irsdk.ParseHeader(make([]byte, 10))
	r.ErrorIs(err, irsdk.ErrBadHeader)

	path := writeFixture(t, 1, aSession)
	good, err := os.ReadFile(path)
	r.NoError(err)
	spoil := func(off int, v int32) []byte {
		b := bytes.Clone(good)
		b[off], b[off+1], b[off+2], b[off+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
		return b
	}
	for name, b := range map[string][]byte{
		"too many variables":  spoil(24, 1<<20),
		"no buffers":          spoil(32, 0),
		"five buffers":        spoil(32, 5),
		"a zero tick":         spoil(36, 0),
		"table in the header": spoil(28, 4),
		"text in the header":  spoil(20, 4),
		"buffer in header":    spoil(52, 4),
	} {
		_, perr := irsdk.ParseHeader(b)
		r.ErrorIs(perr, irsdk.ErrBadHeader, name)
	}

	// A table that runs past the bytes, and a variable that runs past a tick.
	h, err := irsdk.ParseHeader(good)
	r.NoError(err)
	_, err = irsdk.ParseVars(good[:h.VarHeaderOffset+10], h)
	r.ErrorIs(err, irsdk.ErrBadHeader)
	bad := spoil(h.VarHeaderOffset+4, 100000) // the first variable's offset
	_, err = irsdk.ParseVars(bad, h)
	r.ErrorIs(err, irsdk.ErrBadHeader)
	r.Contains(err.Error(), "SessionTime")

	_, err = irsdk.ParseDiskSubHeader(make([]byte, 3))
	r.ErrorIs(err, irsdk.ErrBadHeader)
}

// A tick reads every type through the table; what the table lacks is zero.
func TestValuesReadEveryType(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	vars, bufLen := aTable()
	table := irsdk.NewTable(vars)
	row := irsdk.NewRow(table, bufLen).
		Set("SessionTime", 0, 1234.5).Set("Lap", 0, 7).Set("Speed", 0, 61.25).Set("IsOnTrack", 0, 1).
		Set("Gear", 0, -1).Set("CarIdxLapDistPct", 3, 0.5).Set("CarIdxLapDistPct", 63, 0.75).
		Set("CarIdxLapDistPct", 64, 9).Set("Nothing", 0, 9).SetBits("SessionFlags", 0x10004).
		SetBits("Gear", 1).SetBits("Nothing", 1)
	v := irsdk.NewValues(table, row.Bytes())

	r.InDelta(1234.5, v.Float("SessionTime"), 1e-9)
	r.Equal(7, v.Int("Lap"))
	r.InDelta(61.25, v.Float("Speed"), 1e-6)
	r.True(v.Bool("IsOnTrack"))
	r.Equal(-1, v.Int("Gear"), "a char is a signed byte in iRacing's gear")
	r.InDelta(0.5, v.FloatAt("CarIdxLapDistPct", 3), 1e-6)
	r.InDelta(0.75, v.FloatAt("CarIdxLapDistPct", 63), 1e-6)
	r.Zero(v.FloatAt("CarIdxLapDistPct", 64), "past the array")
	r.Zero(v.FloatAt("CarIdxLapDistPct", -1))
	r.Equal(64, v.Count("CarIdxLapDistPct"))
	r.Equal(uint32(0x10004), v.Bits("SessionFlags"))
	r.Equal(0x10004, v.Int("SessionFlags"))
	r.Zero(v.Bits("Gear"), "a one-byte variable is not a bitfield")
	r.Zero(v.Float("Nothing"))
	r.Zero(v.Int("Nothing"))
	r.Zero(v.Count("Nothing"))
	r.False(v.Bool("Nothing"))
	r.False(v.Has("Nothing"))
	r.True(v.Has("Gear"))
	r.Zero(v.Bits("Nothing"))

	// A short row reads as zero rather than past its end.
	short := irsdk.NewValues(table, row.Bytes()[:10])
	r.Zero(short.Float("Speed"))
	r.False(short.Has("Speed"))
	r.Zero(short.Bits("SessionFlags"))
	r.InDelta(1234.5, short.Float("SessionTime"), 1e-9)

	// Lookup, the table's order, and a duplicate name keeping its first entry.
	x, ok := table.Lookup("Speed")
	r.True(ok)
	r.Equal(12, x.Offset)
	_, ok = table.Lookup("Nothing")
	r.False(ok)
	r.Equal(vars, table.Vars())
	dup := irsdk.NewTable([]irsdk.Var{{Type: irsdk.Int, Offset: 0, Count: 1, Name: "X"}, {Type: irsdk.Int, Offset: 4, Count: 1, Name: "X"}})
	x, _ = dup.Lookup("X")
	r.Equal(0, x.Offset)
}

// The session text is read from iRacing's Latin-1, and the parts the client
// uses come out in its units.
func TestTheSessionTextIsRead(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	s, err := irsdk.ParseSession([]byte(aSession + "\x00\x00\x00"))
	r.NoError(err)
	r.Equal("spa gp", s.WeekendInfo.TrackName)
	r.Equal("Circuit de Spa-Francorchamps - Grand Prix", s.Track())
	r.Equal(7000, s.TrackLengthM())
	r.Equal(124, s.WeekendInfo.TrackID)
	r.Equal("Practice", s.Type(0))
	r.Equal("Race", s.Type(1))
	r.Empty(s.Type(5))
	r.Zero(s.Laps(0), "unlimited")
	r.Equal(12, s.Laps(1))
	r.Zero(s.Laps(5))
	car, class := s.Car()
	r.Equal("Porsche 992 GT3 R", car)
	r.Equal("GT3", class)
	r.Equal("José María", s.DriverInfo.Drivers[1].UserName, "Latin-1 became UTF-8")
	r.Equal([]float64{0, 0.35, 0.71}, s.Sectors())
	r.Zero(s.WeekendInfo.WeekendOptions.IsFixedSetup)
	r.InDelta(irsdk.DefaultFuelKgPerLitre, s.FuelKgPerLitre(), 0, "no density in the text")
	dense, err := irsdk.ParseSession([]byte("DriverInfo:\n DriverCarFuelKgPerLtr: 0.760\n"))
	r.NoError(err)
	r.InDelta(0.76, dense.FuelKgPerLitre(), 1e-9)

	// Without the parts: no crash, zero values.
	var none irsdk.Session
	r.Empty(none.Track())
	r.Zero(none.TrackLengthM())
	car, class = none.Car()
	r.Empty(car)
	r.Empty(class)
	r.Nil(none.Sectors())

	// Lengths in other units and in nonsense.
	for text, want := range map[string]int{"4.35 mi": 7001, "1200 m": 1200, "5 furlongs": 0, "x km": 0, "-3 km": 0, "2": 2000} {
		var s irsdk.Session
		s.WeekendInfo.TrackLength = text
		r.Equal(want, s.TrackLengthM(), text)
	}
	var cfgless irsdk.Session
	cfgless.WeekendInfo.TrackDisplayName = "Monza"
	r.Equal("Monza", cfgless.Track())

	_, err = irsdk.ParseSession([]byte("WeekendInfo: [unclosed"))
	r.ErrorIs(err, irsdk.ErrBadSession)
}

// A file plays every tick and then ends; its head is read once.
func TestAFileIsReadTickByTick(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	path := writeFixture(t, 3, aSession)
	f, err := irsdk.OpenFile(path)
	r.NoError(err)
	defer f.Close() //nolint:errcheck // closed below too

	r.Equal(3, f.Sub.SessionRecordCount)
	r.Equal("spa gp", f.Session.WeekendInfo.TrackName)
	r.Equal([]byte(aSession+"\x00\x00"), f.SessionRaw)
	x, ok := f.Table.Lookup("Speed")
	r.True(ok)
	r.Equal(irsdk.Float, x.Type)

	for i := range 3 {
		v, nerr := f.Next()
		r.NoError(nerr)
		r.InDelta(50+float64(i), v.Float("Speed"), 1e-6)
		r.InDelta(100+float64(i)/60, v.Float("SessionTime"), 1e-9)
		r.Equal(1, v.Int("Lap"))
		r.Equal(4, v.Int("Gear"))
		r.InDelta(0.25, v.FloatAt("CarIdxLapDistPct", 3), 1e-6)
		r.Equal(uint32(4), v.Bits("SessionFlags"))
		r.Equal(i+1, f.Read())
	}
	_, err = f.Next()
	r.ErrorIs(err, io.EOF)
	_, err = f.Next()
	r.ErrorIs(err, io.EOF, "and stays ended")
	r.NoError(f.Close())

	// A file whose count says more than it holds ends where the bytes do; one
	// with no count at all ends there too.
	raw, err := os.ReadFile(path)
	r.NoError(err)
	cut := filepath.Join(t.TempDir(), "cut.ibt")
	r.NoError(os.WriteFile(cut, raw[:len(raw)-100], 0o600))
	f, err = irsdk.OpenFile(cut)
	r.NoError(err)
	_, err = f.Next()
	r.NoError(err)
	_, err = f.Next()
	r.NoError(err)
	_, err = f.Next()
	r.ErrorIs(err, io.EOF, "the third tick is cut short")
	r.NoError(f.Close())

	// A file whose session text is not YAML still plays.
	odd := writeFixture(t, 1, "WeekendInfo: [unclosed")
	f, err = irsdk.OpenFile(odd)
	r.NoError(err)
	r.Empty(f.Session.WeekendInfo.TrackName)
	_, err = f.Next()
	r.NoError(err)
	r.NoError(f.Close())
}

// What is not a file, or not one of these.
func TestWhatIsNotATelemetryFile(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	_, err := irsdk.OpenFile(filepath.Join(t.TempDir(), "missing.ibt"))
	r.Error(err)

	short := filepath.Join(t.TempDir(), "short.ibt")
	r.NoError(os.WriteFile(short, make([]byte, 50), 0o600))
	_, err = irsdk.OpenFile(short)
	r.ErrorIs(err, irsdk.ErrBadHeader)

	zeros := filepath.Join(t.TempDir(), "zeros.ibt")
	r.NoError(os.WriteFile(zeros, make([]byte, 200), 0o600))
	_, err = irsdk.OpenFile(zeros)
	r.ErrorIs(err, irsdk.ErrBadHeader, "no buffers")

	// A head that promises a table past the file's end.
	path := writeFixture(t, 1, aSession)
	raw, err := os.ReadFile(path)
	r.NoError(err)
	headOnly := filepath.Join(t.TempDir(), "head.ibt")
	r.NoError(os.WriteFile(headOnly, raw[:irsdk.HeaderSize+irsdk.DiskSubHeaderSize+20], 0o600))
	_, err = irsdk.OpenFile(headOnly)
	r.ErrorIs(err, irsdk.ErrBadHeader)
}

// The writer refuses what would not read back.
func TestTheWriterRefusesWhatWouldNotRead(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	vars, bufLen := aTable()
	var sink bytes.Buffer
	r.Error(irsdk.WriteFile(&sink, vars, 0, nil, irsdk.DiskSubHeader{}, nil), "a zero tick")
	r.Error(irsdk.WriteFile(&sink, vars, 100, nil, irsdk.DiskSubHeader{}, nil), "a variable past the tick")
	long := []irsdk.Var{{Type: irsdk.Int, Offset: 0, Count: 1, Name: string(make([]byte, 40))}}
	r.Error(irsdk.WriteFile(&sink, long, 4, nil, irsdk.DiskSubHeader{}, nil), "a name too long")
	r.Error(irsdk.WriteFile(&sink, vars, bufLen, nil, irsdk.DiskSubHeader{}, [][]byte{make([]byte, 3)}), "a row of the wrong size")
	r.Error(irsdk.WriteFile(failing{}, vars, bufLen, nil, irsdk.DiskSubHeader{}, nil), "a writer that fails")
	r.Error(irsdk.WriteFile(&failAfter{1}, vars, bufLen, nil, irsdk.DiskSubHeader{}, [][]byte{make([]byte, bufLen)}), "a writer that fails on the ticks")
	r.NoError(irsdk.WriteFile(&sink, vars, bufLen, nil, irsdk.DiskSubHeader{}, nil), "no ticks is a file")
}

type failing struct{}

func (failing) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

type failAfter struct{ n int }

func (f *failAfter) Write(b []byte) (int, error) {
	if f.n == 0 {
		return 0, io.ErrClosedPipe
	}
	f.n--
	return len(b), nil
}

// The stand-ins for the live memory on a machine without iRacing say so.
func TestNoLiveMemoryHere(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	if irsdk.Running() {
		t.Skip("iRacing is running here")
	}
	_, err := irsdk.OpenLive()
	r.ErrorIs(err, irsdk.ErrNotRunning)
}

func BenchmarkReadingATick(b *testing.B) {
	vars, bufLen := aTable()
	table := irsdk.NewTable(vars)
	row := irsdk.NewRow(table, bufLen).Set("Speed", 0, 61).Set("Lap", 0, 3).Bytes()
	b.ReportAllocs()
	for b.Loop() {
		v := irsdk.NewValues(table, row)
		_ = v.Float("Speed") + float64(v.Int("Lap")) + v.FloatAt("CarIdxLapDistPct", 3)
	}
}

func BenchmarkParsingTheHeader(b *testing.B) {
	vars, bufLen := aTable()
	var buf bytes.Buffer
	if err := irsdk.WriteFile(&buf, vars, bufLen, []byte(aSession), irsdk.DiskSubHeader{}, nil); err != nil {
		b.Fatal(err)
	}
	raw := buf.Bytes()
	b.ReportAllocs()
	for b.Loop() {
		h, err := irsdk.ParseHeader(raw)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := irsdk.ParseVars(raw, h); err != nil {
			b.Fatal(err)
		}
	}
}
