package iracing_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pacenote-sim/clientplugin"
	"github.com/pacenote-sim/protocol/wire"

	iracing "github.com/pacenote-sim/client-iracing"
	"github.com/pacenote-sim/client-iracing/internal/irsdk"
)

// The variables the source reads, laid out as a tick; a real table has
// hundreds more, which the source ignores.
func aTable() ([]irsdk.Var, int) {
	names := []struct {
		name string
		ty   irsdk.VarType
	}{
		{"SessionTime", irsdk.Double},
		{"SessionNum", irsdk.Int},
		{"SessionFlags", irsdk.BitField},
		{"IsOnTrack", irsdk.Bool},
		{"OnPitRoad", irsdk.Bool},
		{"Lap", irsdk.Int},
		{"LapDistPct", irsdk.Float},
		{"Speed", irsdk.Float},
		{"Throttle", irsdk.Float},
		{"Brake", irsdk.Float},
		{"Gear", irsdk.Int},
		{"RPM", irsdk.Float},
		{"SteeringWheelAngle", irsdk.Float},
		{"LatAccel", irsdk.Float},
		{"LongAccel", irsdk.Float},
		{"Lat", irsdk.Double},
		{"Lon", irsdk.Double},
		{"LapLastLapTime", irsdk.Float},
		{"LapBestLapTime", irsdk.Float},
		{"PlayerCarMyIncidentCount", irsdk.Int},
		{"PlayerCarPosition", irsdk.Int},
		{"FuelLevel", irsdk.Float},
		{"FuelUsePerHour", irsdk.Float},
		{"Skies", irsdk.Int},
		{"TrackWetness", irsdk.Int},
		{"WindVel", irsdk.Float},
		{"RelativeHumidity", irsdk.Float},
		{"TrackTempCrew", irsdk.Float},
		{"AirTemp", irsdk.Float},
		{"LFtempCM", irsdk.Float},
		{"RFtempCM", irsdk.Float},
		{"LRtempCM", irsdk.Float},
		{"RRtempCM", irsdk.Float},
	}
	vars := make([]irsdk.Var, 0, len(names))
	off := 0
	for _, n := range names {
		vars = append(vars, irsdk.Var{Type: n.ty, Offset: off, Count: 1, Name: n.name})
		off += 8 // every variable on its own eight bytes keeps the doubles aligned
	}
	return vars, off
}

const aSession = "WeekendInfo:\n TrackName: spa gp\n TrackDisplayName: Circuit de Spa-Francorchamps\n TrackConfigName: Grand Prix\n" +
	" TrackLength: 7.00 km\n WeekendOptions:\n  IsFixedSetup: 1\n" +
	"SessionInfo:\n Sessions:\n - SessionNum: 0\n   SessionType: Lone Qualify\n   SessionLaps: 2\n" +
	"DriverInfo:\n DriverCarIdx: 1\n Drivers:\n - CarIdx: 1\n   CarScreenName: Porsche 992 GT3 R\n   CarClassShortName: GT3\n" +
	"SplitTimeInfo:\n Sectors:\n - SectorNum: 0\n   SectorStartPct: 0\n - SectorNum: 1\n   SectorStartPct: 0.5\n"

// aTick is a tick with the driver hard on the brakes into a corner.
func aTick(table *irsdk.Table, bufLen int, clock float64) []byte {
	return irsdk.NewRow(table, bufLen).
		Set("SessionTime", 0, clock).Set("SessionNum", 0, 0).SetBits("SessionFlags", 0x4).
		Set("IsOnTrack", 0, 1).Set("OnPitRoad", 0, 0).Set("Lap", 0, 3).Set("LapDistPct", 0, 0.4321).
		Set("Speed", 0, 50).Set("Throttle", 0, 0.02).Set("Brake", 0, 0.87).Set("Gear", 0, 3).Set("RPM", 0, 6500).
		Set("SteeringWheelAngle", 0, math.Pi/4).Set("LatAccel", 0, 9.80665).Set("LongAccel", 0, -19.6133).
		Set("Lat", 0, 50.4372).Set("Lon", 0, 5.9714).Set("LapLastLapTime", 0, 138.4).Set("LapBestLapTime", 0, -1).
		Set("PlayerCarMyIncidentCount", 0, 2).Set("PlayerCarPosition", 0, 4).Set("FuelLevel", 0, 41.5).
		Set("FuelUsePerHour", 0, 95).Set("Skies", 0, 1).Set("TrackWetness", 0, 0).Set("WindVel", 0, 2.5).
		Set("RelativeHumidity", 0, 0.55).Set("TrackTempCrew", 0, 31.5).Set("AirTemp", 0, 22).
		Set("LFtempCM", 0, 81).Set("RFtempCM", 0, 84).Set("LRtempCM", 0, 79).Set("RRtempCM", 0, 80).
		Bytes()
}

func writeFixture(t *testing.T, n int) string {
	t.Helper()
	vars, bufLen := aTable()
	table := irsdk.NewTable(vars)
	rows := make([][]byte, 0, n)
	for i := range n {
		rows = append(rows, aTick(table, bufLen, 300+float64(i)/60))
	}
	var buf bytes.Buffer
	sub := irsdk.DiskSubHeader{SessionStartDate: time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC), SessionStartTime: 300}
	require.NoError(t, irsdk.WriteFile(&buf, vars, bufLen, []byte(aSession), sub, rows))
	path := filepath.Join(t.TempDir(), "q.ibt")
	require.NoError(t, os.WriteFile(path, buf.Bytes(), 0o600))
	return path
}

// The manifest and the code agree, and the source registered itself.
func TestItIsAPlugin(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	m, err := clientplugin.LoadManifest(".")
	r.NoError(err)
	r.Equal(clientplugin.KindSource, m.Kind)
	r.Equal(iracing.Name, m.Name)
	_, ok := clientplugin.Default.Source(iracing.Name)
	r.True(ok, "init() registered it")
}

// A tick becomes a sample in the client's units: km/h, degrees with left
// negative, g, milliseconds, percent humidity; the session from the text.
func TestATickBecomesASample(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	src := iracing.New()
	src.File = writeFixture(t, 2)
	src.Speed = 1
	var slept []time.Duration
	src.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	ctx := context.Background()

	r.True(src.Running())
	r.NoError(src.Open(ctx))
	s, err := src.Read(ctx)
	r.NoError(err)

	r.Equal(time.Date(2026, 9, 18, 20, 0, 0, 0, time.UTC), s.At)
	r.Equal(wire.SessionQualifying, s.Session)
	r.Equal("Lone Qualify", s.SessionName)
	r.Equal("Circuit de Spa-Francorchamps - Grand Prix", s.Track)
	r.Equal("spa gp", s.TrackID)
	r.Equal(7000, s.TrackLengthM)
	r.Equal("Porsche 992 GT3 R", s.Car)
	r.Equal("GT3", s.CarClass)
	r.Equal([]float64{0, 0.5}, s.Sectors)
	r.False(s.SetupOpen, "a fixed setup")
	r.True(s.OnTrack)
	r.False(s.OnPitRoad)
	r.Equal(3, s.Lap)
	r.InDelta(0.4321, s.LapDistPct, 1e-4)
	r.InDelta(180, s.SpeedKmh, 1e-4)
	r.InDelta(0.02, s.Throttle, 1e-6)
	r.InDelta(0.87, s.Brake, 1e-6)
	r.Equal(3, s.Gear)
	r.InDelta(6500, s.RPM, 1e-3)
	r.InDelta(-45, s.SteerDeg, 1e-4, "iRacing's left is positive; the client's is negative")
	r.InDelta(1, s.LatG, 1e-6)
	r.InDelta(-2, s.LongG, 1e-6)
	r.InDelta(50.4372, s.Lat, 1e-9)
	r.InDelta(5.9714, s.Lon, 1e-9)
	r.Equal(138400, s.LastLapMs)
	r.Zero(s.BestLapMs, "iRacing's minus one is no time")
	r.Equal(2, s.Incidents)
	r.Equal(4, s.Position)
	r.InDelta(41.5, s.FuelL, 1e-6)
	r.InDelta(95/0.75, s.FuelPerHourL, 1e-6, "kilograms an hour over petrol's density, the text naming none")
	r.Equal(clientplugin.Weather{Skies: 1, WindKmh: 9, Humidity: 55, TrackTempC: 31.5, AirTempC: 22}, roundWeather(s.Weather))
	r.Equal(clientplugin.Tyres{LF: 81, RF: 84, LR: 79, RR: 80}, s.Tyres)
	r.Equal(wire.FlagGreen, s.Flag)
	r.Equal(2, s.LapsTotal)

	// The second tick comes a sixtieth later and the file waited that long.
	s2, err := src.Read(ctx)
	r.NoError(err)
	r.Equal(s.At.Add(time.Second/60), s2.At)
	r.Len(slept, 1)
	r.InDelta(float64(time.Second/60), float64(slept[0]), float64(time.Millisecond))

	// Then the file ends, once, and the source is not running any more.
	_, err = src.Read(ctx)
	r.ErrorIs(err, io.EOF)
	r.False(src.Running(), "a played file is not played again")
	r.NoError(src.Close())
	r.NoError(src.Close(), "closing twice is nothing")
}

func roundWeather(w clientplugin.Weather) clientplugin.Weather {
	w.WindKmh = math.Round(w.WindKmh*1000) / 1000
	w.Humidity = math.Round(w.Humidity*1000) / 1000
	w.TrackTempC = math.Round(w.TrackTempC*100) / 100
	return w
}

// A file plays faster when asked, and stops when the context does.
func TestAFilePlaysAtSpeed(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	src := iracing.New()
	src.File = writeFixture(t, 3)
	src.Speed = 10
	var slept []time.Duration
	src.Sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	ctx := context.Background()
	r.NoError(src.Open(ctx))
	_, err := src.Read(ctx)
	r.NoError(err)
	_, err = src.Read(ctx)
	r.NoError(err)
	r.Len(slept, 1)
	r.InDelta(float64(time.Second/600), float64(slept[0]), float64(time.Millisecond))

	r.NoError(src.Close())

	// A wait that ends with the context ends the read.
	stopped := iracing.New()
	stopped.File = src.File
	stopped.Sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	r.NoError(stopped.Open(ctx))
	_, err = stopped.Read(ctx)
	r.NoError(err, "the first tick waits for nothing")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = stopped.Read(cancelled)
	r.ErrorIs(err, context.Canceled)
	r.NoError(stopped.Close())

	// The environment names the file and the speed.
	env := map[string]string{iracing.EnvFile: src.File, iracing.EnvSpeed: "4"}
	fromEnv := iracing.NewFrom(func(k string) string { return env[k] })
	r.Equal(src.File, fromEnv.File)
	r.InDelta(4, fromEnv.Speed, 0)
	env[iracing.EnvSpeed] = "fast"
	r.InDelta(1, iracing.NewFrom(func(k string) string { return env[k] }).Speed, 0, "nonsense is real time")
}

// A file that is not there is tried once and given up on; a read before an
// open is a mistake, not a crash.
func TestAMissingFileIsGivenUpOn(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	src := iracing.New()
	src.File = filepath.Join(t.TempDir(), "none.ibt")
	r.True(src.Running())
	r.Error(src.Open(context.Background()))
	r.False(src.Running())
	_, err := src.Read(context.Background())
	r.Error(err)
	r.NoError(src.Close())
}

// Without a file, the source is live iRacing: not running on this machine.
func TestLiveIRacingIsNotHere(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	src := iracing.New()
	src.File = ""
	if src.Running() {
		t.Skip("iRacing is running here")
	}
	r.ErrorIs(src.Open(context.Background()), irsdk.ErrNotRunning)
}

// The live path, against a stand-in for the memory: ticks come as the event
// fires, the table and the text are re-read when iRacing says they changed,
// a quiet spell is checked against the header, and a closed session ends it.
func TestTheLiveMemoryIsReadTickByTick(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	vars, bufLen := aTable()
	table := irsdk.NewTable(vars)
	mem := &fakeLive{table: table, bufLen: bufLen, session: []byte(aSession), update: 1, connected: true}
	mem.row = aTick(table, bufLen, 300)
	now := time.Date(2026, 9, 18, 21, 0, 0, 0, time.UTC)
	src := iracing.NewLive(func() bool { return mem.connected }, func() (iracing.Live, error) { return mem, nil })
	src.Now = func() time.Time { return now }
	ctx := context.Background()

	r.True(src.Running())
	r.NoError(src.Open(ctx))
	s, err := src.Read(ctx)
	r.NoError(err)
	r.Equal(now, s.At)
	r.Equal(3, s.Lap)
	r.Equal("spa gp", s.TrackID)
	r.Equal(1, mem.tableReads, "the table was read once")

	// Same session: no re-read. A new session text: read again, even one
	// that will not parse, which keeps the last that did.
	mem.row = aTick(table, bufLen, 301)
	_, err = src.Read(ctx)
	r.NoError(err)
	r.Equal(1, mem.tableReads)
	mem.update, mem.session = 2, []byte("WeekendInfo: [unclosed")
	s, err = src.Read(ctx)
	r.NoError(err)
	r.Equal(2, mem.tableReads)
	r.Equal("spa gp", s.TrackID, "the last readable text stands")

	// A quiet spell with iRacing still there is waited through; one with it
	// gone ends the session.
	mem.timeouts = 1
	_, err = src.Read(ctx)
	r.NoError(err)
	mem.timeouts, mem.connected = 1, false
	_, err = src.Read(ctx)
	r.ErrorIs(err, irsdk.ErrNotRunning)
	r.False(src.Running())
	r.NoError(src.Close())

	// The failures each part can have, surfaced.
	mem.connected = true
	mem.waitErr = errors.New("the wait broke")
	r.NoError(src.Open(ctx))
	_, err = src.Read(ctx)
	r.ErrorIs(err, mem.waitErr)
	mem.waitErr = nil
	mem.headerErr = errors.New("no header")
	_, err = src.Read(ctx)
	r.ErrorIs(err, mem.headerErr)
	mem.headerErr = nil
	mem.tableErr = errors.New("no table")
	mem.update = 3
	_, err = src.Read(ctx)
	r.ErrorIs(err, mem.tableErr)
	mem.tableErr = nil
	mem.sessionErr = errors.New("no text")
	_, err = src.Read(ctx)
	r.ErrorIs(err, mem.sessionErr)
	mem.sessionErr = nil
	mem.tickErr = errors.New("no tick")
	_, err = src.Read(ctx)
	r.ErrorIs(err, mem.tickErr)
	mem.tickErr = nil
	r.NoError(src.Close())

	// Opening the memory can fail too.
	broken := iracing.NewLive(func() bool { return true }, func() (iracing.Live, error) { return nil, irsdk.ErrNotRunning })
	r.ErrorIs(broken.Open(ctx), irsdk.ErrNotRunning)
}

// fakeLive stands in for iRacing's memory.
type fakeLive struct {
	table      *irsdk.Table
	bufLen     int
	row        []byte
	session    []byte
	update     int
	connected  bool
	timeouts   int
	tableReads int

	waitErr, headerErr, tableErr, sessionErr, tickErr error
}

func (f *fakeLive) Header() (irsdk.Header, error) {
	if f.headerErr != nil {
		return irsdk.Header{}, f.headerErr
	}
	h := irsdk.Header{TickRate: 60, SessionInfoUpdate: f.update, NumVars: len(f.table.Vars()), NumBuf: 1, BufLen: f.bufLen}
	if f.connected {
		h.Status = irsdk.StatusConnected
	}
	return h, nil
}

func (f *fakeLive) Table() (*irsdk.Table, error) {
	if f.tableErr != nil {
		return nil, f.tableErr
	}
	f.tableReads++
	return f.table, nil
}

func (f *fakeLive) SessionRaw() ([]byte, int, error) {
	if f.sessionErr != nil {
		return nil, 0, f.sessionErr
	}
	return f.session, f.update, nil
}

func (f *fakeLive) Wait(context.Context, time.Duration) error {
	if f.waitErr != nil {
		return f.waitErr
	}
	if f.timeouts > 0 {
		f.timeouts--
		return irsdk.ErrTimeout
	}
	return nil
}

func (f *fakeLive) Tick(row []byte) (irsdk.Header, error) {
	if f.tickErr != nil {
		return irsdk.Header{}, f.tickErr
	}
	copy(row, f.row)
	return f.Header()
}

func (f *fakeLive) Close() error { return nil }

// Session kinds and flags, from iRacing's names and bits.
func TestSessionKindsAndFlags(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	for name, want := range map[string]wire.SessionType{
		"Practice": wire.SessionPractice, "Open Practice": wire.SessionPractice, "Warmup": wire.SessionPractice,
		"Lone Qualify": wire.SessionQualifying, "Open Qualify": wire.SessionQualifying,
		"Race": wire.SessionRace, "Heat Race": wire.SessionRace, "Offline Testing": wire.SessionTesting, "": wire.SessionPractice,
	} {
		r.Equal(want, iracing.SessionKind(name), name)
	}
	for bits, want := range map[uint32]wire.Flag{
		0x1: wire.FlagCheckered, 0x10: wire.FlagRed, 0x10000: wire.FlagBlack, 0x20000: wire.FlagBlack,
		0x8: wire.FlagYellow, 0x100: wire.FlagYellow, 0x4000: wire.FlagYellow, 0x8000: wire.FlagYellow,
		0x2: wire.FlagWhite, 0x4: wire.FlagGreen, 0x400: wire.FlagGreen, 0x4 | 0x1: wire.FlagCheckered, 0: "",
		0x20: "",
	} {
		r.Equal(want, iracing.FlagOf(bits), bits)
	}
}

// A tick with nothing in it, and one with nonsense, is a sample with zeros.
func TestAnEmptyTickIsAZeroSample(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	vars, bufLen := aTable()
	table := irsdk.NewTable(vars)
	s := iracing.ToSample(irsdk.NewValues(table, make([]byte, bufLen)), irsdk.Session{}, time.Time{})
	r.Zero(s.SpeedKmh)
	r.Zero(s.LastLapMs)
	r.True(s.SetupOpen, "with nothing said, the car can be changed")
	r.Equal(wire.SessionPractice, s.Session)
	r.Empty(s.Flag)

	odd := irsdk.NewRow(table, bufLen).Set("LapDistPct", 0, 1.5).Set("Speed", 0, -3).Set("Brake", 0, math.NaN()).
		Set("PlayerCarPosition", 0, -1).Set("LapLastLapTime", 0, math.Inf(1)).Set("RelativeHumidity", 0, 2).Bytes()
	s = iracing.ToSample(irsdk.NewValues(table, odd), irsdk.Session{}, time.Time{})
	r.InDelta(1, s.LapDistPct, 0)
	r.Zero(s.SpeedKmh)
	r.Zero(s.Brake)
	r.Zero(s.Position)
	r.Zero(s.LastLapMs)
	r.InDelta(100, s.Weather.Humidity, 0)
}

// The real sleep waits and stops with the context.
func TestSleep(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	r.NoError(iracing.SleepFor(context.Background(), 0))
	r.NoError(iracing.SleepFor(context.Background(), time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.ErrorIs(iracing.SleepFor(ctx, time.Hour), context.Canceled)
}

// The live path records the session when asked: a .ibt that reads back
// through the decoder, a .yaml of the text, rewritten when the text changes,
// and a new .ibt when the table changes shape.
func TestALiveSessionIsRecorded(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	vars, bufLen := aTable()
	table := irsdk.NewTable(vars)
	mem := &fakeLive{table: table, bufLen: bufLen, session: []byte(aSession), update: 1, connected: true}
	mem.row = aTick(table, bufLen, 300)
	now := time.Date(2026, 9, 18, 21, 0, 0, 0, time.UTC)
	src := iracing.NewLive(func() bool { return true }, func() (iracing.Live, error) { return mem, nil })
	src.Now = func() time.Time { return now }
	src.RecordDir = t.TempDir()
	ctx := context.Background()

	r.NoError(src.Open(ctx))
	for i := range 3 {
		mem.row = aTick(table, bufLen, 300+float64(i)/60)
		_, err := src.Read(ctx)
		r.NoError(err)
	}
	name := filepath.Join(src.RecordDir, "iracing-20260918-210000")
	yaml, err := os.ReadFile(name + ".yaml")
	r.NoError(err)
	r.YAMLEq(aSession, string(yaml))

	// The text changes: the yaml is rewritten and the ticks go on.
	mem.update, mem.session = 2, []byte(aSession+"CarSetup:\n Tires: {}\n")
	_, err = src.Read(ctx)
	r.NoError(err)
	yaml, err = os.ReadFile(name + ".yaml")
	r.NoError(err)
	r.Contains(string(yaml), "CarSetup")

	// The table grows: a new file for a new shape. The clock moves so the
	// name does.
	now = now.Add(time.Minute)
	bigger := append(append([]irsdk.Var{}, vars...), irsdk.Var{Type: irsdk.Int, Offset: bufLen, Count: 1, Name: "Extra"})
	mem.table, mem.bufLen, mem.update = irsdk.NewTable(bigger), bufLen+8, 3
	mem.row = irsdk.NewRow(mem.table, mem.bufLen).Set("SessionTime", 0, 400).Set("Extra", 0, 7).Bytes()
	_, err = src.Read(ctx)
	r.NoError(err)
	r.NoError(src.Close())

	first, err := irsdk.OpenFile(name + ".ibt")
	r.NoError(err)
	r.Equal(4, first.Sub.SessionRecordCount)
	r.InDelta(300, first.Sub.SessionStartTime, 1e-9)
	r.InDelta(300+2.0/60, first.Sub.SessionEndTime, 1e-9)
	v, err := first.Next()
	r.NoError(err)
	r.InDelta(50, v.Float("Speed"), 1e-6)
	r.NoError(first.Close())

	second, err := irsdk.OpenFile(filepath.Join(src.RecordDir, "iracing-20260918-210100.ibt"))
	r.NoError(err)
	r.Equal(1, second.Sub.SessionRecordCount)
	r.Len(second.Table.Vars(), len(vars)+1)
	v, err = second.Next()
	r.NoError(err)
	r.Equal(7, v.Int("Extra"))
	r.NoError(second.Close())

	// A directory that cannot be written: the reading goes on, the close
	// says what happened, and the file was tried once.
	broken := iracing.NewLive(func() bool { return true }, func() (iracing.Live, error) { return mem, nil })
	broken.Now = func() time.Time { return now }
	broken.RecordDir = filepath.Join(t.TempDir(), "missing", "dir")
	r.NoError(broken.Open(ctx))
	_, err = broken.Read(ctx)
	r.NoError(err)
	_, err = broken.Read(ctx)
	r.NoError(err)
	r.Error(broken.Close())

	// The environment names the directory.
	env := map[string]string{iracing.EnvRecord: "/tmp/rec"}
	r.Equal("/tmp/rec", iracing.NewFrom(func(k string) string { return env[k] }).RecordDir)
}

// A real file, when one is at hand: every tick reads, and what comes out is
// a lap of a car. PACENOTE_IRACING_FIXTURE names it; without one the test is
// skipped, because a real file is not in the repository.
func TestARealFileEndToEnd(t *testing.T) {
	t.Parallel()
	path := os.Getenv("PACENOTE_IRACING_FIXTURE")
	if path == "" {
		t.Skip("PACENOTE_IRACING_FIXTURE names no .ibt file")
	}
	r := require.New(t)

	src := iracing.New()
	src.File, src.Speed = path, 1
	src.Sleep = func(context.Context, time.Duration) error { return nil }
	ctx := context.Background()
	r.NoError(src.Open(ctx))
	defer src.Close() //nolint:errcheck // read only

	var n, onTrack, moving int
	maxKmh, minPct, maxPct := 0.0, 1.0, 0.0
	laps := map[int]bool{}
	var first, last clientplugin.Sample
	for {
		s, err := src.Read(ctx)
		if errors.Is(err, io.EOF) {
			break
		}
		r.NoError(err)
		if n == 0 {
			first = s
		}
		last = s
		n++
		if s.OnTrack {
			onTrack++
		}
		if s.SpeedKmh > 1 {
			moving++
		}
		maxKmh = math.Max(maxKmh, s.SpeedKmh)
		minPct, maxPct = math.Min(minPct, s.LapDistPct), math.Max(maxPct, s.LapDistPct)
		laps[s.Lap] = true
		r.GreaterOrEqual(s.Throttle, 0.0)
		r.LessOrEqual(s.Throttle, 1.0)
		r.GreaterOrEqual(s.Brake, 0.0)
		r.LessOrEqual(s.Brake, 1.0)
		r.GreaterOrEqual(s.Gear, -1)
		r.LessOrEqual(s.Gear, 8)
		r.Less(s.RPM, 20000.0)
		r.Less(math.Abs(s.SteerDeg), 1080.0)
		// Accelerations spike into the tens of g on a wall or a reset; only
		// nonsense is refused.
		r.Less(math.Abs(s.LatG), 100.0)
		r.Less(math.Abs(s.LongG), 100.0)
	}
	t.Logf("%d ticks, %d on track, %d moving, %d laps, %.0f km/h at most, %s %s, %d m, fuel %.1f L at %.1f L/h, %s",
		n, onTrack, moving, len(laps), maxKmh, first.Track, first.Car, first.TrackLengthM, last.FuelL, last.FuelPerHourL, first.Session)
	r.Positive(n)
	r.Positive(moving, "a session in which the car never moved")
	r.Greater(maxKmh, 50.0)
	r.Less(maxKmh, 450.0)
	r.GreaterOrEqual(minPct, 0.0)
	r.LessOrEqual(maxPct, 1.0)
	r.NotEmpty(first.Track)
	r.NotEmpty(first.Car)
	r.Positive(first.TrackLengthM)
	r.NotEmpty(first.SessionName)
	r.True(last.At.After(first.At))
	r.Positive(last.FuelL)
	r.Less(last.FuelPerHourL, 200.0, "litres an hour, not kilograms")
}

// What a tick costs. iRacing publishes sixty a second and every one of them
// becomes a sample: this runs inside the loop that keeps up with the game.
func BenchmarkATickToASample(b *testing.B) {
	vars, bufLen := aTable()
	table := irsdk.NewTable(vars)
	row := aTick(table, bufLen, 300)
	session, err := irsdk.ParseSession([]byte(aSession))
	if err != nil {
		b.Fatal(err)
	}
	vals := irsdk.NewValues(table, row)
	at := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	// The facts of the session are worked out when the session changes, not on
	// every tick, which is what the reader hands over.
	f := iracing.FactsOf(session, vals.Int("SessionNum"))
	b.ReportAllocs()
	for b.Loop() {
		_ = iracing.ToSampleWith(vals, f, at)
	}
}

// And what reading the session text costs, which happens when iRacing says it
// changed: once at the start of a session, and then hardly ever.
func BenchmarkReadingTheSessionText(b *testing.B) {
	raw := []byte(aSession)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := irsdk.ParseSession(raw); err != nil {
			b.Fatal(err)
		}
	}
}

// The facts of a session are read from its text, which is the expensive half
// of a sample, so they are read again only when they can have changed: when
// iRacing rewrites the text, or when the weekend moves to the next session.
func TestTheSessionFactsAreReadAgainOnlyWhenTheyChange(t *testing.T) {
	t.Parallel()
	r := require.New(t)

	practice, err := irsdk.ParseSession([]byte(aSession))
	r.NoError(err)
	race, err := irsdk.ParseSession([]byte(strings.Replace(aSession, "Lone Qualify", "Race", 1)))
	r.NoError(err)

	var c iracing.CachedFacts
	first := c.Of(practice, 0, false)
	r.Equal("Lone Qualify", first.Name())
	r.Equal("Circuit de Spa-Francorchamps - Grand Prix", first.Track())

	// The same session again: the same facts, the same slice, nothing read.
	again := c.Of(practice, 0, false)
	r.Equal(first.Name(), again.Name())
	r.Equal(&first.Sectors()[0], &again.Sectors()[0], "the sectors were copied again")

	// The text was read again: so are the facts.
	after := c.Of(race, 0, true)
	r.Equal("Race", after.Name())

	// And the weekend moving to the next session is a change too, text or not.
	var c2 iracing.CachedFacts
	r.Equal("Lone Qualify", c2.Of(practice, 0, false).Name())
	r.Empty(c2.Of(practice, 1, false).Name(), "session 1 is not in this text, and is not session 0's facts")
}
