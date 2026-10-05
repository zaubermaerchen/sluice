# sluice

`sluice` conditionally forwards standard input to standard output. It keeps an
OPEN/CLOSED state and changes state when the configured event arrives. This is
useful between a producer and a consumer when downstream should see data only
during selected windows:

```text
producer | sluice --open signal:USR1 --close signal:USR2 closed | consumer
```

## Related pipeline tools

In a conventional Unix pipeline, a pipe primarily carries data from one process
to another. Pipe-driven software also gives meaning to the flow itself. The
small, composable tools below make that flow observable and controllable without
needing to understand the contents of the data being transferred.

External conditions or signals can control flow, while flow and lifecycle
transitions can drive other behavior, such as hooks or notifications. The
presence, absence, and transitions of flow can themselves act as signals. Each
tool has a distinct role; they do not all provide every capability.

An electrical circuit is a useful mental image for this interaction between
flow and signals, rather than a strict model of Unix pipes.

| Tool | Role |
| --- | --- |
| [`khsier`](https://github.com/zaubermaerchen/khsier) | Observe flow and lifecycle transitions |
| [`pipewisp`](https://github.com/zaubermaerchen/pipewisp) | React to lifecycle transitions |
| [`dam`](https://github.com/zaubermaerchen/dam) | Hold flow until release conditions are satisfied |
| [`outage`](https://github.com/zaubermaerchen/outage) | Cut flow when a condition is triggered |
| [`sluice`](https://github.com/zaubermaerchen/sluice) | Switch flow between open and closed states |

## Installation

Install with Homebrew:

```sh
brew install zaubermaerchen/tap/sluice
```

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

Options must precede the initial state; an option after the state is an
argument-order error. Existing single-dash long options remain accepted for
compatibility; documentation, help, and diagnostics use `--long-option`.

The final argument is the initial state:

- `open` starts by forwarding stdin to stdout.
- `closed` starts closed, using the selected mode.

`block` is the default mode. While CLOSED it does not read stdin, so an
upstream writer can block and backpressure propagates through the pipeline.
An in-flight read may still complete; its chunk is held until OPEN without
starting another read while CLOSED.
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

All positive Go durations are accepted, with no fixed minimum duration.
Negative durations are rejected. Signal events are POSIX-only; on Windows and
other platforms without these signals, use duration events.

A duration begins when its transition event is armed for the corresponding
state. The initial state's duration starts at process startup; the other
duration starts when its state is entered, and each duration restarts whenever
its state is entered again.

Durations are scheduling intervals rather than guarantees of precise
state-transition timing. When both conditions repeatedly become ready
immediately, very short durations can cause rapid state transitions and high
CPU usage. If the opposite condition waits, a short duration alone does not
cause continuous transitions.

Use `-h` or `--help` for the command summary. Help takes priority over all
other arguments regardless of order, writes to stdout, and exits with code 0.
Parse errors write to stderr, exit with code 2, and include a short `--help`
hint without full usage. Configuration and startup validation errors write to
stderr and exit with code 2 without full usage. Runtime I/O errors normally
write synchronously to stderr and exit with code 1 without usage; if an event
warning holds stderr, the diagnostic is best effort and may be omitted without
delaying exit.

`--version` prints the version and must be used by itself, without
normal-operation arguments.
`--describe` prints one deterministic, machine-readable JSON description of the
current CLI, stream semantics, state machine, and platform capabilities. It
also must be used by itself; help takes priority over describe validation. Its
`schema_version` starts at `1`; consumers should ignore unknown fields, while
existing field meanings remain compatible within a schema version.

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
The destination is also checked before each event write. If its mode becomes
unsuitable or its consumer disconnects, event output is immediately disabled
and normal stream processing continues. The checks reduce the risk of
blocking the stream, but cannot prevent a mode change racing with a write.

Event writes are immediate and nonblocking. If the descriptor cannot accept an
event, `sluice` immediately disables further event output and continues its
normal stream behavior. It asynchronously attempts one stderr warning; warning
delivery is best effort, and process exit does not wait for the warning to
finish. Delivery before process termination is not guaranteed. If the warning
is holding stderr when a primary I/O error occurs, its diagnostic is also best
effort and may be omitted so the process can exit with its original status.
Ordinary primary diagnostics remain synchronous. A failed nonblocking socket
write may leave a partial final JSON line; consumers should discard an
incomplete line after an event-stream failure.

At high event rates, the consumer may not keep up; if the descriptor cannot
accept an event, the existing write-failure behavior disables further event
output as described above.

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
- CLOSED `block` mode does not start new stdin reads. If a read already in
  flight returns EOF while CLOSED, the EOF and any final bytes are held until
  OPEN. EOF returned while OPEN does not wait for a later OPEN transition.

In `block` mode, a read already in flight when the stream closes may complete.
Its chunk is held until OPEN, then forwarded exactly once before another read;
no new read starts while CLOSED. A destination write already in flight may
complete after CLOSED, so there is no strict transition-boundary cutoff.
In `discard` mode, boundary bytes may be forwarded or discarded according to
the transition race.
These CLOSED guarantees apply once the transition is complete. While a
transition is still underway, boundary bytes may already be forwarded;
EOF already accepted while OPEN may complete without another OPEN.

## Release maintenance

See [release automation](docs/release-automation.md) for publishing releases,
Homebrew tap updates, and verification and recovery steps.
