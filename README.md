# client-iracing

The Pacenote client's source for iRacing. It reads what iRacing publishes while it runs, sixty
times a second, and turns each reading into a sample the client understands. Everything else
— laps, corners, uploads, the companions — is the client's.

It is a Go module that registers itself as a source when imported: a client has it because the
build that made that client imported it. Nothing is loaded at runtime.

## What is here

| | |
|---|---|
| the package itself | the `clientplugin.Source`: open, read, close, and a tick turned into a sample in the client's units |
| `internal/irsdk` | iRacing's own layout in Go with no C: the header, the variable table, the session text, a tick. The same decoder reads the live memory and a `.ibt` file, and writes one |
| `cmd/ibt-dump` | prints what is in a telemetry file: the header, the session, every variable, and ticks |

## What it reads

iRacing publishes its state in shared memory, `Local\IRSDKMemMapFileName`, and signals every new
tick on `Local\IRSDKDataValidEvent`. The layout is iRacing's own, from the header file its SDK
ships: a header, a table of variables, the session text as YAML, and up to four buffers holding
the latest ticks. This module reads that layout in Go with no C, so the client cross-compiles.

From each tick: the session clock and number, on track and on the pit road, lap and lap
distance, speed, throttle, brake, gear, revs, steering, lateral and longitudinal acceleration,
position on the earth, last and best lap, incidents, place, fuel and burn rate, the sky, the wind,
humidity, track and air temperature, the four tyre temperatures, and the flags. From the session
text: the circuit and its length, the car and its class, the sectors, the session type and length,
and whether the setup is fixed.

Units are converted to the client's: kilometres per hour, degrees with left negative, g,
milliseconds, percent. iRacing's `-1` for a lap not yet set is no time.

A variable a build or a car does not publish reads as zero. The table and the session text are
re-read when iRacing says they changed.

## Telemetry files

iRacing writes `.ibt` files to `Documents\iRacing\telemetry` when telemetry is on. They have the
same layout with the ticks laid end to end, and this module plays them:

```
PACENOTE_IRACING_IBT=~/session.ibt PACENOTE_IRACING_SPEED=3 pacenote --server http://localhost:8080 --data ./data
```

The file plays once, at the pace it was driven or faster, stamped with the session's own clock,
and then the source reports not running. It is how the module is developed and tested on a
machine without iRacing, and how a driver's recording is replayed for a support case.

## Recording a session

```
PACENOTE_IRACING_RECORD=C:\pacenote-recordings pacenote.exe --server https://your.server
```

writes every live session to that directory as `iracing-<date>.ibt` and `iracing-<date>.yaml`, in
iRacing's own layout, so that what the machine read can be played back here through the same
decoder, or opened in any tool that reads `.ibt`. The `.yaml` is rewritten whenever iRacing
changes the session text. A recording that fails stops, the reading goes on, and the source's
close reports it.

## Looking inside a file

```
go run ./cmd/ibt-dump session.ibt              # header, session, every variable
go run ./cmd/ibt-dump -ticks 10 session.ibt    # and ten ticks, one a second
go run ./cmd/ibt-dump -yaml session.ibt        # the session text as iRacing wrote it
```

It is what a tester runs on their file when something reads wrongly.

## Testing it

```
make check          # format, build for this OS and for Windows, vet, lint, test, coverage, tidy
make bench          # what a tick costs
```

The decoder is tested on files this module writes itself in iRacing's layout, and on a real `.ibt`
when `PACENOTE_IRACING_FIXTURE` names one. The live memory cannot be opened without iRacing, so it
is tested against a stand-in and run on a machine that has the game. `TESTING.md` has the rest.

## Licence

GNU General Public License, version 3 — see `LICENSE` — with the Pacenote Plugin Exception in
`LICENSE-EXCEPTION`, the same one the client carries. It lets this plugin be compiled into a client
alongside plugins under other licences, including closed ones, and lets that client be handed out
under those plugins' own terms while this plugin's part stays GPL. The contract it is built on
(`github.com/pacenote-sim/clientplugin`) is Apache-2.0.
