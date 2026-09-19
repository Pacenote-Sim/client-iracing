package iracing

import (
	"math"
	"strings"
	"time"

	"github.com/pacenote-sim/clientplugin"
	"github.com/pacenote-sim/protocol/wire"

	"github.com/pacenote-sim/client-iracing/internal/irsdk"
)

// The variables read from a tick, by iRacing's names. Units are iRacing's:
// metres per second, radians, metres per second squared, seconds, litres.
const (
	varSessionTime  = "SessionTime"
	varSessionNum   = "SessionNum"
	varSessionFlags = "SessionFlags"
	varIsOnTrack    = "IsOnTrack"
	varOnPitRoad    = "OnPitRoad"
	varLap          = "Lap"
	varLapDistPct   = "LapDistPct"
	varSpeed        = "Speed"
	varThrottle     = "Throttle"
	varBrake        = "Brake"
	varGear         = "Gear"
	varRPM          = "RPM"
	varSteer        = "SteeringWheelAngle"
	varLatAccel     = "LatAccel"
	varLongAccel    = "LongAccel"
	varLat          = "Lat"
	varLon          = "Lon"
	varLastLap      = "LapLastLapTime"
	varBestLap      = "LapBestLapTime"
	varIncidents    = "PlayerCarMyIncidentCount"
	varPosition     = "PlayerCarPosition"
	varFuel         = "FuelLevel"
	varFuelPerHour  = "FuelUsePerHour"
	varSkies        = "Skies"
	varWetness      = "TrackWetness"
	varWind         = "WindVel"
	varHumidity     = "RelativeHumidity"
	varTrackTemp    = "TrackTempCrew"
	varAirTemp      = "AirTemp"
	varLFtemp       = "LFtempCM"
	varRFtemp       = "RFtempCM"
	varLRtemp       = "LRtempCM"
	varRRtemp       = "RRtempCM"
)

// iRacing's session flag bits, from its SDK.
const (
	flagCheckered     = 0x00000001
	flagWhite         = 0x00000002
	flagGreen         = 0x00000004
	flagYellow        = 0x00000008
	flagRed           = 0x00000010
	flagYellowWaving  = 0x00000100
	flagGreenHeld     = 0x00000400
	flagCaution       = 0x00004000
	flagCautionWaving = 0x00008000
	flagBlack         = 0x00010000
	flagDisqualify    = 0x00020000
)

const gravity = 9.80665

// toSample is one tick in the client's units.
func toSample(v irsdk.Values, f facts, at time.Time) clientplugin.Sample {
	out := clientplugin.Sample{
		At:           at,
		Session:      f.kind,
		SessionName:  f.name,
		Track:        f.track,
		TrackID:      f.trackID,
		TrackLengthM: f.lengthM,
		Car:          f.car,
		CarClass:     f.carClass,
		Sectors:      f.sectors,
		SetupOpen:    f.setupOpen,

		OnTrack:    v.Bool(varIsOnTrack),
		OnPitRoad:  v.Bool(varOnPitRoad),
		Lap:        v.Int(varLap),
		LapDistPct: clamp01(v.Float(varLapDistPct)),
		SpeedKmh:   math.Max(v.Float(varSpeed), 0) * 3.6,
		Throttle:   clamp01(v.Float(varThrottle)),
		Brake:      clamp01(v.Float(varBrake)),
		Gear:       v.Int(varGear),
		RPM:        math.Max(v.Float(varRPM), 0),
		// iRacing's angle is positive turning left; the client's left is negative.
		SteerDeg: -v.Float(varSteer) * 180 / math.Pi,
		LatG:     v.Float(varLatAccel) / gravity,
		LongG:    v.Float(varLongAccel) / gravity,
		Lat:      v.Float(varLat),
		Lon:      v.Float(varLon),

		LastLapMs: lapMs(v.Float(varLastLap)),
		BestLapMs: lapMs(v.Float(varBestLap)),
		Incidents: v.Int(varIncidents),
		Position:  max(v.Int(varPosition), 0),
		FuelL:     math.Max(v.Float(varFuel), 0),
		// iRacing's burn rate is kilograms an hour; the density is in the text.
		FuelPerHourL: math.Max(v.Float(varFuelPerHour), 0) / f.fuelKgPerLitre,
		Weather: clientplugin.Weather{
			Skies:      v.Int(varSkies),
			Wetness:    v.Int(varWetness),
			WindKmh:    math.Max(v.Float(varWind), 0) * 3.6,
			Humidity:   clamp01(v.Float(varHumidity)) * 100,
			TrackTempC: v.Float(varTrackTemp),
			AirTempC:   v.Float(varAirTemp),
		},
		Tyres: clientplugin.Tyres{
			LF: v.Float(varLFtemp), RF: v.Float(varRFtemp), LR: v.Float(varLRtemp), RR: v.Float(varRRtemp),
		},
		Flag:      flagOf(v.Bits(varSessionFlags)),
		LapsTotal: f.lapsTotal,
	}
	return out
}

// facts are the parts of a sample that come from the session text rather than
// from the tick: the circuit, the car, the sectors, what kind of session it is.
//
// They are the same on every tick until iRacing rewrites the text or moves to
// the next session of the weekend, and working them out costs more than the
// whole of the rest of a sample — a string lowered, a slice copied, two strings
// joined. Sixty times a second, for an hour of practice, that is a quarter of a
// million allocations nobody needs.
type facts struct {
	kind           wire.SessionType
	name           string
	track          string
	trackID        string
	car            string
	carClass       string
	sectors        []float64
	lengthM        int
	lapsTotal      int
	fuelKgPerLitre float64
	setupOpen      bool
}

// factsOf reads them out of the session text, once.
func factsOf(s irsdk.Session, sessionNum int) facts {
	name := s.Type(sessionNum)
	car, class := s.Car()
	return facts{
		kind: sessionKind(name), name: name,
		track: s.Track(), trackID: s.WeekendInfo.TrackName, lengthM: s.TrackLengthM(),
		car: car, carClass: class, sectors: s.Sectors(),
		setupOpen: s.WeekendInfo.WeekendOptions.IsFixedSetup == 0,
		lapsTotal: s.Laps(sessionNum), fuelKgPerLitre: s.FuelKgPerLitre(),
	}
}

// cachedFacts keeps the last ones worked out, and works them out again when
// the session text changes or the weekend moves to the next session.
type cachedFacts struct {
	f    facts
	num  int
	read bool
}

// of is the facts for this tick's session, computed only when they can have
// changed. fresh is a session text that has just been read again.
func (c *cachedFacts) of(s irsdk.Session, sessionNum int, fresh bool) facts {
	if !c.read || fresh || c.num != sessionNum {
		c.f, c.num, c.read = factsOf(s, sessionNum), sessionNum, true
	}
	return c.f
}

// sessionKind is the client's session type for iRacing's name.
func sessionKind(name string) wire.SessionType {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "race"):
		return wire.SessionRace
	case strings.Contains(n, "qualif"):
		return wire.SessionQualifying
	case strings.Contains(n, "testing"):
		return wire.SessionTesting
	default:
		// Practice, Open Practice, Warmup, and anything new.
		return wire.SessionPractice
	}
}

// flagOf is the one flag that matters most of those flying, or "".
func flagOf(bits uint32) wire.Flag {
	switch {
	case bits&flagCheckered != 0:
		return wire.FlagCheckered
	case bits&flagRed != 0:
		return wire.FlagRed
	case bits&(flagBlack|flagDisqualify) != 0:
		return wire.FlagBlack
	case bits&(flagYellow|flagYellowWaving|flagCaution|flagCautionWaving) != 0:
		return wire.FlagYellow
	case bits&flagWhite != 0:
		return wire.FlagWhite
	case bits&(flagGreen|flagGreenHeld) != 0:
		return wire.FlagGreen
	default:
		return ""
	}
}

// lapMs is a lap time in seconds as milliseconds; iRacing's -1 for none is 0.
func lapMs(seconds float64) int {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0
	}
	return int(seconds*1000 + 0.5)
}

// clamp01 holds a fraction to 0…1; NaN is 0.
func clamp01(x float64) float64 {
	if math.IsNaN(x) {
		return 0
	}
	return math.Min(math.Max(x, 0), 1)
}
