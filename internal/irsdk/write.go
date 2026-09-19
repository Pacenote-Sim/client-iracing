package irsdk

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// WriteFile writes a .ibt file: the header, the sub-header, the variable
// table, the session text and the ticks, in iRacing's layout. It is what the
// tests build their fixtures with until a real file replaces them, and what
// any tool that wants to write iRacing's format from Go can use. Every row
// must be exactly bufLen bytes; the variables must fit it.
func WriteFile(w io.Writer, vars []Var, bufLen int, session []byte, sub DiskSubHeader, rows [][]byte) error {
	if bufLen <= 0 || bufLen > MaxBufLen {
		return fmt.Errorf("irsdk: a tick of %d bytes cannot be written", bufLen)
	}
	for _, v := range vars {
		if v.Type.Size() == 0 || v.Count <= 0 || v.Offset < 0 || v.Offset+v.Count*v.Type.Size() > bufLen {
			return fmt.Errorf("irsdk: variable %q does not fit a tick of %d bytes", v.Name, bufLen)
		}
		if len(v.Name) >= MaxString || len(v.Unit) >= MaxString || len(v.Desc) >= MaxDesc {
			return fmt.Errorf("irsdk: variable %q has a name, unit or description too long", v.Name)
		}
	}
	for i, r := range rows {
		if len(r) != bufLen {
			return fmt.Errorf("irsdk: row %d is %d bytes, not %d", i, len(r), bufLen)
		}
	}

	varOff := HeaderSize + DiskSubHeaderSize
	sessOff := varOff + len(vars)*VarHeaderSize
	bufOff := sessOff + len(session)
	sub.SessionRecordCount = len(rows)

	buf := make([]byte, bufOff)
	put := func(off, v int) { binary.LittleEndian.PutUint32(buf[off:], uint32(int32(v))) }
	put(0, 2) // ver
	put(4, StatusConnected)
	put(8, 60) // tick rate
	put(12, 1) // session info update
	put(16, len(session))
	put(20, sessOff)
	put(24, len(vars))
	put(28, varOff)
	put(32, 1) // one buffer in a file
	put(36, bufLen)
	put(48, len(rows)) // buffer 0 tick count
	put(52, bufOff)

	binary.LittleEndian.PutUint64(buf[HeaderSize:], uint64(sub.SessionStartDate.Unix()))
	binary.LittleEndian.PutUint64(buf[HeaderSize+8:], math.Float64bits(sub.SessionStartTime))
	binary.LittleEndian.PutUint64(buf[HeaderSize+16:], math.Float64bits(sub.SessionEndTime))
	put(HeaderSize+24, sub.SessionLapCount)
	put(HeaderSize+28, sub.SessionRecordCount)

	for i, v := range vars {
		off := varOff + i*VarHeaderSize
		put(off, int(v.Type))
		put(off+4, v.Offset)
		put(off+8, v.Count)
		if v.CountAsTime {
			buf[off+12] = 1
		}
		copy(buf[off+16:off+16+MaxString], v.Name)
		copy(buf[off+48:off+48+MaxDesc], v.Desc)
		copy(buf[off+112:off+112+MaxString], v.Unit)
	}
	copy(buf[sessOff:], session)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("irsdk: writing the file head: %w", err)
	}
	for i, r := range rows {
		if _, err := w.Write(r); err != nil {
			return fmt.Errorf("irsdk: writing tick %d: %w", i, err)
		}
	}
	return nil
}

// Row builds one tick for a table: a BufLen-sized row with the given values
// written in, each by variable name. A value is converted to the variable's
// type. It is the writing half of Values, for fixtures and tools.
type Row struct {
	table *Table
	buf   []byte
}

// NewRow is an empty tick of bufLen bytes over the table.
func NewRow(table *Table, bufLen int) *Row { return &Row{table: table, buf: make([]byte, bufLen)} }

// Set writes element i of a variable. An unknown name is ignored, like a
// missing one is on reading.
func (r *Row) Set(name string, i int, value float64) *Row {
	v, ok := r.table.Lookup(name)
	if !ok || i < 0 || i >= v.Count {
		return r
	}
	off := v.Offset + i*v.Type.Size()
	switch v.Type {
	case Char, Bool:
		r.buf[off] = byte(int(value))
	case Int, BitField:
		binary.LittleEndian.PutUint32(r.buf[off:], uint32(int32(value)))
	case Float:
		binary.LittleEndian.PutUint32(r.buf[off:], math.Float32bits(float32(value)))
	case Double:
		binary.LittleEndian.PutUint64(r.buf[off:], math.Float64bits(value))
	}
	return r
}

// SetBits writes a bitfield whole.
func (r *Row) SetBits(name string, bits uint32) *Row {
	if v, ok := r.table.Lookup(name); ok && v.Type.Size() == 4 {
		binary.LittleEndian.PutUint32(r.buf[v.Offset:], bits)
	}
	return r
}

// Bytes is the tick, copied.
func (r *Row) Bytes() []byte {
	out := make([]byte, len(r.buf))
	copy(out, r.buf)
	return out
}
