// Command flowforge runs a FlowForge flow. See docs/engine-v1.md §4.6.
//
//	flowforge run [-addr :8080] <flow.json>
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
	_ "github.com/aleksandarjonoski/flow-forge/backend/internal/nodes" // register built-in nodes
)

const usage = "usage: flowforge run [-addr :8080] <flow.json>"

// shutdownGrace is how long in-flight HTTP requests get on shutdown.
const shutdownGrace = 5 * time.Second

func main() {
	os.Exit(cli(os.Args[1:], os.Stderr))
}

// cli runs the command line and returns the process exit code: 0 on clean
// shutdown, 1 on a load or runtime error, 2 on a usage error.
func cli(args []string, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "run" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, usage)
		fs.PrintDefaults()
	}
	addr := fs.String("addr", ":8080", "listen address for http_trigger nodes")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	cf, svc, err := load(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("flowforge running", slog.String("flow", cf.Flow.ID), slog.String("addr", ln.Addr().String()))
	if err := serve(ctx, cf, svc, ln, engine.NewStdoutTraceSink()); err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}
	slog.Info("flowforge stopped")
	return 0
}

// load parses and compiles the flow at path against the default registry.
func load(path string) (*engine.CompiledFlow, *engine.EngineServices, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	fl, err := flow.Parse(f)
	if err != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	svc := engine.NewEngineServices()
	cf, err := engine.Compile(fl, engine.DefaultRegistry, svc)
	if err != nil {
		return nil, nil, fmt.Errorf("compile %s: %w", path, err)
	}
	return cf, svc, nil
}

// serve runs the engine and serves svc.HTTP on ln until ctx is cancelled or
// either of them fails. On shutdown the HTTP server stops accepting requests
// first, then the engine stops.
func serve(ctx context.Context, cf *engine.CompiledFlow, svc *engine.EngineServices, ln net.Listener, sink engine.TraceSink) error {
	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	runErr := make(chan error, 1)
	go func() { runErr <- engine.Run(runCtx, cf, svc, sink) }()

	srv := &http.Server{Handler: svc.HTTP, ReadHeaderTimeout: 10 * time.Second}
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Serve(ln) }()

	var errs []error
	runDone := false
	select {
	case <-ctx.Done():
	case err := <-srvErr:
		errs = append(errs, fmt.Errorf("http server: %w", err))
	case err := <-runErr:
		// Run only returns early when a trigger fails to start.
		errs = append(errs, err)
		runDone = true
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	if !runDone {
		cancelRun()
		errs = append(errs, <-runErr)
	}
	return errors.Join(errs...)
}
