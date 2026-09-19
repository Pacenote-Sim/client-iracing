package irsdk

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// File is a .ibt telemetry file: the header, the file's own sub-header, the
// variable table and the session text, then every tick end to end.
type File struct {
	Header  Header
	Sub     DiskSubHeader
	Table   *Table
	Session Session
	// SessionRaw is the session text as written, for anyone who wants more
	// of it than Session keeps.
	SessionRaw []byte

	f    *os.File
	row  []byte
	next int64
	read int
}

// OpenFile opens a telemetry file and reads everything before the first tick.
// A file whose session text cannot be read is still opened, with an empty
// Session: the ticks are the point of the file.
func OpenFile(path string) (*File, error) {
	f, err := os.Open(path) //nolint:gosec // G304: the path is the one the operator gave.
	if err != nil {
		return nil, fmt.Errorf("irsdk: %w", err)
	}
	file, err := readFile(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return file, nil
}

func readFile(f *os.File) (*File, error) {
	head := make([]byte, HeaderSize+DiskSubHeaderSize)
	if _, err := io.ReadFull(f, head); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadHeader, err)
	}
	h, err := ParseHeader(head)
	if err != nil {
		return nil, err
	}
	sub, err := ParseDiskSubHeader(head[HeaderSize:])
	if err != nil {
		return nil, err
	}

	// Everything before the first tick, in one read: the table and the text.
	pre := max(h.VarHeaderOffset+h.NumVars*VarHeaderSize, h.SessionInfoOffset+h.SessionInfoLen)
	front := make([]byte, pre)
	if _, rerr := f.ReadAt(front, 0); rerr != nil {
		return nil, fmt.Errorf("%w: reading the front of the file: %w", ErrBadHeader, rerr)
	}
	vars, err := ParseVars(front, h)
	if err != nil {
		return nil, err
	}
	raw := front[h.SessionInfoOffset : h.SessionInfoOffset+h.SessionInfoLen]
	session, _ := ParseSession(raw) // an unreadable text leaves the zero Session
	return &File{
		Header: h, Sub: sub, Table: NewTable(vars), Session: session, SessionRaw: raw,
		f: f, row: make([]byte, h.BufLen), next: int64(h.Bufs[0].BufOffset),
	}, nil
}

// Next is the next tick, io.EOF after the last. The values read the file's
// one row buffer, which the next call overwrites.
func (file *File) Next() (Values, error) {
	if file.Sub.SessionRecordCount > 0 && file.read >= file.Sub.SessionRecordCount {
		return Values{}, io.EOF
	}
	n, err := file.f.ReadAt(file.row, file.next)
	if n < len(file.row) {
		if err == nil || errors.Is(err, io.EOF) {
			return Values{}, io.EOF
		}
		return Values{}, fmt.Errorf("irsdk: reading tick %d: %w", file.read, err)
	}
	file.next += int64(n)
	file.read++
	return NewValues(file.Table, file.row), nil
}

// Read is how many ticks Next has returned.
func (file *File) Read() int { return file.read }

// Close closes the file.
func (file *File) Close() error {
	if err := file.f.Close(); err != nil {
		return fmt.Errorf("irsdk: %w", err)
	}
	return nil
}
