# sluice

`sluice` conditionally forwards standard input to standard output. It keeps an
OPEN/CLOSED state and changes state when the configured event arrives. This is
useful between a producer and a consumer when downstream should see data only
during selected windows:

```text
producer | sluice --open signal:USR1 --close signal:USR2 closed | consumer
```

## Installation

Install the latest source version with Go:

```sh
go install github.com/zaubermaerchen/sluice/cmd/sluice@latest
```

To build the checkout in this repository:

```sh
go build ./cmd/sluice
```

This writes a `sluice` binary in the current directory. Tagged releases also
provide platform archives containing the binary, `LICENSE`, and this
`README.md`.

## Usage

```text
sluice [--mode block|discard] [--events-fd N] --open EVENT --close EVENT open|closed
```

The final argument is the initial state:

- `open` starts by forwarding stdin to stdout.
- `closed` starts closed, using the selected mode.

`block` is the default mode. While CLOSED it does not read stdin, so an
upstream writer can block and backpressure propagates through the pipeline.
`discard` continues reading stdin while CLOSED but drops those bytes.

Only the event that can leave the current state is armed. `--open` is armed
while CLOSED and `--close` is armed while OPEN. The same event can be supplied
for both flags; each occurrence observed by `sluice` then toggles the state.
Rapid identical POSIX signals may be coalesced before they are observed, so
each delivered signal is not guaranteed to produce a separate transition.

### Events

The supported event forms are:

- `signal:USR1` or `signal:SIGUSR1`;
- `signal:USR2` or `signal:SIGUSR2`;
- `duration:DURATION`, where `DURATION` uses Go duration syntax such as
  `250ms`, `1s`, or `1m30s`.

Negative durations are rejected. Signal events are POSIX-only; on Windows and
other platforms without these signals, use duration events.

A duration begins when its transition event is armed for the corresponding
state. The initial state's duration starts at process startup; the other
duration starts when its state is entered, and each duration restarts whenever
its state is entered again.

Use `-h` or `--help` for the command summary. `--version` prints the version
and must be used by itself, without normal-operation arguments.
`--describe` prints one deterministic, machine-readable JSON description of the
current CLI, stream semantics, state machine, and platform capabilities. It
also must be used by itself; when `--help` is parsed, flag parsing prints help
before describe validation. Its `schema_version` starts at `1`; consumers
should ignore unknown fields, while existing field meanings remain compatible
within a schema version.

### Lifecycle events

Pass `--events-fd N` (or `--events-fd=N`) to write committed stream
transitions as JSON Lines to file descriptor `N`, which must be at least 3:

```json
{"event":"stream-open","timestamp":"2026-09-21T12:00:00.123456789Z"}
```

The event stream reports `stream-open` and `stream-closed` transitions in
order. The initial state and EOF do not produce events. Timestamps are UTC in
RFC3339Nano format. The descriptor is duplicated and remains owned by the
caller. On Unix, it must be a writable FIFO or socket that is already in
`O_NONBLOCK` mode. On Windows, it must be a named pipe that is already in
`PIPE_NOWAIT` mode. `sluice` checks this at startup and exits with status 2
before reading stdin when the descriptor is unsuitable. Keep the mode enabled
while `sluice` is running because the duplicate shares the descriptor's open
file description; `sluice` does not change the caller's descriptor flags.

Event writes are immediate and nonblocking. If the descriptor cannot accept an
event, `sluice` warns once on stderr, disables further event output, and
continues its normal stream behavior. A failed nonblocking socket write may
leave a partial final JSON line; consumers should discard an incomplete line
after an event-stream failure.

For example, a Unix caller can create a nonblocking event pipe and pass its
write end to `sluice`:

```sh
python3 - <<'PY'
import os
import subprocess

events_r, events_w = os.pipe()
os.set_blocking(events_w, False)
process = subprocess.Popen(
    ["./sluice", "--events-fd", str(events_w), "--open", "duration:0s",
     "--close", "duration:1h", "closed"],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE, pass_fds=(events_w,))
os.close(events_w)
stdout, _ = process.communicate(b"forwarded\n")
events = os.fdopen(events_r, "rb").read()
print(stdout.decode(), end="")
print(events.decode(), end="")
PY
```

## Examples

Except for the example explicitly labeled PowerShell, these examples use POSIX
shell syntax.

### Duration events

This starts CLOSED; the small write can fit in the upstream pipe and remains
there until the duration opens the sluice:

```sh
{ printf '%s\n' 'released after one second'; } |
  ./sluice --mode block --open duration:1s --close duration:1h closed
```

On Windows, after a `sluice.exe` binary is available in the current directory,
the equivalent PowerShell command is:

```powershell
"released after one second" | .\sluice.exe --mode block --open duration:1s --close duration:1h closed
```

### POSIX signal events

Signal delivery is asynchronous. The POSIX-shell examples below use `sleep 1`
before the first signal and after each signal to give the prior transition time
to settle; increase that bounded wait on a heavily loaded host if needed.
Rapid identical signals may coalesce, so do not use them as a per-signal
counter. The FIFO and direct background command keep the `sluice` PID
unambiguous.

With different signals for opening and closing, only the line written while
OPEN is printed:

```sh
set -eu
tmp_dir=$(mktemp -d)
sluice_pid=
cleanup() {
  pid=$sluice_pid
  sluice_pid=
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || :
    wait "$pid" 2>/dev/null || :
  fi
  rm -rf "$tmp_dir"
}
trap cleanup 0 HUP INT TERM
mkfifo "$tmp_dir/in"

./sluice --mode discard --open signal:USR1 --close signal:USR2 closed \
  <"$tmp_dir/in" >"$tmp_dir/out" &
sluice_pid=$!
exec 3>"$tmp_dir/in"
sleep 1
kill -USR1 "$sluice_pid"
sleep 1
printf '%s\n' 'forwarded while OPEN' >&3
kill -USR2 "$sluice_pid"
sleep 1
printf '%s\n' 'discarded while CLOSED' >&3
exec 3>&-
wait "$sluice_pid"
sluice_pid=
cat "$tmp_dir/out"
```

Use the same signal for both transitions to toggle the state:

```sh
set -eu
tmp_dir=$(mktemp -d)
sluice_pid=
cleanup() {
  pid=$sluice_pid
  sluice_pid=
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || :
    wait "$pid" 2>/dev/null || :
  fi
  rm -rf "$tmp_dir"
}
trap cleanup 0 HUP INT TERM
mkfifo "$tmp_dir/in"

./sluice --mode discard --open signal:USR1 --close signal:USR1 closed \
  <"$tmp_dir/in" >"$tmp_dir/out" &
sluice_pid=$!
exec 3>"$tmp_dir/in"
sleep 1
kill -USR1 "$sluice_pid"
sleep 1
printf '%s\n' 'forwarded after first toggle' >&3
kill -USR1 "$sluice_pid"
sleep 1
printf '%s\n' 'discarded after second toggle' >&3
exec 3>&-
wait "$sluice_pid"
sluice_pid=
cat "$tmp_dir/out"
```

Both signal examples are POSIX-shell examples and use `USR1`/`USR2`; they do
not run on Windows.

### `block` versus `discard`

With `block`, this small input can remain in the upstream pipe while CLOSED
and is forwarded when the stream opens. A sufficiently large write that fills
the pipe blocks its producer, providing upstream backpressure:

```sh
printf '%s\n' 'held, then forwarded' |
  ./sluice --mode block --open duration:1s --close duration:1h closed
```

With `discard`, the producer is drained while CLOSED and its data is dropped,
so this prints nothing and exits without waiting for the one-second opening:

```sh
printf '%s\n' 'discarded' |
  ./sluice --mode discard --open duration:1s --close duration:1h closed
```

## Stream and EOF semantics

- EOF while OPEN exits successfully after forwarded data has been written.
- EOF while CLOSED in `discard` mode exits successfully after the input is
  drained.
- CLOSED `block` mode does not read stdin while CLOSED, so EOF from the normal
  CLOSED/block path is deferred until OPEN. A read already in flight can
  observe EOF according to the transition race.

If a state transition races with a read or write already in flight, those
boundary bytes follow normal concurrent pipe behavior. `sluice` does not
provide a strict transition-boundary cutoff, so a byte at the boundary may be
forwarded or discarded according to that race.
