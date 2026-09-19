package irsdk

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ErrBadSession reports session text that could not be read.
var ErrBadSession = errors.New("irsdk: the session text could not be read")

// Session is the part of iRacing's session text this client uses. iRacing
// writes far more; what is not named here is ignored, and a field missing
// from a build or a car reads as its zero.
type Session struct {
	WeekendInfo struct {
		// TrackName is iRacing's stable identifier, "spa gp";
		// TrackDisplayName and TrackConfigName are for people. TrackLength
		// is a distance with a unit, "7.00 km".
		TrackName        string `yaml:"TrackName"`
		TrackDisplayName string `yaml:"TrackDisplayName"`
		TrackConfigName  string `yaml:"TrackConfigName"`
		TrackLength      string `yaml:"TrackLength"`
		TrackID          int    `yaml:"TrackID"`
		WeekendOptions   struct {
			// IsFixedSetup is 1 when the car cannot be changed.
			IsFixedSetup int `yaml:"IsFixedSetup"`
		} `yaml:"WeekendOptions"`
	} `yaml:"WeekendInfo"`
	SessionInfo struct {
		Sessions []struct {
			SessionNum int `yaml:"SessionNum"`
			// SessionType is "Practice", "Lone Qualify", "Race", "Offline Testing"…
			SessionType string `yaml:"SessionType"`
			// SessionLaps is a number, or "unlimited".
			SessionLaps string `yaml:"SessionLaps"`
		} `yaml:"Sessions"`
	} `yaml:"SessionInfo"`
	DriverInfo struct {
		// DriverCarIdx is the driver's own car in Drivers. DriverCarFuelKgPerLtr
		// is the fuel's density, which turns iRacing's burn rate in kilograms
		// an hour into litres; DriverCarFuelMaxLtr the tank.
		DriverCarIdx          int     `yaml:"DriverCarIdx"`
		DriverCarFuelKgPerLtr float64 `yaml:"DriverCarFuelKgPerLtr"`
		DriverCarFuelMaxLtr   float64 `yaml:"DriverCarFuelMaxLtr"`
		Drivers               []struct {
			CarIdx            int    `yaml:"CarIdx"`
			UserName          string `yaml:"UserName"`
			CarNumber         string `yaml:"CarNumber"`
			CarScreenName     string `yaml:"CarScreenName"`
			CarPath           string `yaml:"CarPath"`
			CarClassShortName string `yaml:"CarClassShortName"`
			CarClassID        int    `yaml:"CarClassID"`
		} `yaml:"Drivers"`
	} `yaml:"DriverInfo"`
	SplitTimeInfo struct {
		Sectors []struct {
			SectorNum      int     `yaml:"SectorNum"`
			SectorStartPct float64 `yaml:"SectorStartPct"`
		} `yaml:"Sectors"`
	} `yaml:"SplitTimeInfo"`
}

// ParseSession reads the session text. iRacing writes it in Latin-1 and pads
// it with NULs; both are dealt with here.
func ParseSession(raw []byte) (Session, error) {
	if i := bytes.IndexByte(raw, 0); i >= 0 {
		raw = raw[:i]
	}
	var s Session
	if err := yaml.Unmarshal(latin1(raw), &s); err != nil {
		return Session{}, fmt.Errorf("%w: %w", ErrBadSession, err)
	}
	return s, nil
}

// latin1 is Latin-1 bytes as UTF-8: every byte is one rune of the same value.
func latin1(b []byte) []byte {
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return b
	}
	out := make([]byte, 0, len(b)+len(b)/8)
	for _, c := range b {
		out = append(out, string(rune(c))...)
	}
	return out
}

// TrackLengthM is the circuit length in metres, from "7.00 km" or "4.35 mi";
// zero when the text has none.
func (s Session) TrackLengthM() int {
	f := strings.Fields(s.WeekendInfo.TrackLength)
	if len(f) == 0 {
		return 0
	}
	n, err := strconv.ParseFloat(f[0], 64)
	if err != nil || n <= 0 {
		return 0
	}
	unit := "km"
	if len(f) > 1 {
		unit = strings.ToLower(f[1])
	}
	switch unit {
	case "km":
		return int(n*1000 + 0.5)
	case "mi", "miles":
		return int(n*1609.344 + 0.5)
	case "m":
		return int(n + 0.5)
	default:
		return 0
	}
}

// Type is the session type by number, "" when unknown.
func (s Session) Type(sessionNum int) string {
	for _, x := range s.SessionInfo.Sessions {
		if x.SessionNum == sessionNum {
			return x.SessionType
		}
	}
	return ""
}

// Laps is the session's length in laps by number, zero when unlimited or unknown.
func (s Session) Laps(sessionNum int) int {
	for _, x := range s.SessionInfo.Sessions {
		if x.SessionNum == sessionNum {
			n, err := strconv.Atoi(strings.TrimSpace(x.SessionLaps))
			if err != nil || n <= 0 || n >= 32767 {
				return 0
			}
			return n
		}
	}
	return 0
}

// FuelKgPerLitre is the fuel's density, petrol's 0.75 when the text has none.
func (s Session) FuelKgPerLitre() float64 {
	if d := s.DriverInfo.DriverCarFuelKgPerLtr; d > 0 {
		return d
	}
	return DefaultFuelKgPerLitre
}

// DefaultFuelKgPerLitre is petrol, for a session text without a density.
const DefaultFuelKgPerLitre = 0.75

// Car is the driver's own car's screen name and class, "" when unknown.
func (s Session) Car() (name, class string) {
	for _, d := range s.DriverInfo.Drivers {
		if d.CarIdx == s.DriverInfo.DriverCarIdx {
			return d.CarScreenName, d.CarClassShortName
		}
	}
	return "", ""
}

// Track is the circuit's name for people: the display name with its
// configuration when there is one, "Circuit de Spa-Francorchamps - Grand Prix".
func (s Session) Track() string {
	name := s.WeekendInfo.TrackDisplayName
	if cfg := s.WeekendInfo.TrackConfigName; cfg != "" && name != "" {
		return name + " - " + cfg
	}
	return name
}

// Sectors are the sector starts as fractions of the lap, in order; the first
// is 0 when the text has any.
func (s Session) Sectors() []float64 {
	if len(s.SplitTimeInfo.Sectors) == 0 {
		return nil
	}
	out := make([]float64, 0, len(s.SplitTimeInfo.Sectors))
	for _, x := range s.SplitTimeInfo.Sectors {
		out = append(out, x.SectorStartPct)
	}
	return out
}
