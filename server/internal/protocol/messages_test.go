package protocol

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestEnvelopeRoundTrips(t *testing.T) {
	tests := []struct {
		name          string
		msgType       string
		sessionID     string
		correlationID string
		payload       any
		newDst        func() any
		want          any
	}{
		{
			name:          "session-create",
			msgType:       TypeSessionCreate,
			sessionID:     "sess-1",
			correlationID: "corr-1",
			payload: SessionCreatePayload{
				Command: "copilot",
				Args:    []string{"--allow-all-tools"},
				Cwd:     "/home/user/repo",
				Env:     map[string]string{"TERM": "xterm-256color"},
				Cols:    120,
				Rows:    40,
			},
			newDst: func() any { return &SessionCreatePayload{} },
			want: &SessionCreatePayload{
				Command: "copilot",
				Args:    []string{"--allow-all-tools"},
				Cwd:     "/home/user/repo",
				Env:     map[string]string{"TERM": "xterm-256color"},
				Cols:    120,
				Rows:    40,
			},
		},
		{
			name:      "input-write",
			msgType:   TypeInputWrite,
			sessionID: "sess-1",
			payload:   DataPayload{Data: []byte("ls -la\n\x03")},
			newDst:    func() any { return &DataPayload{} },
			want:      &DataPayload{Data: []byte("ls -la\n\x03")},
		},
		{
			name:      "output-chunk",
			msgType:   TypeOutputChunk,
			sessionID: "sess-1",
			payload:   DataPayload{Data: []byte("\x1b[32mok\x1b[0m\r\n")},
			newDst:    func() any { return &DataPayload{} },
			want:      &DataPayload{Data: []byte("\x1b[32mok\x1b[0m\r\n")},
		},
		{
			name:      "session-resize",
			msgType:   TypeSessionResize,
			sessionID: "sess-1",
			payload:   ResizePayload{Cols: 100, Rows: 30},
			newDst:    func() any { return &ResizePayload{} },
			want:      &ResizePayload{Cols: 100, Rows: 30},
		},
		{
			name:      "session-exit",
			msgType:   TypeSessionExit,
			sessionID: "sess-1",
			payload:   SessionExitPayload{ExitCode: 0},
			newDst:    func() any { return &SessionExitPayload{} },
			want:      &SessionExitPayload{ExitCode: 0},
		},
		{
			name:    "session-sync",
			msgType: TypeSessionSync,
			payload: SessionSyncPayload{ActiveSessionIDs: []string{"s1", "s2"}},
			newDst:  func() any { return &SessionSyncPayload{} },
			want:    &SessionSyncPayload{ActiveSessionIDs: []string{"s1", "s2"}},
		},
		{
			name:          "fs-mkdir",
			msgType:       TypeFSMkdir,
			correlationID: "corr-mkdir",
			payload:       FSMkdirPayload{Path: "/projects/app", GitInit: true},
			newDst:        func() any { return &FSMkdirPayload{} },
			want:          &FSMkdirPayload{Path: "/projects/app", GitInit: true},
		},
		{
			name:          "fs-mkdir-ack",
			msgType:       TypeAck,
			correlationID: "corr-mkdir",
			payload:       FSMkdirAckPayload{Path: "/projects/app"},
			newDst:        func() any { return &FSMkdirAckPayload{} },
			want:          &FSMkdirAckPayload{Path: "/projects/app"},
		},
		{
			name:          "fs-list",
			msgType:       TypeFSList,
			correlationID: "corr-list",
			payload:       FSListPayload{Root: "/projects"},
			newDst:        func() any { return &FSListPayload{} },
			want:          &FSListPayload{Root: "/projects"},
		},
		{
			name:          "fs-list-ack",
			msgType:       TypeAck,
			correlationID: "corr-list",
			payload: FSListAckPayload{
				Entries: []FSEntry{
					{Name: "alpha", Path: "/projects/alpha"},
					{Name: "beta", Path: "/projects/beta"},
				},
			},
			newDst: func() any { return &FSListAckPayload{} },
			want: &FSListAckPayload{
				Entries: []FSEntry{
					{Name: "alpha", Path: "/projects/alpha"},
					{Name: "beta", Path: "/projects/beta"},
				},
			},
		},
		{
			name:          "fs-delete",
			msgType:       TypeFSDelete,
			correlationID: "corr-del",
			payload:       FSDeletePayload{Path: "/projects/alpha"},
			newDst:        func() any { return &FSDeletePayload{} },
			want:          &FSDeletePayload{Path: "/projects/alpha"},
		},
		{
			name:          "fs-delete-ack",
			msgType:       TypeAck,
			correlationID: "corr-del",
			payload:       FSDeleteAckPayload{Path: "/projects/alpha"},
			newDst:        func() any { return &FSDeleteAckPayload{} },
			want:          &FSDeleteAckPayload{Path: "/projects/alpha"},
		},
		{
			name:          "exec-sync",
			msgType:       TypeExecSync,
			correlationID: "corr-exec",
			payload:       ExecSyncPayload{Cwd: "/projects/alpha", Command: "go test ./...", TimeoutSec: 60},
			newDst:        func() any { return &ExecSyncPayload{} },
			want:          &ExecSyncPayload{Cwd: "/projects/alpha", Command: "go test ./...", TimeoutSec: 60},
		},
		{
			name:          "exec-sync-ack",
			msgType:       TypeAck,
			correlationID: "corr-exec",
			payload:       ExecSyncAckPayload{ExitCode: 1, Output: "FAIL\n", DurationMs: 423},
			newDst:        func() any { return &ExecSyncAckPayload{} },
			want:          &ExecSyncAckPayload{ExitCode: 1, Output: "FAIL\n", DurationMs: 423},
		},
		{
			name:          "tools-probe",
			msgType:       TypeToolsProbe,
			correlationID: "corr-probe",
			payload:       ToolsProbePayload{Commands: []string{"__shell__", "vibe", "copilot"}},
			newDst:        func() any { return &ToolsProbePayload{} },
			want:          &ToolsProbePayload{Commands: []string{"__shell__", "vibe", "copilot"}},
		},
		{
			name:          "tools-probe-ack",
			msgType:       TypeAck,
			correlationID: "corr-probe",
			payload: ToolsProbeAckPayload{
				Tools: map[string]ToolStatus{
					"__shell__": {Command: "__shell__", Installed: true, Path: "/bin/bash"},
					"vibe":      {Command: "vibe", Installed: false},
				},
			},
			newDst: func() any { return &ToolsProbeAckPayload{} },
			want: &ToolsProbeAckPayload{
				Tools: map[string]ToolStatus{
					"__shell__": {Command: "__shell__", Installed: true, Path: "/bin/bash"},
					"vibe":      {Command: "vibe", Installed: false},
				},
			},
		},
		{
			name:          "error",
			msgType:       TypeError,
			sessionID:     "sess-1",
			correlationID: "corr-err",
			payload:       ErrorPayload{Message: "spawn failed"},
			newDst:        func() any { return &ErrorPayload{} },
			want:          &ErrorPayload{Message: "spawn failed"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, err := NewEnvelope(tc.msgType, tc.sessionID, tc.correlationID, tc.payload)
			if err != nil {
				t.Fatalf("NewEnvelope() error = %v", err)
			}

			raw, err := env.Marshal()
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			gotEnv, err := UnmarshalEnvelope(raw)
			if err != nil {
				t.Fatalf("UnmarshalEnvelope() error = %v", err)
			}
			if gotEnv.Type != tc.msgType {
				t.Errorf("Type = %q, want %q", gotEnv.Type, tc.msgType)
			}
			if gotEnv.SessionID != tc.sessionID {
				t.Errorf("SessionID = %q, want %q", gotEnv.SessionID, tc.sessionID)
			}
			if gotEnv.CorrelationID != tc.correlationID {
				t.Errorf("CorrelationID = %q, want %q", gotEnv.CorrelationID, tc.correlationID)
			}

			dst := tc.newDst()
			if err := gotEnv.DecodePayload(dst); err != nil {
				t.Fatalf("DecodePayload() error = %v", err)
			}
			if !reflect.DeepEqual(dst, tc.want) {
				t.Errorf("decoded payload = %#v, want %#v", dst, tc.want)
			}
		})
	}
}

func TestEnvelopeWithoutPayloadAndValidation(t *testing.T) {
	env, err := NewEnvelope(TypeSessionClose, "sess-9", "corr-9", nil)
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	raw, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	decoded, err := UnmarshalEnvelope(raw)
	if err != nil {
		t.Fatalf("UnmarshalEnvelope() error = %v", err)
	}
	if decoded.Type != TypeSessionClose || decoded.SessionID != "sess-9" || decoded.CorrelationID != "corr-9" {
		t.Fatalf("unexpected envelope: %+v", decoded)
	}
	if err := decoded.DecodePayload(&ResizePayload{}); !errors.Is(err, ErrEmptyPayload) {
		t.Fatalf("DecodePayload() error = %v, want ErrEmptyPayload", err)
	}

	if _, err := NewEnvelope("", "", "", nil); !errors.Is(err, ErrMissingType) {
		t.Fatalf("NewEnvelope empty type error = %v, want ErrMissingType", err)
	}
	if _, err := UnmarshalEnvelope([]byte(`{"sessionId":"s1"}`)); !errors.Is(err, ErrMissingType) {
		t.Fatalf("UnmarshalEnvelope missing type error = %v, want ErrMissingType", err)
	}
}

func TestDataPayloadJSONUsesBase64(t *testing.T) {
	env, err := NewEnvelope(TypeOutputChunk, "s1", "", DataPayload{Data: []byte("hello\n")})
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	raw, err := env.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	// "hello\n" base64-encodes to "aGVsbG8K".
	if !bytes.Contains(raw, []byte(`"aGVsbG8K"`)) {
		t.Fatalf("expected base64 payload in %s", raw)
	}
}

func TestMobileFrameRoundTrips(t *testing.T) {
	frames := []MobileFrame{
		NewMobileReplayFrame("s1", []byte("\x1b[Hreplay")),
		NewMobileOutputFrame("s1", []byte("chunk")),
		NewMobileApprovalNeededFrame("s1", "copilot", "Approve\\?", "Copilot is waiting for confirmation"),
		NewMobileSessionExitFrame("s1", 0),
		NewMobileInputFrame("s1", []byte("y\n")),
		NewMobileResizeFrame("s1", 110, 36),
	}

	for _, want := range frames {
		t.Run(want.Type, func(t *testing.T) {
			raw, err := want.Marshal()
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			got, err := UnmarshalMobileFrame(raw)
			if err != nil {
				t.Fatalf("UnmarshalMobileFrame() error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got = %#v, want %#v", got, want)
			}
		})
	}

	if _, err := UnmarshalMobileFrame([]byte(`{"sessionId":"s1"}`)); !errors.Is(err, ErrMissingType) {
		t.Fatalf("UnmarshalMobileFrame missing type error = %v, want ErrMissingType", err)
	}
}
