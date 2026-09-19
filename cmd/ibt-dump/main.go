// ibt-dump reads one of iRacing's .ibt telemetry files and prints what is in
// it: the header, the session, every variable, and the first ticks of the
// variables the client uses. It is what a tester runs on their file when
// something reads wrongly, and what the decoder is checked against.
//
//	ibt-dump session.ibt            the header, the session and the variables
//	ibt-dump -ticks 5 session.ibt   and the first five ticks
//	ibt-dump -yaml session.ibt      the session text as iRacing wrote it
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pacenote-sim/client-iracing/internal/irsdk"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ibt-dump", flag.ContinueOnError)
	fs.SetOutput(out)
	ticks := fs.Int("ticks", 0, "print this many ticks of the variables the client reads")
	every := fs.Int("every", 60, "with -ticks, print one tick in this many")
	yaml := fs.Bool("yaml", false, "print the session text and nothing else")
	if err := fs.Parse(args); err != nil {
		return err //nolint:wrapcheck // flag's own message
	}
	if fs.NArg() != 1 {
		return errors.New("usage: ibt-dump [-ticks n] [-every n] [-yaml] file.ibt")
	}
	f, err := irsdk.OpenFile(fs.Arg(0))
	if err != nil {
		return err //nolint:wrapcheck // the reader's own words
	}
	defer f.Close() //nolint:errcheck // read only

	if *yaml {
		_, err := out.Write(f.SessionRaw)
		return err //nolint:wrapcheck // the writer's own words
	}

	h := f.Header
	fmt.Fprintf(out, "header: version %d, status %d, %d Hz, %d variables, tick %d bytes, %d buffer(s)\n",
		h.Ver, h.Status, h.TickRate, h.NumVars, h.BufLen, h.NumBuf)
	fmt.Fprintf(out, "file: %d ticks, %d laps, session clock %.1f to %.1f s, started %s\n",
		f.Sub.SessionRecordCount, f.Sub.SessionLapCount, f.Sub.SessionStartTime, f.Sub.SessionEndTime,
		f.Sub.SessionStartDate.Format("2006-01-02 15:04:05 UTC"))
	s := f.Session
	car, class := s.Car()
	fmt.Fprintf(out, "session: %s (%s), %d m, car %q class %q, setup fixed %d, sectors %v\n",
		s.Track(), s.WeekendInfo.TrackName, s.TrackLengthM(), car, class, s.WeekendInfo.WeekendOptions.IsFixedSetup, s.Sectors())
	for _, x := range s.SessionInfo.Sessions {
		fmt.Fprintf(out, "  session %d: %s, laps %s\n", x.SessionNum, x.SessionType, x.SessionLaps)
	}

	fmt.Fprintf(out, "\nvariables:\n")
	for _, v := range f.Table.Vars() {
		count := ""
		if v.Count > 1 {
			count = fmt.Sprintf("[%d]", v.Count)
		}
		fmt.Fprintf(out, "  %-32s %-8s%-6s @%-5d %-14s %s\n", v.Name, v.Type, count, v.Offset, v.Unit, v.Desc)
	}

	if *ticks <= 0 {
		return nil
	}
	names := []string{
		"SessionTime", "SessionNum", "Lap", "LapDistPct", "Speed", "Throttle", "Brake", "Gear", "RPM",
		"SteeringWheelAngle", "IsOnTrack", "OnPitRoad", "LapLastLapTime", "FuelLevel", "SessionFlags",
	}
	fmt.Fprintf(out, "\nticks, one in %d:\n", max(*every, 1))
	fmt.Fprintln(out, strings.Join(names, "\t"))
	printed := 0
	for i := 0; printed < *ticks; i++ {
		v, err := f.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err //nolint:wrapcheck // the reader's own words
		}
		if i%max(*every, 1) != 0 {
			continue
		}
		fields := make([]string, 0, len(names))
		for _, n := range names {
			switch {
			case !v.Has(n):
				fields = append(fields, "-")
			case n == "SessionFlags":
				fields = append(fields, fmt.Sprintf("%#x", v.Bits(n)))
			default:
				fields = append(fields, fmt.Sprintf("%.4g", v.Float(n)))
			}
		}
		fmt.Fprintln(out, strings.Join(fields, "\t"))
		printed++
	}
	return nil
}
