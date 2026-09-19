package irsdk

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

// VarType is how a variable is stored in a tick.
type VarType int

// The types, in iRacing's numbering.
const (
	Char VarType = iota
	Bool
	Int
	BitField
	Float
	Double
)

// Size is the bytes one element of the type takes.
func (t VarType) Size() int {
	switch t {
	case Char, Bool:
		return 1
	case Int, BitField, Float:
		return 4
	case Double:
		return 8
	default:
		return 0
	}
}

// String is the type's name in the SDK.
func (t VarType) String() string {
	switch t {
	case Char:
		return "char"
	case Bool:
		return "bool"
	case Int:
		return "int"
	case BitField:
		return "bitfield"
	case Float:
		return "float"
	case Double:
		return "double"
	default:
		return fmt.Sprintf("type %d", int(t))
	}
}

// Var is one variable's header: what it is and where it sits in a tick.
type Var struct {
	Type VarType
	// Offset is bytes from the start of a tick; Count elements sit there.
	Offset, Count int
	// CountAsTime marks an array indexed by time rather than by car.
	CountAsTime bool
	Name, Desc  string
	Unit        string
}

// ParseVars reads the variable table the header describes from b, the whole
// mapping or file head.
func ParseVars(b []byte, h Header) ([]Var, error) {
	end := h.VarHeaderOffset + h.NumVars*VarHeaderSize
	if end > len(b) {
		return nil, fmt.Errorf("%w: the variable table ends at %d, past %d bytes", ErrBadHeader, end, len(b))
	}
	vars := make([]Var, 0, h.NumVars)
	for i := range h.NumVars {
		off := h.VarHeaderOffset + i*VarHeaderSize
		v := Var{
			Type:        VarType(int32(binary.LittleEndian.Uint32(b[off:]))),
			Offset:      int(int32(binary.LittleEndian.Uint32(b[off+4:]))),
			Count:       int(int32(binary.LittleEndian.Uint32(b[off+8:]))),
			CountAsTime: b[off+12] != 0,
			Name:        cstring(b[off+16 : off+16+MaxString]),
			Desc:        cstring(b[off+48 : off+48+MaxDesc]),
			Unit:        cstring(b[off+112 : off+112+MaxString]),
		}
		if v.Type.Size() == 0 || v.Count <= 0 || v.Offset < 0 || v.Offset+v.Count*v.Type.Size() > h.BufLen {
			return nil, fmt.Errorf("%w: variable %q does not fit a tick", ErrBadHeader, v.Name)
		}
		vars = append(vars, v)
	}
	return vars, nil
}

// cstring is a NUL-padded field as a string.
func cstring(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// Table is a variable table indexed by name.
type Table struct {
	vars  []Var
	index map[string]int
}

// NewTable indexes a table. A name that appears twice keeps its first entry.
func NewTable(vars []Var) *Table {
	t := &Table{vars: vars, index: make(map[string]int, len(vars))}
	for i, v := range vars {
		if _, dup := t.index[v.Name]; !dup {
			t.index[v.Name] = i
		}
	}
	return t
}

// Vars is the table in the order published.
func (t *Table) Vars() []Var { return t.vars }

// Lookup is the variable by name.
func (t *Table) Lookup(name string) (Var, bool) {
	i, ok := t.index[name]
	if !ok {
		return Var{}, false
	}
	return t.vars[i], true
}

// Values is one tick, read through its table. A variable the table does not
// have reads as zero: iRacing adds and removes variables between builds and
// cars, and a reading with one missing is a reading, not an error.
type Values struct {
	table *Table
	row   []byte
}

// NewValues is a tick over a table. The row is kept, not copied.
func NewValues(table *Table, row []byte) Values { return Values{table: table, row: row} }

// Has reports that the variable is published and fits the tick.
func (v Values) Has(name string) bool {
	x, ok := v.table.Lookup(name)
	return ok && x.Offset+x.Count*x.Type.Size() <= len(v.row)
}

// Float is element i of the variable as a float64, whatever its type; zero
// when absent or out of range.
func (v Values) Float(name string) float64 { return v.FloatAt(name, 0) }

// FloatAt is element i of an array variable.
func (v Values) FloatAt(name string, i int) float64 {
	x, ok := v.table.Lookup(name)
	if !ok || i < 0 || i >= x.Count {
		return 0
	}
	off := x.Offset + i*x.Type.Size()
	if off+x.Type.Size() > len(v.row) {
		return 0
	}
	b := v.row[off:]
	switch x.Type {
	case Char:
		return float64(int8(b[0])) // signed, as C's char is where iRacing builds
	case Bool:
		return float64(b[0])
	case Int, BitField:
		return float64(int32(binary.LittleEndian.Uint32(b)))
	case Float:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	case Double:
		return float64frombits(b)
	default:
		return 0
	}
}

// Int is the variable as an int: the value for the integer types, and the
// float types truncated.
func (v Values) Int(name string) int { return v.IntAt(name, 0) }

// IntAt is element i of an array variable as an int.
func (v Values) IntAt(name string, i int) int {
	f := v.FloatAt(name, i)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int(f)
}

// Bool is the variable as a bool: anything but zero.
func (v Values) Bool(name string) bool { return v.FloatAt(name, 0) != 0 }

// Bits is a bitfield variable, unsigned.
func (v Values) Bits(name string) uint32 {
	x, ok := v.table.Lookup(name)
	if !ok || x.Offset+4 > len(v.row) || x.Type.Size() != 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(v.row[x.Offset:])
}

// Count is how many elements the variable has, zero when absent.
func (v Values) Count(name string) int {
	x, ok := v.table.Lookup(name)
	if !ok {
		return 0
	}
	return x.Count
}

func float64frombits(b []byte) float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(b))
}
