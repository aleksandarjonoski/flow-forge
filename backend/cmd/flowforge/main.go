// Command flowforge runs a FlowForge flow, or serves the API the visual
// editor uses. See docs/engine-v1.md §4.6 and docs/api-v1.md.
//
//	flowforge run [-addr :8080] <flow.json>
//	flowforge serve [-addr 127.0.0.1:8090]
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

	"github.com/aleksandarjonoski/flow-forge/backend/internal/api"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
	_ "github.com/aleksandarjonoski/flow-forge/backend/internal/nodes" // register built-in nodes
)

const usage = `usage:
  flowforge run [-addr :8080] <flow.json>     run a flow until interrupted
  flowforge serve [-addr 127.0.0.1:8090]      serve the editor API (local use only)`

// shutdownGrace is how long in-flight HTTP requests get on shutdown.
const shutdownGrace = 5 * time.Second

func main() {
	os.Exit(cli(os.Args[1:], os.Stderr))
}

// cli runs the command line and returns the process exit code: 0 on clean
// shutdown, 1 on a load or runtime error, 2 on a usage error.
func cli(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "run":
		return runCmd(args[1:], stderr)
	case "serve":
		return serveCmd(args[1:], stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

// parseFlags parses args into fs, which must take exactly nargs positional
// arguments. It returns the exit code to stop with, or -1 to continue.
func parseFlags(fs *flag.FlagSet, args []string, nargs int, stderr io.Writer) int {
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != nargs {
		fs.Usage()
		return 2
	}
	return -1
}

// runCmd implements `flowforge run`: run one flow until interrupted.
func runCmd(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address for http_trigger nodes")
	if code := parseFlags(fs, args, 1, stderr); code >= 0 {
		return code
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

// serveCmd implements `flowforge serve`: serve the editor API until
// interrupted. See docs/api-v1.md.
func serveCmd(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8090", "listen address for the API (no auth: keep it local)")
	if code := parseFlags(fs, args, 0, stderr); code >= 0 {
		return code
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("flowforge serving API", slog.String("addr", ln.Addr().String()))
	if err := serveAPI(ctx, ln, api.NewHandler(engine.DefaultRegistry)); err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}
	slog.Info("flowforge stopped")
	return 0
}

// serveAPI serves h on ln until ctx is cancelled or the server fails, then
// shuts down, giving in-flight requests shutdownGrace to finish.
func serveAPI(ctx context.Context, ln net.Listener, h http.Handler) error {
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Serve(ln) }()

	select {
	case err := <-srvErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
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
