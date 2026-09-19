package iracing

import (
	"context"
	"time"

	"github.com/pacenote-sim/clientplugin"
	"github.com/pacenote-sim/protocol/wire"

	"github.com/pacenote-sim/client-iracing/internal/irsdk"
)

// The unexported parts, for the tests.

// Live is the live memory as the source sees it.
type Live = live

// NewLive is a source reading live iRacing through the given checks, for a
// test that stands in for the memory.
func NewLive(running func() bool, open func() (Live, error)) *Source {
	s := New()
	s.File = ""
	s.running = running
	s.openLive = open
	return s
}

// NewFrom is New reading the given environment.
func NewFrom(getenv func(string) string) *Source { return newFrom(getenv) }

// ToSample is toSample with the session's facts worked out for it, which is
// what a reader hands over.
func ToSample(v irsdk.Values, s irsdk.Session, at time.Time) clientplugin.Sample {
	return toSample(v, factsOf(s, v.Int(varSessionNum)), at)
}

// Facts is the part of a sample that comes from the session text.
type Facts = facts

// CachedFacts keeps them until the session changes.
type CachedFacts = cachedFacts

// FactsOf reads them out of the session text.
func FactsOf(s irsdk.Session, sessionNum int) Facts { return factsOf(s, sessionNum) }

// ToSampleWith is toSample as a reader calls it: with the facts already in
// hand, which is the cost a tick actually has.
func ToSampleWith(v irsdk.Values, f Facts, at time.Time) clientplugin.Sample {
	return toSample(v, f, at)
}

// SessionKind is sessionKind.
func SessionKind(name string) wire.SessionType { return sessionKind(name) }

// FlagOf is flagOf.
func FlagOf(bits uint32) wire.Flag { return flagOf(bits) }

// SleepFor is sleep.
func SleepFor(ctx context.Context, d time.Duration) error { return sleep(ctx, d) }

// The facts a test reads back.

// Of is cachedFacts.of.
func (c *CachedFacts) Of(s irsdk.Session, sessionNum int, fresh bool) Facts {
	return c.of(s, sessionNum, fresh)
}

// Name is the simulator's own name for the session.
func (f Facts) Name() string { return f.name }

// Track is the circuit, for a person.
func (f Facts) Track() string { return f.track }

// Sectors are the sector starts.
func (f Facts) Sectors() []float64 { return f.sectors }
