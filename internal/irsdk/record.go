package irsdk

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"time"
)

// Recorder writes a live session to a .ibt file as iRacing would, tick by
// tick, so that what a driver's machine read can be played back here through
// the same decoder. The head is written first with a record count of zero and
// patched when the recorder closes; a file cut short by a crash still reads
// to where it stopped, because the reader stops at the bytes.
type Recorder struct {
	f      *os.File
	vars   int
	bufLen int
	n      int
	start  bool
	first  float64
	last   float64
}

// NewRecorder starts a file at path for the given table and session text. The
// session start date is now; the session clock is filled in from the first
// and last ticks, which carry SessionTime.
func NewRecorder(path string, table *Table, bufLen int, session []byte, now time.Time) (*Recorder, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // G304: the path is the operator's.
	if err != nil {
		return nil, fmt.Errorf("irsdk: %w", err)
	}
	sub := DiskSubHeader{SessionStartDate: now}
	if err := WriteFile(f, table.Vars(), bufLen, session, sub, nil); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Recorder{f: f, vars: len(table.Vars()), bufLen: bufLen}, nil
}

// BufLen is the tick length the file was started with; Vars how many
// variables its table has. A live table that changed either needs a new file.
func (r *Recorder) BufLen() int { return r.bufLen }

// Vars is the number of variables in the file's table.
func (r *Recorder) Vars() int { return r.vars }

// Write appends one tick, which must be the recorder's tick length.
func (r *Recorder) Write(row []byte) error {
	if len(row) != r.bufLen {
		return fmt.Errorf("irsdk: a tick of %d bytes, and the file's are %d", len(row), r.bufLen)
	}
	if _, err := r.f.Write(row); err != nil {
		return fmt.Errorf("irsdk: writing a tick: %w", err)
	}
	r.n++
	return nil
}

// Clock notes the session clock of the tick just written, for the head.
func (r *Recorder) Clock(sessionTime float64) {
	if !r.start {
		r.first, r.start = sessionTime, true
	}
	r.last = sessionTime
}

// Written is how many ticks the file holds.
func (r *Recorder) Written() int { return r.n }

// Close patches the record count, the tick count and the session clock into
// the head and closes the file.
func (r *Recorder) Close() error {
	var b [8]byte
	binary.LittleEndian.PutUint32(b[:4], uint32(r.n))
	_, err := r.f.WriteAt(b[:4], 48) // buffer 0's tick count
	if err == nil {
		_, err = r.f.WriteAt(b[:4], HeaderSize+28) // the sub-header's record count
	}
	if err == nil {
		_, err = r.f.WriteAt(float64bits(r.first), HeaderSize+8)
	}
	if err == nil {
		_, err = r.f.WriteAt(float64bits(r.last), HeaderSize+16)
	}
	if cerr := r.f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("irsdk: finishing the recording: %w", err)
	}
	return nil
}

var _ io.Closer = (*Recorder)(nil)

func float64bits(f float64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], math.Float64bits(f))
	return b[:]
}
