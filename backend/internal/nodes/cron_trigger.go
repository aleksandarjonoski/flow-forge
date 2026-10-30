package nodes

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// CronTriggerType is the registered type name of the cron trigger node.
const CronTriggerType = "cron_trigger"

func init() {
	engine.DefaultRegistry.Register(CronTriggerType, func() engine.Node { return &CronTrigger{} })
}

// CronTrigger fires one execution per tick of its schedule. It adds a job to
// the engine's shared scheduler at Start and removes it at Stop. See
// docs/engine-v1.md §11.
type CronTrigger struct {
	expression string
	schedule   cron.Schedule
	scheduler  *cron.Cron
	entry      cron.EntryID
	// now is the clock used for firedAt; tests override it.
	now func() time.Time
}

func (t *CronTrigger) Type() string { return CronTriggerType }

// Init parses the expression so an invalid schedule fails at load time.
func (t *CronTrigger) Init(config map[string]any, services *engine.EngineServices) error {
	expression, err := stringConfig(config, "expression", true, "")
	if err != nil {
		return err
	}
	schedule, err := cron.ParseStandard(expression)
	if err != nil {
		return fmt.Errorf("config %q: %w", "expression", err)
	}
	if services == nil || services.Cron == nil {
		return errors.New("engine services provide no cron scheduler")
	}
	t.expression = expression
	t.schedule = schedule
	t.scheduler = services.Cron
	t.now = time.Now
	return nil
}

// Start adds the job and returns immediately; ticks fire once the scheduler
// is running.
func (t *CronTrigger) Start(_ context.Context, emit engine.Emitter) error {
	t.entry = t.scheduler.Schedule(t.schedule, cron.FuncJob(func() { t.fire(emit) }))
	return nil
}

func (t *CronTrigger) Stop() error {
	t.scheduler.Remove(t.entry)
	return nil
}

// fire emits one tick. There is no caller to return an emit error to, so it
// is logged.
func (t *CronTrigger) fire(emit engine.Emitter) {
	payload := engine.Payload{"firedAt": t.now().UTC().Format(time.RFC3339)}
	if err := emit(payload); err != nil {
		slog.Error("cron trigger emit failed", slog.String("expression", t.expression), slog.Any("error", err))
	}
}
