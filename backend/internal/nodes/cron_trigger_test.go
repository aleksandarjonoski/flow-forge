package nodes

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

func newCronTrigger(t *testing.T, expression string, svc *engine.EngineServices) *CronTrigger {
	t.Helper()
	tr := &CronTrigger{}
	if err := tr.Init(map[string]any{"expression": expression}, svc); err != nil {
		t.Fatalf("Init(%q): %v", expression, err)
	}
	return tr
}

func TestCronTrigger_Type(t *testing.T) {
	if got := (&CronTrigger{}).Type(); got != "cron_trigger" {
		t.Errorf("Type() = %q, want cron_trigger", got)
	}
}

func TestCronTrigger_InitErrors(t *testing.T) {
	svc := engine.NewEngineServices()
	cases := []struct {
		name    string
		config  map[string]any
		svc     *engine.EngineServices
		wantErr string
	}{
		{"missing expression", map[string]any{}, svc, `"expression" is required`},
		{"invalid expression", map[string]any{"expression": "not a cron"}, svc, `"expression"`},
		{"seconds field", map[string]any{"expression": "0 * * * * *"}, svc, `"expression"`},
		{"bad time zone", map[string]any{"expression": "CRON_TZ=Nowhere/Land * * * * *"}, svc, `"expression"`},
		{"nil services", map[string]any{"expression": "@hourly"}, nil, "no cron scheduler"},
		{"no scheduler", map[string]any{"expression": "@hourly"}, &engine.EngineServices{}, "no cron scheduler"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := (&CronTrigger{}).Init(tc.config, tc.svc)
			if err == nil {
				t.Fatal("Init succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestCronTrigger_ValidExpressions(t *testing.T) {
	svc := engine.NewEngineServices()
	for _, expr := range []string{"*/5 * * * *", "@hourly", "@every 30s", "CRON_TZ=Europe/Amsterdam 0 9 * * 1-5"} {
		if err := (&CronTrigger{}).Init(map[string]any{"expression": expr}, svc); err != nil {
			t.Errorf("Init(%q): %v", expr, err)
		}
	}
}

func TestCronTrigger_FirePayload(t *testing.T) {
	tr := newCronTrigger(t, "@hourly", engine.NewEngineServices())
	tr.now = func() time.Time {
		return time.Date(2026, 10, 30, 14, 0, 0, 0, time.FixedZone("CET", 3600))
	}

	var got engine.Payload
	tr.fire(func(p engine.Payload) error { got = p; return nil })

	if got["firedAt"] != "2026-10-30T13:00:00Z" {
		t.Errorf("firedAt = %v, want 2026-10-30T13:00:00Z (UTC)", got["firedAt"])
	}
}

func TestCronTrigger_StartStopRegistersEntry(t *testing.T) {
	svc := engine.NewEngineServices()
	tr := newCronTrigger(t, "@hourly", svc)

	if err := tr.Start(context.Background(), func(engine.Payload) error { return nil }); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if n := len(svc.Cron.Entries()); n != 1 {
		t.Fatalf("entries after Start = %d, want 1", n)
	}
	if err := tr.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if n := len(svc.Cron.Entries()); n != 0 {
		t.Errorf("entries after Stop = %d, want 0", n)
	}
}

// TestCronTrigger_FiresOnRunningScheduler waits for a real tick (~1s).
func TestCronTrigger_FiresOnRunningScheduler(t *testing.T) {
	svc := engine.NewEngineServices()
	tr := newCronTrigger(t, "@every 1s", svc)

	fired := make(chan engine.Payload, 1)
	emit := func(p engine.Payload) error {
		select {
		case fired <- p:
		default:
		}
		return nil
	}
	if err := tr.Start(context.Background(), emit); err != nil {
		t.Fatalf("Start: %v", err)
	}
	svc.Cron.Start()
	defer func() {
		_ = tr.Stop()
		<-svc.Cron.Stop().Done()
	}()

	select {
	case p := <-fired:
		if _, err := time.Parse(time.RFC3339, p["firedAt"].(string)); err != nil {
			t.Errorf("firedAt %v is not RFC3339: %v", p["firedAt"], err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no tick within 3s")
	}
}
