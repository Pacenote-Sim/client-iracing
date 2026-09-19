// Package iracing is the client's source for iRacing. It reads the shared
// memory iRacing publishes sixty times a second and turns each tick into a
// [clientplugin.Sample]; the app does the rest. On a machine without iRacing
// it can play one of iRacing's own .ibt telemetry files instead, at the pace
// it was driven or faster, which is how it is developed and tested here.
package iracing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/pacenote-sim/clientplugin"

	"github.com/pacenote-sim/client-iracing/internal/irsdk"
)

func init() { clientplugin.RegisterSource(New()) }

// Name is the simulator, as the stint's Sim field says it.
const Name = "iracing"

// The environment names a telemetry file to play instead of live iRacing,
// and how many times faster than real time. A built client carries this and
// nothing plays unless asked; it is for a machine without iRacing.
const (
	EnvFile  = "PACENOTE_IRACING_IBT"
	EnvSpeed = "PACENOTE_IRACING_SPEED"
	// EnvRecord names a directory to write the live session into as .ibt
	// and .yaml files, as iRacing itself would: what the machine read, for
	// playing back here through the same decoder.
	EnvRecord = "PACENOTE_IRACING_RECORD"
	// WaitForTick is how long a live read waits for iRacing's signal before
	// checking that it is still running.
	WaitForTick = 2 * time.Second
)

// Source is the iRacing source.
type Source struct {
	// File is a telemetry file to play instead of live iRacing; empty reads
	// iRacing. Speed is how many times faster than real time a file plays.
	// RecordDir, when set, gets a .ibt and a .yaml of every live session.
	File      string
	Speed     float64
	RecordDir string
	// Sleep waits between a file's ticks; Now is the clock a live tick is
	// stamped with. Tests replace both.
	Sleep func(context.Context, time.Duration) error
	Now   func() time.Time

	running  func() bool
	openLive func() (live, error)
	r        reader
	done     bool
}

// New is the source, playing the file the environment names if it names one.
func New() *Source { return newFrom(os.Getenv) }

func newFrom(getenv func(string) string) *Source {
	s := &Source{
		File: getenv(EnvFile), RecordDir: getenv(EnvRecord), Speed: 1,
		Sleep: sleep, Now: time.Now, running: irsdk.Running, openLive: openLive,
	}
	if v, err := strconv.ParseFloat(getenv(EnvSpeed), 64); err == nil && v > 0 {
		s.Speed = v
	}
	return s
}

// Name implements [clientplugin.Source].
func (*Source) Name() string { return Name }

// Running implements [clientplugin.Source]: iRacing has its memory open, or
// a file is waiting to be played and has not been.
func (s *Source) Running() bool {
	if s.File != "" {
		return !s.done
	}
	return s.running()
}

// Open implements [clientplugin.Source].
func (s *Source) Open(context.Context) error {
	if s.File != "" {
		f, err := irsdk.OpenFile(s.File)
		if err != nil {
			s.done = true // a file that cannot be opened is not tried again
			return fmt.Errorf("iracing: %w", err)
		}
		s.r = &fileReader{file: f, speed: s.Speed, sleep: s.Sleep}
		return nil
	}
	l, err := s.openLive()
	if err != nil {
		return fmt.Errorf("iracing: %w", err)
	}
	s.r = &liveReader{live: l, now: s.Now, recordDir: s.RecordDir}
	return nil
}

// Read implements [clientplugin.Source]: the next tick as a sample. A file
// that has ended is io.EOF and is not played again; iRacing that has closed
// its session is an error too, and the app polls until it opens one.
func (s *Source) Read(ctx context.Context) (clientplugin.Sample, error) {
	if s.r == nil {
		return clientplugin.Sample{}, errors.New("iracing: read before open")
	}
	vals, f, at, err := s.r.Next(ctx)
	if err != nil {
		if errors.Is(err, io.EOF) {
			s.done = true
		}
		return clientplugin.Sample{}, fmt.Errorf("iracing: %w", err)
	}
	return toSample(vals, f, at), nil
}

// Close implements [clientplugin.Source].
func (s *Source) Close() error {
	if s.r == nil {
		return nil
	}
	err := s.r.Close()
	s.r = nil
	if err != nil {
		return fmt.Errorf("iracing: %w", err)
	}
	return nil
}

// reader is a file or the live memory: the next tick, the facts of the session
// it belongs to, and the time it was produced. The facts are the reader's to
// keep, because only the reader knows when the session text changed.
type reader interface {
	Next(ctx context.Context) (irsdk.Values, facts, time.Time, error)
	Close() error
}

// fileReader plays a telemetry file at its own pace, or faster.
type fileReader struct {
	file  *irsdk.File
	speed float64
	sleep func(context.Context, time.Duration) error
	last  float64
	first bool
	facts cachedFacts
}

func (f *fileReader) Next(ctx context.Context) (irsdk.Values, facts, time.Time, error) {
	vals, err := f.file.Next()
	if err != nil {
		return irsdk.Values{}, facts{}, time.Time{}, err //nolint:wrapcheck // io.EOF has to stay itself.
	}
	// The session clock, or the tick count when the file has none.
	clock := vals.Float("SessionTime")
	if !vals.Has("SessionTime") {
		clock = float64(f.file.Read()) / float64(max(f.file.Header.TickRate, 1))
	}
	if f.first {
		if gap := clock - f.last; gap > 0 {
			d := time.Duration(gap * float64(time.Second))
			if f.speed > 1 {
				d = time.Duration(float64(d) / f.speed)
			}
			if err := f.sleep(ctx, d); err != nil {
				return irsdk.Values{}, facts{}, time.Time{}, err
			}
		}
	}
	f.first, f.last = true, clock
	at := f.file.Sub.SessionStartDate.Add(time.Duration((clock - f.file.Sub.SessionStartTime) * float64(time.Second)))
	// A file's session text never changes; its session number can.
	return vals, f.facts.of(f.file.Session, vals.Int(varSessionNum), false), at, nil
}

func (f *fileReader) Close() error { return f.file.Close() } //nolint:wrapcheck // the file's own words.

// live is what the live memory offers, so a test can stand in for it.
type live interface {
	Header() (irsdk.Header, error)
	Table() (*irsdk.Table, error)
	SessionRaw() ([]byte, int, error)
	Wait(ctx context.Context, timeout time.Duration) error
	Tick(row []byte) (irsdk.Header, error)
	Close() error
}

func openLive() (live, error) { return irsdk.OpenLive() } //nolint:wrapcheck // the reader's own words.

// liveReader reads iRacing's memory tick by tick, re-reading the variable
// table and the session text when iRacing says they changed.
type liveReader struct {
	live    live
	now     func() time.Time
	table   *irsdk.Table
	row     []byte
	session irsdk.Session
	update  int

	// recordDir is where the session is written, when it is; rec is the
	// file being written and recPath its name without an extension.
	recordDir string
	rec       *irsdk.Recorder
	recPath   string
	recErr    error

	// facts are the session's, kept until iRacing changes the text; fresh
	// says it just did.
	facts cachedFacts
	fresh bool
}

func (l *liveReader) Next(ctx context.Context) (irsdk.Values, facts, time.Time, error) {
	for {
		err := l.live.Wait(ctx, WaitForTick)
		switch {
		case errors.Is(err, irsdk.ErrTimeout):
			// Nothing for a while: still there, or gone between sessions?
			h, herr := l.live.Header()
			if herr != nil || !h.Connected() {
				return irsdk.Values{}, facts{}, time.Time{}, irsdk.ErrNotRunning
			}
			continue
		case err != nil:
			return irsdk.Values{}, facts{}, time.Time{}, err //nolint:wrapcheck // the wait's own words.
		}
		break
	}
	h, err := l.live.Header()
	if err != nil {
		return irsdk.Values{}, facts{}, time.Time{}, err //nolint:wrapcheck // the reader's own words.
	}
	l.fresh = false
	if l.table == nil || h.SessionInfoUpdate != l.update || l.table.Vars() == nil || len(l.row) != h.BufLen {
		if err := l.refresh(h); err != nil {
			return irsdk.Values{}, facts{}, time.Time{}, err
		}
		l.fresh = true
	}
	if _, err := l.live.Tick(l.row); err != nil {
		return irsdk.Values{}, facts{}, time.Time{}, err //nolint:wrapcheck // the reader's own words.
	}
	vals := irsdk.NewValues(l.table, l.row)
	l.record(vals)
	return vals, l.facts.of(l.session, vals.Int(varSessionNum), l.fresh), l.now(), nil
}

// record writes the tick to the recording, when there is one. A recording
// that fails is closed and not retried; the reading goes on, and the error
// is reported by Close.
func (l *liveReader) record(vals irsdk.Values) {
	if l.rec == nil {
		return
	}
	if err := l.rec.Write(l.row); err != nil {
		l.recErr = err
		_ = l.rec.Close()
		l.rec = nil
		return
	}
	l.rec.Clock(vals.Float(varSessionTime))
}

// startRecording opens the .ibt for the session under refresh, and writes
// the session text beside it as .yaml. A table that changed shape since the
// recording began means a new file: a .ibt has one table.
func (l *liveReader) startRecording(h irsdk.Header, raw []byte) {
	if l.recordDir == "" || l.recErr != nil {
		return
	}
	if l.rec != nil {
		if l.rec.BufLen() == h.BufLen && l.rec.Vars() == h.NumVars {
			l.recErr = os.WriteFile(l.recPath+".yaml", raw, 0o600) // the text changed; the ticks go on
			return
		}
		l.recErr = l.rec.Close()
		l.rec = nil
	}
	name := "iracing-" + l.now().UTC().Format("20060102-150405")
	l.recPath = filepath.Join(l.recordDir, name)
	rec, err := irsdk.NewRecorder(l.recPath+".ibt", l.table, h.BufLen, raw, l.now())
	if err != nil {
		l.recErr = err
		return
	}
	l.rec = rec
	if err := os.WriteFile(l.recPath+".yaml", raw, 0o600); err != nil {
		l.recErr = err
	}
}

// refresh re-reads the table and the session text. A session text that will
// not parse keeps the last one that did: the ticks are still worth reading.
func (l *liveReader) refresh(h irsdk.Header) error {
	table, err := l.live.Table()
	if err != nil {
		return err //nolint:wrapcheck // the reader's own words.
	}
	raw, update, err := l.live.SessionRaw()
	if err != nil {
		return err //nolint:wrapcheck // the reader's own words.
	}
	if s, perr := irsdk.ParseSession(raw); perr == nil {
		l.session = s
	}
	l.table, l.update = table, update
	if len(l.row) != h.BufLen {
		l.row = make([]byte, h.BufLen)
	}
	l.startRecording(h, raw)
	return nil
}

func (l *liveReader) Close() error {
	err := l.live.Close()
	if l.rec != nil {
		if cerr := l.rec.Close(); err == nil {
			err = cerr
		}
		l.rec = nil
	}
	if err == nil {
		err = l.recErr
	}
	return err
}

// sleep waits, or stops when the context does.
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // the context's own error, as the app expects it.
	case <-t.C:
		return nil
	}
}

var _ clientplugin.Source = (*Source)(nil)
