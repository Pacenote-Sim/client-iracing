// Package irsdk reads what iRacing publishes: the shared memory it writes
// sixty times a second while it runs, and the .ibt telemetry files it writes
// to disk, which have the same layout with the ticks laid end to end. The
// layout is iRacing's own, from the header file its SDK ships; this package
// is a reading of it in Go with no C, so that the client cross-compiles.
//
// Everything is little-endian, as iRacing writes it. A header names where the
// variable table, the session text and the telemetry buffers are; the table
// says what each variable is and where it sits in a tick; a tick is BufLen
// bytes; the session text is YAML.
package irsdk

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// The layout's fixed numbers.
const (
	// MaxBufs is how many telemetry buffers the header describes. iRacing
	// rotates through them; the one with the highest tick count is newest.
	MaxBufs = 4
	// MaxString and MaxDesc are the fixed widths of a variable's name and
	// unit, and of its description, NUL-padded.
	MaxString = 32
	MaxDesc   = 64
	// HeaderSize, VarHeaderSize and DiskSubHeaderSize are the byte sizes of
	// the three fixed structures.
	HeaderSize        = 112
	VarHeaderSize     = 144
	DiskSubHeaderSize = 32
	// StatusConnected is the header status bit set while iRacing is running
	// a session.
	StatusConnected = 1
	// MaxVars bounds a variable table; iRacing publishes a few hundred.
	MaxVars = 4096
	// MaxBufLen bounds one tick; iRacing's is a few kilobytes.
	MaxBufLen = 1 << 20
)

// ErrBadHeader reports bytes that are not an iRacing header.
var ErrBadHeader = errors.New("irsdk: not an iRacing header")

// Header is what sits at the start of the shared memory and of a file.
type Header struct {
	// Ver is the layout version; Status holds StatusConnected while a
	// session runs; TickRate is ticks per second, sixty.
	Ver, Status, TickRate int
	// SessionInfoUpdate counts changes to the session text; the text is
	// SessionInfoLen bytes at SessionInfoOffset.
	SessionInfoUpdate, SessionInfoLen, SessionInfoOffset int
	// NumVars variable headers sit at VarHeaderOffset.
	NumVars, VarHeaderOffset int
	// NumBuf telemetry buffers of BufLen bytes each, described by Bufs.
	NumBuf, BufLen int
	Bufs           [MaxBufs]VarBuf
}

// VarBuf is one telemetry buffer: its tick count and where it starts.
type VarBuf struct {
	TickCount, BufOffset int
}

// ParseHeader reads a header from the start of b.
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize {
		return Header{}, fmt.Errorf("%w: %d bytes, and a header is %d", ErrBadHeader, len(b), HeaderSize)
	}
	i32 := func(off int) int { return int(int32(binary.LittleEndian.Uint32(b[off:]))) }
	h := Header{
		Ver: i32(0), Status: i32(4), TickRate: i32(8),
		SessionInfoUpdate: i32(12), SessionInfoLen: i32(16), SessionInfoOffset: i32(20),
		NumVars: i32(24), VarHeaderOffset: i32(28),
		NumBuf: i32(32), BufLen: i32(36),
	}
	for i := range MaxBufs {
		h.Bufs[i] = VarBuf{TickCount: i32(48 + i*16), BufOffset: i32(52 + i*16)}
	}
	switch {
	case h.NumVars < 0 || h.NumVars > MaxVars:
		return Header{}, fmt.Errorf("%w: %d variables", ErrBadHeader, h.NumVars)
	case h.NumBuf < 1 || h.NumBuf > MaxBufs:
		return Header{}, fmt.Errorf("%w: %d buffers", ErrBadHeader, h.NumBuf)
	case h.BufLen <= 0 || h.BufLen > MaxBufLen:
		return Header{}, fmt.Errorf("%w: a tick of %d bytes", ErrBadHeader, h.BufLen)
	case h.VarHeaderOffset < HeaderSize || h.SessionInfoOffset < HeaderSize || h.SessionInfoLen < 0:
		return Header{}, fmt.Errorf("%w: offsets inside the header", ErrBadHeader)
	}
	for i := range h.NumBuf {
		if h.Bufs[i].BufOffset < HeaderSize {
			return Header{}, fmt.Errorf("%w: buffer %d inside the header", ErrBadHeader, i)
		}
	}
	return h, nil
}

// Connected reports that iRacing is running a session.
func (h Header) Connected() bool { return h.Status&StatusConnected != 0 }

// Newest is the buffer with the highest tick count, the one to read.
func (h Header) Newest() VarBuf {
	best := h.Bufs[0]
	for i := 1; i < h.NumBuf; i++ {
		if h.Bufs[i].TickCount > best.TickCount {
			best = h.Bufs[i]
		}
	}
	return best
}

// Extent is how many bytes from the start the header refers to: the least a
// mapping or a file must hold for the header to be read from.
func (h Header) Extent() int {
	n := max(h.VarHeaderOffset+h.NumVars*VarHeaderSize, h.SessionInfoOffset+h.SessionInfoLen)
	for i := range h.NumBuf {
		n = max(n, h.Bufs[i].BufOffset+h.BufLen)
	}
	return n
}

// DiskSubHeader follows the header in a file and says what the file holds.
type DiskSubHeader struct {
	// SessionStartDate is when the session began; SessionStartTime and
	// SessionEndTime are the session clock, in seconds, at the first and the
	// last tick.
	SessionStartDate                 time.Time
	SessionStartTime, SessionEndTime float64
	// SessionLapCount is laps in the file; SessionRecordCount ticks.
	SessionLapCount, SessionRecordCount int
}

// ParseDiskSubHeader reads the file's sub-header from b, which starts at
// HeaderSize into the file.
func ParseDiskSubHeader(b []byte) (DiskSubHeader, error) {
	if len(b) < DiskSubHeaderSize {
		return DiskSubHeader{}, fmt.Errorf("%w: %d bytes, and the file header is %d", ErrBadHeader, len(b), DiskSubHeaderSize)
	}
	return DiskSubHeader{
		SessionStartDate:   time.Unix(int64(binary.LittleEndian.Uint64(b[0:])), 0).UTC(),
		SessionStartTime:   float64frombits(b[8:]),
		SessionEndTime:     float64frombits(b[16:]),
		SessionLapCount:    int(int32(binary.LittleEndian.Uint32(b[24:]))),
		SessionRecordCount: int(int32(binary.LittleEndian.Uint32(b[28:]))),
	}, nil
}
