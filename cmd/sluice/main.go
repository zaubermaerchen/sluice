package main

// This file owns CLI parsing and the state machine that gates stdin and stdout.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

var version = "dev"

type eventKind uint8

const (
	eventSignal eventKind = iota
	eventDuration
)

type event struct {
	kind     eventKind
	signal   string
	duration time.Duration
}

type streamMode uint8

const (
	modeBlock streamMode = iota
	modeDiscard
)

type streamState uint8

const (
	stateOpen streamState = iota
	stateClosed
)

type config struct {
	mode    streamMode
	open    event
	close   event
	initial streamState
}

type singleValue struct {
	value string
	set   bool
}

func (v *singleValue) Set(value string) error {
	if v.set {
		return errors.New("flag specified more than once")
	}
	v.value = value
	v.set = true
	return nil
}

func (v *singleValue) String() string {
	return v.value
}

type armedEvent struct {
	ch   <-chan struct{}
	stop func()
}

func main() {
	os.Exit(run(os.Stdout, os.Stderr, os.Args[1:]))
}

func run(stdout, stderr io.Writer, args []string) int {
	return runWithIO(os.Stdin, stdout, stderr, args)
}

func runWithIO(stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	fs := flag.NewFlagSet("sluice", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of %s:\n", fs.Name())
		fs.PrintDefaults()
	}

	showVersion := fs.Bool("version", false, "show version")
	var modeValue = singleValue{value: "block"}
	var openValue singleValue
	var closeValue singleValue
	fs.Var(&modeValue, "mode", "stream mode (block|discard)")
	fs.Var(&openValue, "open", "event that opens the sluice")
	fs.Var(&closeValue, "close", "event that closes the sluice")

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}

	// Keep --version as a short circuit so it remains usable without normal-operation arguments.
	if *showVersion {
		fmt.Fprintf(stdout, "sluice %s\n", version)
		return 0
	}

	cfg, err := parseConfig(modeValue, openValue, closeValue, fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "sluice: %v\n", err)
		fs.Usage()
		return 2
	}

	return runStateMachine(stdin, stdout, stderr, cfg)
}

func parseConfig(modeValue, openValue, closeValue singleValue, args []string) (config, error) {
	if !openValue.set {
		return config{}, errors.New("--open is required")
	}
	if !closeValue.set {
		return config{}, errors.New("--close is required")
	}

	var cfg config
	switch modeValue.value {
	case "block":
		cfg.mode = modeBlock
	case "discard":
		cfg.mode = modeDiscard
	default:
		return config{}, fmt.Errorf("invalid --mode %q: want block or discard", modeValue.value)
	}

	var err error
	if cfg.open, err = parseEvent(openValue.value); err != nil {
		return config{}, fmt.Errorf("invalid --open event: %w", err)
	}
	if cfg.close, err = parseEvent(closeValue.value); err != nil {
		return config{}, fmt.Errorf("invalid --close event: %w", err)
	}
	if err := validateEventPlatform(cfg.open); err != nil {
		return config{}, fmt.Errorf("invalid --open event: %w", err)
	}
	if err := validateEventPlatform(cfg.close); err != nil {
		return config{}, fmt.Errorf("invalid --close event: %w", err)
	}

	if len(args) != 1 {
		return config{}, errors.New("exactly one initial state (open or closed) is required")
	}
	switch args[0] {
	case "open":
		cfg.initial = stateOpen
	case "closed":
		cfg.initial = stateClosed
	default:
		return config{}, fmt.Errorf("invalid initial state %q: want open or closed", args[0])
	}
	return cfg, nil
}

func parseEvent(value string) (event, error) {
	prefix, detail, ok := strings.Cut(value, ":")
	if !ok || detail == "" {
		return event{}, errors.New("event must be signal:USR1, signal:USR2, or duration:DURATION")
	}

	switch prefix {
	case "signal":
		switch detail {
		case "USR1", "SIGUSR1":
			return event{kind: eventSignal, signal: "USR1"}, nil
		case "USR2", "SIGUSR2":
			return event{kind: eventSignal, signal: "USR2"}, nil
		default:
			return event{}, fmt.Errorf("unsupported signal %q", detail)
		}
	case "duration":
		duration, err := time.ParseDuration(detail)
		if err != nil {
			return event{}, fmt.Errorf("invalid duration %q: %w", detail, err)
		}
		if duration < 0 {
			return event{}, errors.New("duration must not be negative")
		}
		return event{kind: eventDuration, duration: duration}, nil
	default:
		return event{}, fmt.Errorf("unsupported event type %q", prefix)
	}
}

func armEvent(e event) (armedEvent, error) {
	if e.kind == eventDuration {
		if e.duration == 0 {
			eventCh := make(chan struct{}, 1)
			eventCh <- struct{}{}
			return armedEvent{ch: eventCh, stop: func() {}}, nil
		}
		timer := time.NewTimer(e.duration)
		done := make(chan struct{})
		eventCh := make(chan struct{}, 1)
		go func() {
			select {
			case <-timer.C:
				eventCh <- struct{}{}
			case <-done:
			}
		}()
		var stopOnce sync.Once
		return armedEvent{
			ch: eventCh,
			stop: func() {
				stopOnce.Do(func() {
					timer.Stop()
					close(done)
				})
			},
		}, nil
	}
	if e.kind == eventSignal {
		return armSignal(e)
	}
	return armedEvent{}, errors.New("unknown event kind")
}

type gatedReader struct {
	source  io.Reader
	mu      sync.Mutex
	enabled bool
	changed chan struct{}
}

func newGatedReader(source io.Reader) *gatedReader {
	return &gatedReader{source: source, changed: make(chan struct{})}
}

func (r *gatedReader) setEnabled(enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.enabled == enabled {
		return
	}
	r.enabled = enabled
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *gatedReader) Read(p []byte) (int, error) {
	for {
		r.mu.Lock()
		if r.enabled {
			source := r.source
			r.mu.Unlock()
			return source.Read(p)
		}
		changed := r.changed
		r.mu.Unlock()
		<-changed
	}
}

type stateWriter struct {
	destination io.Writer
	mu          sync.RWMutex
	open        bool
}

func (w *stateWriter) setState(state streamState) {
	w.mu.Lock()
	w.open = state == stateOpen
	w.mu.Unlock()
}

func (w *stateWriter) Write(p []byte) (int, error) {
	w.mu.RLock()
	open := w.open
	destination := w.destination
	w.mu.RUnlock()
	if !open {
		// A state change may race with an in-flight Read or Write; boundary bytes
		// follow normal concurrent pipe semantics rather than a strict cutoff.
		return len(p), nil
	}
	return destination.Write(p)
}

type stream struct {
	reader *gatedReader
	writer *stateWriter
	mode   streamMode
}

func newStream(source io.Reader, destination io.Writer, mode streamMode) *stream {
	return &stream{
		reader: newGatedReader(source),
		writer: &stateWriter{destination: destination},
		mode:   mode,
	}
}

func (s *stream) setState(state streamState) {
	s.writer.setState(state)
	s.reader.setEnabled(state == stateOpen || s.mode == modeDiscard)
}

func runStateMachine(stdin io.Reader, stdout, stderr io.Writer, cfg config) int {
	return runStateMachineWithArmer(stdin, stdout, stderr, cfg, armEvent)
}

func runStateMachineWithArmer(stdin io.Reader, stdout, stderr io.Writer, cfg config, arm func(event) (armedEvent, error)) int {
	stream := newStream(stdin, stdout, cfg.mode)
	state := cfg.initial

	armed, err := arm(eventForState(cfg, state))
	if err != nil {
		fmt.Fprintf(stderr, "sluice: cannot arm event: %v\n", err)
		return 1
	}
	defer func() {
		armed.stop()
	}()
	// Resolve one already-ready event before enabling stdin. This makes a zero
	// duration transition immediate even when stdin already contains data,
	// while still allowing two zero-duration events to be handled by the main
	// loop instead of spinning during startup.
	select {
	case <-armed.ch:
		armed.stop()
		if state == stateOpen {
			state = stateClosed
		} else {
			state = stateOpen
		}
		next, armErr := arm(eventForState(cfg, state))
		if armErr != nil {
			fmt.Fprintf(stderr, "sluice: cannot arm event: %v\n", armErr)
			return 1
		}
		armed = next
	default:
	}

	stream.setState(state)
	copyResult := make(chan error, 1)
	go func() {
		_, err := io.Copy(stream.writer, stream.reader)
		copyResult <- err
	}()

	eofPending := false
	for {
		select {
		case err := <-copyResult:
			if err != nil {
				fmt.Fprintf(stderr, "sluice: I/O error: %v\n", err)
				return 1
			}
			if state == stateOpen || cfg.mode == modeDiscard {
				return 0
			}
			// A block-mode reader may have reached EOF in a Read that began
			// before the transition to CLOSED. Keep waiting until OPEN so EOF
			// is not observed from the normal CLOSED/block path.
			eofPending = true

		case <-armed.ch:
			armed.stop()
			if state == stateOpen {
				state = stateClosed
			} else {
				state = stateOpen
			}
			stream.setState(state)
			if eofPending && (state == stateOpen || cfg.mode == modeDiscard) {
				return 0
			}
			next, armErr := arm(eventForState(cfg, state))
			if armErr != nil {
				fmt.Fprintf(stderr, "sluice: cannot arm event: %v\n", armErr)
				return 1
			}
			armed = next
		}
	}
}

func eventForState(cfg config, state streamState) event {
	if state == stateOpen {
		return cfg.close
	}
	return cfg.open
}
