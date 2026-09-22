package main

// This file verifies the machine-readable self-description contract.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRun_DescribeEmitsFixedJSONWithoutRuntimeProcessing(t *testing.T) {
	var firstOutput, firstDiagnostics bytes.Buffer
	firstInput := &trackingReader{}
	if got := runWithIO(firstInput, &firstOutput, &firstDiagnostics, []string{"--describe"}); got != 0 {
		t.Fatalf("runWithIO() exit code = %d, want 0; diagnostics = %q", got, firstDiagnostics.String())
	}
	if firstInput.read {
		t.Fatal("--describe read stdin")
	}
	if firstDiagnostics.Len() != 0 {
		t.Fatalf("diagnostics = %q, want empty", firstDiagnostics.String())
	}
	if got := bytes.Count(firstOutput.Bytes(), []byte{'\n'}); got != 1 {
		t.Fatalf("description newline count = %d, want 1: %q", got, firstOutput.String())
	}

	var document map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(firstOutput.Bytes()))
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode --describe output: %v; output = %q", err, firstOutput.String())
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("--describe output has trailing JSON/data: %v; output = %q", err, firstOutput.String())
	}
	wantFields := []string{
		"schema_version",
		"name",
		"version",
		"cli_schema",
		"stream_semantics",
		"state_machine",
		"side_effects",
	}
	if len(document) != len(wantFields) {
		t.Fatalf("top-level field count = %d, want %d: %#v", len(document), len(wantFields), document)
	}
	for _, field := range wantFields {
		if _, ok := document[field]; !ok {
			t.Errorf("description missing top-level field %q", field)
		}
	}

	var schemaVersion int
	if err := json.Unmarshal(document["schema_version"], &schemaVersion); err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	if schemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", schemaVersion)
	}
	var name string
	if err := json.Unmarshal(document["name"], &name); err != nil {
		t.Fatalf("name: %v", err)
	}
	if name != "sluice" {
		t.Fatalf("name = %q, want sluice", name)
	}
	var describedVersion string
	if err := json.Unmarshal(document["version"], &describedVersion); err != nil {
		t.Fatalf("version: %v", err)
	}
	if describedVersion != version {
		t.Fatalf("version = %q, want %q", describedVersion, version)
	}

	var secondOutput, secondDiagnostics bytes.Buffer
	if got := runWithIO(&trackingReader{}, &secondOutput, &secondDiagnostics, []string{"--describe"}); got != 0 {
		t.Fatalf("second run exit code = %d, want 0; diagnostics = %q", got, secondDiagnostics.String())
	}
	if !bytes.Equal(firstOutput.Bytes(), secondOutput.Bytes()) {
		t.Fatalf("description output changed between runs:\nfirst:  %q\nsecond: %q", firstOutput.String(), secondOutput.String())
	}
}

func TestRun_DescribeMetadataDescribesCurrentCLIAndRuntime(t *testing.T) {
	var output, diagnostics bytes.Buffer
	if got := runWithIO(&trackingReader{}, &output, &diagnostics, []string{"--describe"}); got != 0 {
		t.Fatalf("runWithIO() exit code = %d, want 0; diagnostics = %q", got, diagnostics.String())
	}

	var document struct {
		CLISchema struct {
			Usage   string `json:"usage"`
			Options []struct {
				Name       string   `json:"name"`
				Type       string   `json:"type"`
				Required   bool     `json:"required"`
				Repeatable bool     `json:"repeatable"`
				Default    string   `json:"default"`
				Values     []string `json:"values"`
				Minimum    int      `json:"minimum"`
			} `json:"options"`
			EventForms []struct {
				Name          string   `json:"name"`
				Syntax        []string `json:"syntax"`
				Supported     bool     `json:"supported"`
				UnsupportedOn []string `json:"unsupported_on"`
				ValueFormat   string   `json:"value_format"`
				NonNegative   *bool    `json:"non_negative"`
				ZeroAllowed   *bool    `json:"zero_allowed"`
			} `json:"event_forms"`
			Constraints []string `json:"constraints"`
			Arguments   []struct {
				Name     string   `json:"name"`
				Type     string   `json:"type"`
				Required bool     `json:"required"`
				Values   []string `json:"values"`
			} `json:"arguments"`
		} `json:"cli_schema"`
		StreamSemantics map[string]struct {
			Role                    string   `json:"role"`
			Description             string   `json:"description"`
			Supported               bool     `json:"supported"`
			Format                  string   `json:"format"`
			Option                  string   `json:"option"`
			AcceptedDescriptorTypes []string `json:"accepted_descriptor_types"`
			RequiredNonblockingMode string   `json:"required_nonblocking_mode"`
		} `json:"stream_semantics"`
		StateMachine struct {
			InitialState         string   `json:"initial_state"`
			InitialStateArgument string   `json:"initial_state_argument"`
			States               []string `json:"states"`
			Events               []string `json:"events"`
			EventFDEvents        []string `json:"event_fd_events"`
			Transitions          []struct {
				From           string `json:"from"`
				Event          string `json:"event"`
				To             string `json:"to"`
				LifecycleEvent string `json:"lifecycle_event"`
			} `json:"transitions"`
			Repeating                 bool   `json:"repeating"`
			ArmsOnlyCurrentStateEvent bool   `json:"arms_only_current_state_event"`
			SameEventToggles          bool   `json:"same_event_toggles"`
			DurationArming            string `json:"duration_arming"`
			EOFBehavior               string `json:"eof_behavior"`
			InFlightBoundary          string `json:"in_flight_boundary"`
		} `json:"state_machine"`
		SideEffects json.RawMessage `json:"side_effects"`
	}
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatalf("decode description: %v; output = %q", err, output.String())
	}

	if document.CLISchema.Usage != "sluice [--mode block|discard] [--events-fd N] --open EVENT --close EVENT open|closed" {
		t.Fatalf("CLI usage = %q", document.CLISchema.Usage)
	}
	if !describeContainsString(document.CLISchema.Constraints, "--open and --close cannot both be zero-duration events") {
		t.Fatalf("CLI constraints = %#v, want zero-duration pair constraint", document.CLISchema.Constraints)
	}
	wantTypes := map[string]string{
		"--describe":  "boolean",
		"--help":      "boolean",
		"--version":   "boolean",
		"--mode":      "enum",
		"--open":      "event",
		"--close":     "event",
		"--events-fd": "fd",
	}
	seenOptions := make(map[string]bool, len(document.CLISchema.Options))
	for _, option := range document.CLISchema.Options {
		if seenOptions[option.Name] {
			t.Fatalf("duplicate CLI option %q", option.Name)
		}
		seenOptions[option.Name] = true
		wantType, ok := wantTypes[option.Name]
		if !ok {
			t.Fatalf("unexpected CLI option %q", option.Name)
		}
		if option.Type != wantType {
			t.Errorf("option %s type = %q, want %q", option.Name, option.Type, wantType)
		}
		if option.Name == "--open" || option.Name == "--close" {
			if !option.Required {
				t.Errorf("option %s required = false, want true", option.Name)
			}
		}
		if option.Name == "--mode" {
			if option.Default != "block" {
				t.Errorf("--mode default = %q, want block", option.Default)
			}
			if !describeEqualStrings(option.Values, []string{"block", "discard"}) {
				t.Errorf("--mode values = %#v, want [block discard]", option.Values)
			}
		}
	}
	if len(seenOptions) != len(wantTypes) {
		t.Fatalf("CLI option count = %d, want %d", len(seenOptions), len(wantTypes))
	}
	for _, option := range document.CLISchema.Options {
		if option.Name == "--events-fd" && option.Minimum != 3 {
			t.Fatalf("--events-fd minimum = %d, want 3", option.Minimum)
		}
	}

	if len(document.CLISchema.EventForms) != 2 {
		t.Fatalf("event form count = %d, want 2", len(document.CLISchema.EventForms))
	}
	if document.CLISchema.EventForms[0].Name != "signal" ||
		!describeEqualStrings(document.CLISchema.EventForms[0].Syntax, []string{"signal:USR1", "signal:SIGUSR1", "signal:USR2", "signal:SIGUSR2"}) {
		t.Errorf("signal event form = %#v", document.CLISchema.EventForms[0])
	}
	if document.CLISchema.EventForms[0].Supported != signalsSupportedOnPlatform() {
		t.Fatalf("signal support = %v, want %v", document.CLISchema.EventForms[0].Supported, signalsSupportedOnPlatform())
	}
	if !describeContainsString(document.CLISchema.EventForms[0].UnsupportedOn, "zos") {
		t.Errorf("signal unsupported_on = %#v, missing zos", document.CLISchema.EventForms[0].UnsupportedOn)
	}
	if !document.CLISchema.EventForms[0].Supported {
		if len(document.CLISchema.EventForms[0].UnsupportedOn) == 0 {
			t.Errorf("signal event form unsupported_on = %#v, want current platform", document.CLISchema.EventForms[0].UnsupportedOn)
		}
	} else if !signalsSupportedOnPlatform() {
		t.Fatal("description claims unsupported signal events on a supported platform")
	}
	durationForm := document.CLISchema.EventForms[1]
	if durationForm.Name != "duration" || !describeEqualStrings(durationForm.Syntax, []string{"duration:DURATION"}) {
		t.Errorf("duration event form = %#v", durationForm)
	}
	if durationForm.ValueFormat != "go-duration" || durationForm.NonNegative == nil || !*durationForm.NonNegative || durationForm.ZeroAllowed == nil || !*durationForm.ZeroAllowed {
		t.Errorf("duration constraints = %#v", durationForm)
	}
	if len(document.CLISchema.Arguments) != 1 || document.CLISchema.Arguments[0].Name != "INITIAL_STATE" ||
		document.CLISchema.Arguments[0].Type != "state" || !document.CLISchema.Arguments[0].Required ||
		!describeEqualStrings(document.CLISchema.Arguments[0].Values, []string{"open", "closed"}) {
		t.Fatalf("initial-state argument = %#v", document.CLISchema.Arguments)
	}

	wantRoles := map[string]string{
		"stdin":    "input",
		"stdout":   "passthrough",
		"stderr":   "diagnostics",
		"event_fd": "observation",
	}
	if len(document.StreamSemantics) != len(wantRoles) {
		t.Fatalf("stream interface count = %d, want %d", len(document.StreamSemantics), len(wantRoles))
	}
	for name, wantRole := range wantRoles {
		stream, ok := document.StreamSemantics[name]
		if !ok {
			t.Fatalf("missing stream interface %q", name)
		}
		if stream.Role != wantRole || strings.TrimSpace(stream.Description) == "" {
			t.Errorf("stream interface %s = %#v", name, stream)
		}
	}
	if got := document.StreamSemantics["event_fd"]; got.Format != "jsonl" || got.Option != "--events-fd" {
		t.Errorf("event_fd description = %#v", got)
	}
	wantEventFDSupported, wantDescriptorTypes, wantNonblockingMode := eventFDPlatform()
	eventFD := document.StreamSemantics["event_fd"]
	if eventFD.Supported != wantEventFDSupported || !describeEqualStrings(eventFD.AcceptedDescriptorTypes, wantDescriptorTypes) || eventFD.RequiredNonblockingMode != wantNonblockingMode {
		t.Errorf("event_fd capabilities = %#v, want supported=%v types=%#v mode=%q", eventFD, wantEventFDSupported, wantDescriptorTypes, wantNonblockingMode)
	}
	for _, name := range []string{"stdin", "stdout", "event_fd"} {
		if got := strings.ToLower(document.StreamSemantics[name].Description); !strings.Contains(got, "open") || !strings.Contains(got, "closed") {
			t.Errorf("stream interface %s description = %q, want OPEN/CLOSED semantics", name, document.StreamSemantics[name].Description)
		}
	}

	wantStates := []string{"OPEN", "CLOSED"}
	if document.StateMachine.InitialState != "positional argument" || document.StateMachine.InitialStateArgument != "open|closed" ||
		!describeEqualStrings(document.StateMachine.States, wantStates) {
		t.Fatalf("state machine initial state/states = %#v/%#v, want positional argument/open|closed and %#v", document.StateMachine.InitialState, document.StateMachine.InitialStateArgument, wantStates)
	}
	if len(document.StateMachine.Transitions) != 2 ||
		document.StateMachine.Transitions[0].From != "CLOSED" || document.StateMachine.Transitions[0].To != "OPEN" ||
		document.StateMachine.Transitions[0].LifecycleEvent != "stream-open" ||
		document.StateMachine.Transitions[1].From != "OPEN" || document.StateMachine.Transitions[1].To != "CLOSED" ||
		document.StateMachine.Transitions[1].LifecycleEvent != "stream-closed" {
		t.Fatalf("state transitions = %#v", document.StateMachine.Transitions)
	}
	if !document.StateMachine.Repeating || !document.StateMachine.ArmsOnlyCurrentStateEvent || !document.StateMachine.SameEventToggles {
		t.Fatalf("state machine capabilities = %#v", document.StateMachine)
	}
	for _, event := range []string{"stream-open", "stream-closed"} {
		if !describeContainsString(document.StateMachine.EventFDEvents, event) {
			t.Errorf("event_fd_events = %#v, missing %q", document.StateMachine.EventFDEvents, event)
		}
	}
	if describeContainsString(document.StateMachine.EventFDEvents, "initial") || describeContainsString(document.StateMachine.EventFDEvents, "eof") {
		t.Errorf("event_fd_events = %#v, must omit initial/EOF", document.StateMachine.EventFDEvents)
	}
	for _, phrase := range []string{"armed", "re-entered", "duration"} {
		if !strings.Contains(strings.ToLower(document.StateMachine.DurationArming), phrase) {
			t.Errorf("duration_arming = %q, want %q", document.StateMachine.DurationArming, phrase)
		}
	}
	for _, contract := range []struct {
		name    string
		text    string
		phrases []string
	}{
		{"stdin", document.StreamSemantics["stdin"].Description, []string{
			"CLOSED block does not read stdin", "upstream backpressure", "CLOSED discard reads and discards stdin",
		}},
		{"eof_behavior", document.StateMachine.EOFBehavior, []string{
			"after forwarded bytes are written", "after input is drained", "deferred until OPEN",
			"read already in flight can observe EOF", "EOF emits no lifecycle event",
		}},
		{"in_flight_boundary", document.StateMachine.InFlightBoundary, []string{
			"no guarantee stronger than normal concurrent pipe I/O", "may be forwarded or discarded",
		}},
	} {
		for _, phrase := range contract.phrases {
			if !strings.Contains(contract.text, phrase) {
				t.Errorf("%s = %q, missing contract %q", contract.name, contract.text, phrase)
			}
		}
	}
	if !bytes.Equal(bytes.TrimSpace(document.SideEffects), []byte("[]")) {
		t.Fatalf("side_effects = %s, want []", document.SideEffects)
	}
}

func TestRun_DescribeHelpHasPriority(t *testing.T) {
	for _, args := range [][]string{{"--describe", "--help"}, {"--help", "--describe"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			stdin := &trackingReader{}
			if got := runWithIO(stdin, &output, &diagnostics, args); got != 0 {
				t.Fatalf("runWithIO() exit code = %d, want 0; diagnostics = %q", got, diagnostics.String())
			}
			if output.Len() != 0 || !strings.Contains(diagnostics.String(), "Usage of sluice:") {
				t.Fatalf("output = %q, diagnostics = %q; want help on stderr", output.String(), diagnostics.String())
			}
			if strings.Contains(diagnostics.String(), "--describe must be used") || stdin.read {
				t.Fatalf("describe handling ran before help: diagnostics = %q, stdin read = %v", diagnostics.String(), stdin.read)
			}
		})
	}
}

func TestRun_DescribeAcceptsGoFlagSpellings(t *testing.T) {
	for _, arg := range []string{"-describe", "--describe", "--describe=true"} {
		t.Run(arg, func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			stdin := &trackingReader{}
			if got := runWithIO(stdin, &output, &diagnostics, []string{arg}); got != 0 {
				t.Fatalf("runWithIO() exit code = %d, want 0; diagnostics = %q", got, diagnostics.String())
			}
			if output.Len() == 0 || diagnostics.Len() != 0 || stdin.read {
				t.Fatalf("output = %q, diagnostics = %q, stdin read = %v", output.String(), diagnostics.String(), stdin.read)
			}
		})
	}
}

func TestRun_DescribeRejectsInvalidCombinationsWithoutRuntimeValidation(t *testing.T) {
	tests := [][]string{
		{"--describe", "extra"},
		{"--describe", "--events-fd", "0"},
		{"--events-fd", "0", "--describe"},
		{"--describe", "--version"},
		{"--version", "--describe"},
		{"--describe", "--describe"},
		{"--describe=false"},
		{"--describe", "--describe=false"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output, diagnostics bytes.Buffer
			stdin := &trackingReader{}
			if got := runWithIO(stdin, &output, &diagnostics, args); got != 2 {
				t.Fatalf("runWithIO() exit code = %d, want 2; output = %q; diagnostics = %q", got, output.String(), diagnostics.String())
			}
			if output.Len() != 0 || !strings.Contains(diagnostics.String(), "Usage of sluice:") {
				t.Fatalf("output = %q, diagnostics = %q; want usage error", output.String(), diagnostics.String())
			}
			if !strings.Contains(diagnostics.String(), "--describe must be used alone and set to true") {
				t.Fatalf("diagnostics = %q, want describe validation error", diagnostics.String())
			}
			if strings.Contains(diagnostics.String(), "invalid event file descriptor") || stdin.read {
				t.Fatalf("normal operation validation ran: diagnostics = %q, stdin read = %v", diagnostics.String(), stdin.read)
			}
		})
	}
}

func TestRun_DescribeReportsOutputError(t *testing.T) {
	const wantError = "description output unavailable"
	var diagnostics bytes.Buffer
	stdin := &trackingReader{}
	if got := runWithIO(stdin, errorWriter{err: errors.New(wantError)}, &diagnostics, []string{"--describe"}); got != 1 {
		t.Fatalf("runWithIO() exit code = %d, want 1; diagnostics = %q", got, diagnostics.String())
	}
	if !strings.Contains(diagnostics.String(), wantError) {
		t.Fatalf("diagnostics = %q, want %q", diagnostics.String(), wantError)
	}
	if stdin.read {
		t.Fatal("description output error path read stdin")
	}
}

func describeEqualStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func describeContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
