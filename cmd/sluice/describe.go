package main

// This file defines and renders sluice's machine-readable CLI description.

import (
	"encoding/json"
	"io"
)

type description struct {
	SchemaVersion   int               `json:"schema_version"`
	Name            string            `json:"name"`
	Version         string            `json:"version"`
	CLISchema       cliDescription    `json:"cli_schema"`
	StreamSemantics streamDescription `json:"stream_semantics"`
	StateMachine    stateDescription  `json:"state_machine"`
	SideEffects     []sideEffect      `json:"side_effects"`
}

type cliDescription struct {
	Usage       string                   `json:"usage"`
	Options     []cliOptionDescription   `json:"options"`
	EventForms  []eventFormDescription   `json:"event_forms"`
	Arguments   []cliArgumentDescription `json:"arguments"`
	Constraints []string                 `json:"constraints,omitempty"`
}

type cliOptionDescription struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Required   bool     `json:"required"`
	Repeatable bool     `json:"repeatable"`
	Default    string   `json:"default,omitempty"`
	Values     []string `json:"values,omitempty"`
	Minimum    int      `json:"minimum,omitempty"`
	Aliases    []string `json:"aliases,omitempty"`
	Conflicts  []string `json:"conflicts,omitempty"`
	Standalone bool     `json:"standalone,omitempty"`
}

type eventFormDescription struct {
	Name          string   `json:"name"`
	Syntax        []string `json:"syntax"`
	Supported     bool     `json:"supported"`
	UnsupportedOn []string `json:"unsupported_on,omitempty"`
	ValueFormat   string   `json:"value_format,omitempty"`
	NonNegative   *bool    `json:"non_negative,omitempty"`
	ZeroAllowed   *bool    `json:"zero_allowed,omitempty"`
}

type cliArgumentDescription struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Required  bool     `json:"required"`
	Values    []string `json:"values,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
}

type streamDescription struct {
	Stdin   streamInterfaceDescription `json:"stdin"`
	Stdout  streamInterfaceDescription `json:"stdout"`
	Stderr  streamInterfaceDescription `json:"stderr"`
	EventFD streamInterfaceDescription `json:"event_fd"`
}

type streamInterfaceDescription struct {
	Role                    string   `json:"role"`
	Description             string   `json:"description"`
	Supported               bool     `json:"supported"`
	Format                  string   `json:"format,omitempty"`
	Option                  string   `json:"option,omitempty"`
	AcceptedDescriptorTypes []string `json:"accepted_descriptor_types,omitempty"`
	RequiredNonblockingMode string   `json:"required_nonblocking_mode,omitempty"`
}

type stateDescription struct {
	InitialState              string            `json:"initial_state"`
	InitialStateArgument      string            `json:"initial_state_argument"`
	States                    []string          `json:"states"`
	Events                    []string          `json:"events"`
	EventFDEvents             []string          `json:"event_fd_events"`
	Transitions               []stateTransition `json:"transitions"`
	Repeating                 bool              `json:"repeating"`
	ArmsOnlyCurrentStateEvent bool              `json:"arms_only_current_state_event"`
	SameEventToggles          bool              `json:"same_event_toggles"`
	DurationArming            string            `json:"duration_arming"`
	EOFBehavior               string            `json:"eof_behavior"`
	InFlightBoundary          string            `json:"in_flight_boundary"`
}

type stateTransition struct {
	From           string `json:"from"`
	Event          string `json:"event"`
	To             string `json:"to"`
	LifecycleEvent string `json:"lifecycle_event"`
}

type sideEffect struct{}

func newDescription() description {
	durationNonNegative := true
	durationZeroAllowed := true
	return description{
		SchemaVersion: 1,
		Name:          "sluice",
		Version:       version,
		CLISchema: cliDescription{
			Usage: "sluice [--mode block|discard] [--events-fd N] --open EVENT --close EVENT open|closed",
			Constraints: []string{
				"--open and --close cannot both be zero-duration events",
			},
			Options: []cliOptionDescription{
				{
					Name:       "--describe",
					Type:       "boolean",
					Conflicts:  []string{"--version", "--mode", "--open", "--close", "--events-fd", "INITIAL_STATE"},
					Standalone: true,
				},
				{
					Name:       "--help",
					Type:       "boolean",
					Repeatable: true,
					Aliases:    []string{"-h"},
				},
				{
					Name:       "--version",
					Type:       "boolean",
					Conflicts:  []string{"--describe", "--mode", "--open", "--close", "--events-fd", "INITIAL_STATE"},
					Standalone: true,
				},
				{
					Name:    "--mode",
					Type:    "enum",
					Default: "block",
					Values:  []string{"block", "discard"},
				},
				{Name: "--open", Type: "event", Required: true},
				{Name: "--close", Type: "event", Required: true},
				{Name: "--events-fd", Type: "fd", Minimum: 3},
			},
			EventForms: []eventFormDescription{
				{
					Name:          "signal",
					Syntax:        []string{"signal:USR1", "signal:SIGUSR1", "signal:USR2", "signal:SIGUSR2"},
					Supported:     signalsSupportedOnPlatform(),
					UnsupportedOn: []string{"js", "plan9", "wasip1", "windows", "zos"},
				},
				{
					Name:        "duration",
					Syntax:      []string{"duration:DURATION"},
					Supported:   true,
					ValueFormat: "go-duration",
					NonNegative: &durationNonNegative,
					ZeroAllowed: &durationZeroAllowed,
				},
			},
			Arguments: []cliArgumentDescription{
				{
					Name:      "INITIAL_STATE",
					Type:      "state",
					Required:  true,
					Values:    []string{"open", "closed"},
					Conflicts: []string{"--describe", "--version"},
				},
			},
		},
		StreamSemantics: descriptionStreamSemantics(),
		StateMachine: stateDescription{
			InitialState:         "positional argument",
			InitialStateArgument: "open|closed",
			States:               []string{"OPEN", "CLOSED"},
			Events:               []string{"open-event", "close-event"},
			EventFDEvents:        []string{"stream-open", "stream-closed"},
			Transitions: []stateTransition{
				{From: "CLOSED", Event: "open-event", To: "OPEN", LifecycleEvent: "stream-open"},
				{From: "OPEN", Event: "close-event", To: "CLOSED", LifecycleEvent: "stream-closed"},
			},
			Repeating:                 true,
			ArmsOnlyCurrentStateEvent: true,
			SameEventToggles:          true,
			DurationArming:            "A duration starts when its corresponding transition event is armed for the current state, and restarts whenever that state is re-entered.",
			EOFBehavior:               "EOF while OPEN succeeds after forwarded bytes are written; EOF while CLOSED in discard mode succeeds after input is drained; CLOSED block mode does not read while closed, so EOF from the normal CLOSED/block path is deferred until OPEN. A read already in flight can observe EOF according to the transition race. EOF emits no lifecycle event.",
			InFlightBoundary:          "A transition racing with a read or write already in flight has no guarantee stronger than normal concurrent pipe I/O; boundary bytes may be forwarded or discarded according to that race.",
		},
		SideEffects: []sideEffect{},
	}
}

func descriptionStreamSemantics() streamDescription {
	eventFD := eventFDPlatformDescription()
	return streamDescription{
		Stdin: streamInterfaceDescription{
			Role:        "input",
			Supported:   true,
			Description: "Primary input stream. OPEN forwards stdin to stdout; CLOSED block does not read stdin and propagates upstream backpressure; CLOSED discard reads and discards stdin. EOF in OPEN succeeds after forwarded bytes are written; EOF in CLOSED discard succeeds after input is drained; CLOSED block does not read while closed, so EOF from the normal CLOSED/block path is deferred until OPEN, while a read already in flight can observe EOF according to the transition race.",
		},
		Stdout: streamInterfaceDescription{
			Role:        "passthrough",
			Supported:   true,
			Description: "Primary output stream containing input bytes forwarded while OPEN and suppressed while CLOSED. A transition racing with a read or write already in flight has no stricter boundary guarantee than normal concurrent pipe I/O.",
		},
		Stderr: streamInterfaceDescription{
			Role:        "diagnostics",
			Supported:   true,
			Description: "Human-facing diagnostics, warnings, argument errors, and I/O errors; it is separate from the data stream.",
		},
		EventFD: eventFD,
	}
}

func eventFDPlatformDescription() streamInterfaceDescription {
	description := streamInterfaceDescription{
		Role:        "observation",
		Supported:   true,
		Format:      "jsonl",
		Option:      "--events-fd",
		Description: "Optional machine-readable observation plane for committed OPEN/CLOSED transitions. N must be at least 3. On supported platforms the caller must supply a writable FIFO/socket or named pipe that is already in the required nonblocking mode and keep that mode enabled; startup rejects descriptors that fail platform validation with usage status 2 before stdin is read. Sluice duplicates the descriptor without taking ownership of the caller's descriptor or changing its flags. Writes are immediate and nonblocking; a failed write attempts one stderr warning, disables further event output, and leaves normal stream behavior running. The initial state and EOF produce no lifecycle event.",
	}
	supported, acceptedTypes, nonblockingMode := eventFDPlatform()
	description.Supported = supported
	description.AcceptedDescriptorTypes = acceptedTypes
	description.RequiredNonblockingMode = nonblockingMode
	if !supported {
		description.Description = "--events-fd is unavailable on this platform; startup rejects it with usage status 2, and normal stream behavior does not use an event descriptor for OPEN/CLOSED observation."
	}
	return description
}

func signalsSupportedOnPlatform() bool {
	return signalEventsSupported
}

func printDescription(out io.Writer) error {
	return json.NewEncoder(out).Encode(newDescription())
}
