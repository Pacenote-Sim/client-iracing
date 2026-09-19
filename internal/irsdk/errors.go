package irsdk

import "errors"

var (
	// ErrNotRunning reports that iRacing is not publishing: not started, or
	// started and between sessions.
	ErrNotRunning = errors.New("irsdk: iRacing is not running a session")
	// ErrTimeout reports that no tick came in the time given.
	ErrTimeout = errors.New("irsdk: no tick arrived in time")
)
