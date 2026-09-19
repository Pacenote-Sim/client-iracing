//go:build !windows

package irsdk

import (
	"context"
	"time"
)

// Running reports that iRacing is publishing. iRacing runs on Windows only,
// so here it never is.
func Running() bool { return false }

// Live is iRacing's shared memory. It exists here so the source compiles
// everywhere; OpenLive always fails.
type Live struct{}

// OpenLive fails: there is no iRacing on this operating system.
func OpenLive() (*Live, error) { return nil, ErrNotRunning }

// Header implements the Windows Live's method, and is never reached.
func (*Live) Header() (Header, error) { return Header{}, ErrNotRunning }

// Table implements the Windows Live's method, and is never reached.
func (*Live) Table() (*Table, error) { return nil, ErrNotRunning }

// SessionRaw implements the Windows Live's method, and is never reached.
func (*Live) SessionRaw() ([]byte, int, error) { return nil, 0, ErrNotRunning }

// Wait implements the Windows Live's method, and is never reached.
func (*Live) Wait(context.Context, time.Duration) error { return ErrNotRunning }

// Tick implements the Windows Live's method, and is never reached.
func (*Live) Tick([]byte) (Header, error) { return Header{}, ErrNotRunning }

// Close implements the Windows Live's method, and is never reached.
func (*Live) Close() error { return nil }
