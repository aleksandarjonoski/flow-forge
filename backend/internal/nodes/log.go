package nodes

import (
	"fmt"
	"log/slog"
	"strings"
	"text/template"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
)

// LogType is the registered type name of the log node.
const LogType = "log"

func init() {
	engine.DefaultRegistry.Register(LogType, func() engine.Node { return &Log{} })
}

// defaultLogMessage is used when the node has no "message" config.
const defaultLogMessage = "payload"

// Log writes its input to a slog logger and passes it through unchanged, so it
// can be inserted anywhere in a flow. See docs/engine-v1.md §11.
//
// All fields are set once at Init and only read afterwards, so Execute is safe
// for concurrent executions.
type Log struct {
	level   slog.Level
	message *template.Template
	// logger overrides slog.Default(); tests set it to capture output.
	logger *slog.Logger
}

func (l *Log) Type() string { return LogType }

// Init parses the level and the message template so bad config fails at load
// time.
func (l *Log) Init(config map[string]any, _ *engine.EngineServices) error {
	levelName, err := stringConfig(config, "level", false, "info")
	if err != nil {
		return err
	}
	level, err := parseLevel(levelName)
	if err != nil {
		return err
	}
	message, err := stringConfig(config, "message", false, defaultLogMessage)
	if err != nil {
		return err
	}
	tmpl, err := template.New("message").Parse(message)
	if err != nil {
		return fmt.Errorf("parse message template: %w", err)
	}
	l.level = level
	l.message = tmpl
	return nil
}

func (l *Log) Execute(ctx *engine.ExecutionContext, input engine.Payload) (engine.Payload, error) {
	var msg strings.Builder
	data := map[string]any{"input": input, "nodes": ctx.NodeResults}
	if err := l.message.Execute(&msg, data); err != nil {
		return nil, fmt.Errorf("render message: %w", err)
	}

	logger := l.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Log(ctx.GoCtx, l.level, msg.String(),
		slog.String("flow_id", ctx.FlowID),
		slog.String("execution_id", ctx.ExecutionID),
		slog.Any("payload", input),
	)
	return input, nil
}

// parseLevel maps the config level names to slog levels.
func parseLevel(name string) (slog.Level, error) {
	switch name {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("config %q: unknown level %q (want debug, info, warn or error)", "level", name)
	}
}
