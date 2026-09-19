# Testing the iRacing source

`make` runs what CI runs, in order: format, build for this machine and for Windows, vet, lint, the
suite with the race detector and shuffled order, coverage over 90 %, every benchmark once, and
`go mod tidy` a no-op.

```
make            # everything, in order
make test       # the suite alone
make cover      # the suite with the coverage floor
make bench      # the benchmarks, properly
```

## What the suite asserts

| | |
|---|---|
| the layout | a header, a sub-header and a variable table written by this package and read back, with every offset asserted against the numbers in iRacing's own SDK header |
| what is not the layout | bytes that are not a header, a table that runs past the file, a variable that does not fit a tick, a session text that is not YAML: each refused with a reason |
| every type | char, bool, int, bitfield, float and double, arrays and their bounds, and a short row read as zeros rather than past its end |
| the session text | Latin-1 as UTF-8, the circuit and its length in three units, the car, the sectors, the session type and length, the fuel density |
| a file | every tick in order, then `io.EOF`; a file cut short ends where the bytes do; a file whose text will not parse still plays |
| a recording | what a live session writes, read back through the same decoder, including one a crash never closed |
| the source | a tick in the client's units, a file played at speed, the environment that names one, and a lap of a real file end to end when one is at hand |
| the live memory | against a stand-in: ticks as the event fires, the table and the text re-read when iRacing says they changed, a quiet spell checked against the header, and a closed session ending the read |

The live memory itself cannot be opened on a machine without iRacing. It is cross-compiled for
Windows by `make build`, tested against a stand-in here, and run on a driver's machine.

## Against a real telemetry file

Most of the suite runs on files this package writes in iRacing's layout. Point it at a real one and
the source is tested end to end on real bytes instead:

```
PACENOTE_IRACING_FIXTURE=/path/to/session.ibt make test
```

It was last checked against a Mazda MX-5 at Okayama: 272 variables, 47 329 ticks, 8 laps, every one
read. A real file is not in this repository, so without that variable the test skips.

To see what is in a file:

```
go run ./cmd/ibt-dump session.ibt              # header, session, every variable
go run ./cmd/ibt-dump -ticks 10 session.ibt    # and ten ticks, one a second
go run ./cmd/ibt-dump -yaml session.ibt        # the session text as iRacing wrote it
```

## What a tick costs

iRacing publishes sixty ticks a second and the client turns every one into a sample. On an Apple M5:

| | |
|---|---|
| reading a value out of a tick | 75 ns, no allocations |
| a tick to a sample | 1.1 µs, no allocations |
| parsing a header and its variable table | 903 ns, once when a file or a session opens |
| reading the session text | 40 µs, when iRacing says it changed |

The session text is the expensive one, and it is read once a session rather than once a tick: the
circuit, the car, the sectors and the session type are worked out when they can have changed and
kept until they do. That is the difference between no allocations a tick and four.
