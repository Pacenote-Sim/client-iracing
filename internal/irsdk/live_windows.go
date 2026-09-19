//go:build windows

package irsdk

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The names iRacing publishes under, from its SDK.
const (
	mappingName = `Local\IRSDKMemMapFileName`
	eventName   = `Local\IRSDKDataValidEvent`
)

// openFileMappingW is not in x/sys/windows; kernel32 has it.
var (
	kernel32            = windows.NewLazySystemDLL("kernel32.dll")
	procOpenFileMapping = kernel32.NewProc("OpenFileMappingW")
)

func openFileMapping(name string) (windows.Handle, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	h, _, callErr := procOpenFileMapping.Call(uintptr(windows.FILE_MAP_READ), 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return 0, callErr
	}
	return windows.Handle(h), nil
}

// Running reports that iRacing has its shared memory open. It opens and
// closes the mapping and is cheap enough to poll.
func Running() bool {
	h, err := openFileMapping(mappingName)
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(h)
	return true
}

// Live is iRacing's shared memory, mapped read-only, and the event it
// signals at every tick.
type Live struct {
	mapping windows.Handle
	event   windows.Handle
	base    uintptr
	mem     []byte
}

// OpenLive maps the shared memory. iRacing must be running.
func OpenLive() (*Live, error) {
	mapping, err := openFileMapping(mappingName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotRunning, err)
	}
	base, err := windows.MapViewOfFile(mapping, windows.FILE_MAP_READ, 0, 0, 0)
	if err != nil {
		_ = windows.CloseHandle(mapping)
		return nil, fmt.Errorf("irsdk: mapping the shared memory: %w", err)
	}
	var info windows.MemoryBasicInformation
	if err := windows.VirtualQuery(base, &info, unsafe.Sizeof(info)); err != nil {
		_ = windows.UnmapViewOfFile(base)
		_ = windows.CloseHandle(mapping)
		return nil, fmt.Errorf("irsdk: sizing the shared memory: %w", err)
	}
	l := &Live{mapping: mapping, base: base, mem: unsafe.Slice((*byte)(pointer(base)), info.RegionSize)}
	name, _ := windows.UTF16PtrFromString(eventName)
	if ev, err := windows.OpenEvent(windows.SYNCHRONIZE, false, name); err == nil {
		l.event = ev // without it, Wait sleeps a tick instead
	}
	return l, nil
}

// Header is the header as it stands now.
func (l *Live) Header() (Header, error) { return ParseHeader(l.mem) }

// Table is the variable table as it stands now.
func (l *Live) Table() (*Table, error) {
	h, err := l.Header()
	if err != nil {
		return nil, err
	}
	vars, err := ParseVars(l.mem, h)
	if err != nil {
		return nil, err
	}
	return NewTable(vars), nil
}

// SessionRaw is the session text as it stands now, copied, and its update
// count, so the caller can tell whether it changed.
func (l *Live) SessionRaw() ([]byte, int, error) {
	h, err := l.Header()
	if err != nil {
		return nil, 0, err
	}
	end := h.SessionInfoOffset + h.SessionInfoLen
	if end > len(l.mem) {
		return nil, 0, fmt.Errorf("%w: the session text runs past the mapping", ErrBadHeader)
	}
	out := make([]byte, h.SessionInfoLen)
	copy(out, l.mem[h.SessionInfoOffset:end])
	return out, h.SessionInfoUpdate, nil
}

// Wait blocks until iRacing signals a new tick, the timeout passes, or ctx
// ends. Without the event, it sleeps one tick.
func (l *Live) Wait(ctx context.Context, timeout time.Duration) error {
	if l.event == 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second / 60):
			return nil
		}
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		slice := min(time.Until(deadline), 100*time.Millisecond)
		if slice <= 0 {
			return ErrTimeout
		}
		ev, err := windows.WaitForSingleObject(l.event, uint32(slice/time.Millisecond))
		switch {
		case err != nil:
			return fmt.Errorf("irsdk: waiting for a tick: %w", err)
		case ev == windows.WAIT_OBJECT_0:
			return nil
		case ev == uint32(windows.WAIT_TIMEOUT):
			continue
		default:
			return fmt.Errorf("irsdk: waiting for a tick: wait returned %d", ev)
		}
	}
}

// Tick copies the newest tick into row, which must be BufLen long, and
// returns the header it was read under. iRacing may overwrite a buffer
// while it is copied; the tick count is checked after the copy and the copy
// retried, as the SDK does.
func (l *Live) Tick(row []byte) (Header, error) {
	for range 8 {
		h, err := l.Header()
		if err != nil {
			return Header{}, err
		}
		if !h.Connected() {
			return h, ErrNotRunning
		}
		buf := h.Newest()
		end := buf.BufOffset + h.BufLen
		if end > len(l.mem) || len(row) < h.BufLen {
			return h, fmt.Errorf("%w: a tick runs past the mapping", ErrBadHeader)
		}
		copy(row, l.mem[buf.BufOffset:end])
		again, err := l.Header()
		if err != nil {
			return Header{}, err
		}
		if again.Newest().TickCount == buf.TickCount {
			return h, nil
		}
	}
	return Header{}, errors.New("irsdk: the tick kept changing while it was read")
}

// Close unmaps the memory.
func (l *Live) Close() error {
	var errs []error
	if l.event != 0 {
		errs = append(errs, windows.CloseHandle(l.event))
	}
	errs = append(errs, windows.UnmapViewOfFile(l.base), windows.CloseHandle(l.mapping))
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("irsdk: closing: %w", err)
	}
	return nil
}

// pointer is the address as a pointer. The view stays mapped for as long as
// the pointer is used, so the address is stable; going through the address's
// own storage is what keeps vet from reading it as a pointer laundered
// through an integer.
func pointer(addr uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&addr)) }
