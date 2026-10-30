package nodes

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// newLog returns an initialized Log node that writes JSON records to buf.
func newLog(t *testing.T, config map[string]any, buf *bytes.Buffer) *Log {
	t.Helper()
	l := &Log{}
	if err := l.Init(config, nil); err != nil {
		t.Fatalf("Init(%v): %v", config, err)
	}
	l.logger = slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return l
}

// decodeRecord parses the single JSON log record in buf.
func decodeRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("decode log record %q: %v", buf.String(), err)
	}
	return rec
}

func TestLog_Type(t *testing.T) {
	if got := (&Log{}).Type(); got != "log" {
		t.Errorf("Type() = %q, want log", got)
	}
}

func TestLog_InitErrors(t *testing.T) {
	cases := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{"unknown level", map[string]any{"level": "loud"}, "unknown level"},
		{"non-string level", map[string]any{"level": 1}, "must be a string"},
		{"non-string message", map[string]any{"message": true}, "must be a string"},
		{"bad template", map[string]any{"message": "{{.input"}, "parse message template"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&Log{}).Init(tc.config, nil)
			if err == nil {
				t.Fatal("Init succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestLog_Defaults(t *testing.T) {
	var buf bytes.Buffer
	l := newLog(t, nil, &buf)
	ctx := &engine.ExecutionContext{FlowID: "f1", ExecutionID: "e1"}
	input := engine.Payload{"k": "v"}

	out, err := l.Execute(ctx, input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !reflect.DeepEqual(out, input) {
		t.Errorf("output = %v, want input passed through", out)
	}

	rec := decodeRecord(t, &buf)
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}
	if rec["msg"] != "payload" {
		t.Errorf("msg = %v, want payload", rec["msg"])
	}
	if rec["flow_id"] != "f1" || rec["execution_id"] != "e1" {
		t.Errorf("ids = %v/%v, want f1/e1", rec["flow_id"], rec["execution_id"])
	}
	if !reflect.DeepEqual(rec["payload"], map[string]any{"k": "v"}) {
		t.Errorf("payload = %v, want {k: v}", rec["payload"])
	}
}

func TestLog_Levels(t *testing.T) {
	for name, want := range map[string]string{
		"debug": "DEBUG", "info": "INFO", "warn": "WARN", "error": "ERROR",
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			l := newLog(t, map[string]any{"level": name}, &buf)
			if _, err := l.Execute(&engine.ExecutionContext{}, nil); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got := decodeRecord(t, &buf)["level"]; got != want {
				t.Errorf("level = %v, want %s", got, want)
			}
		})
	}
}

func TestLog_MessageTemplate(t *testing.T) {
	var buf bytes.Buffer
	l := newLog(t, map[string]any{
		"message": `hello {{.input.body.name}} from {{index .nodes "node-1" "source"}}`,
	}, &buf)
	ctx := &engine.ExecutionContext{
		NodeResults: map[string]engine.Payload{"node-1": {"source": "webhook"}},
	}

	_, err := l.Execute(ctx, engine.Payload{"body": map[string]any{"name": "ada"}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := decodeRecord(t, &buf)["msg"]; got != "hello ada from webhook" {
		t.Errorf("msg = %q, want %q", got, "hello ada from webhook")
	}
}

func TestLog_TemplateRenderError(t *testing.T) {
	var buf bytes.Buffer
	l := newLog(t, map[string]any{"message": "{{index .input 0}}"}, &buf)

	_, err := l.Execute(&engine.ExecutionContext{}, engine.Payload{})
	if err == nil {
		t.Fatal("Execute succeeded, want error")
	}
	if !strings.Contains(err.Error(), "render message") {
		t.Errorf("error = %q, want it to mention render message", err)
	}
	if buf.Len() != 0 {
		t.Errorf("logged %q despite render error", buf.String())
	}
}
