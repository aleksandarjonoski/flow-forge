// Command flowforge runs a FlowForge flow, or serves the editor API and the
// deployed flows. See docs/engine-v1.md §4.6, docs/api-v1.md and
// docs/server-v1.md §10.
//
//	flowforge run [-addr :8080] <flow.json>
//	flowforge serve [-addr 127.0.0.1:8090] [-flows-addr :8080] [-data dir] [-keep-executions 1000]
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
	"path/filepath"
	"syscall"
	"time"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/api"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/history"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/host"
	_ "github.com/aleksandarjonoski/flow-forge/backend/internal/nodes" // register built-in nodes
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
)

const usage = `usage:
  flowforge run [-addr :8080] <flow.json>
      run a flow until interrupted
  flowforge serve [-addr 127.0.0.1:8090] [-flows-addr :8080] [-data dir] [-keep-executions 1000]
      serve the editor API (local use only) and the deployed flows`

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

// serveCmd implements `flowforge serve`: the editor API and every deployed
// flow, until interrupted. See docs/server-v1.md.
func serveCmd(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8090", "listen address for the editor API (no auth: keep it local)")
	flowsAddr := fs.String("flows-addr", ":8080", "listen address for the deployed flows' http_trigger routes")
	data := fs.String("data", "", "data directory (default $FLOWFORGE_DATA, else ~/.flowforge)")
	keep := fs.Int("keep-executions", history.DefaultKeep, "executions kept per flow in the run history (0 keeps all)")
	if code := parseFlags(fs, args, 0, stderr); code >= 0 {
		return code
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	dir, err := dataDir(*data)
	if err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}
	apiLn, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}
	flowsLn, err := net.Listen("tcp", *flowsAddr)
	if err != nil {
		apiLn.Close()
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runServer(ctx, serverConfig{dir: dir, keep: *keep}, apiLn, flowsLn); err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}
	slog.Info("flowforge stopped")
	return 0
}

// dataDir resolves the data directory: the flag, else $FLOWFORGE_DATA, else
// ~/.flowforge (server-v1.md §3.1).
func dataDir(flagValue string) (string, error) {
	if flagValue != "" {
		return flagValue, nil
	}
	if env := os.Getenv("FLOWFORGE_DATA"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no data directory: set -data or FLOWFORGE_DATA (%w)", err)
	}
	return filepath.Join(home, ".flowforge"), nil
}

// serverConfig is what `flowforge serve` was asked for.
type serverConfig struct {
	dir  string // data directory
	keep int    // executions kept per flow
}

// runServer opens the store in cfg.dir, starts the deployed flows and serves
// the editor API on apiLn and the flows on flowsLn until ctx is cancelled.
// Then both listeners stop accepting requests, the flows stop (staying
// deployed for the next start), the run history is flushed and the store
// closes (server-v1.md §4.5). It closes both listeners.
func runServer(ctx context.Context, cfg serverConfig, apiLn, flowsLn net.Listener) error {
	dir := cfg.dir
	st, err := store.Open(dir)
	if err != nil {
		apiLn.Close()
		flowsLn.Close()
		return err
	}
	defer st.Close()

	hist := history.New(st, history.Options{Keep: cfg.keep})
	defer hist.Close() // after the flows stop: their last events are flushed
	h := host.New(st, engine.DefaultRegistry, host.Options{Sink: hist.Sink})
	res, err := h.Boot(ctx)
	if err != nil {
		apiLn.Close()
		flowsLn.Close()
		return errors.Join(err, h.Shutdown())
	}
	if res.Interrupted > 0 {
		slog.Warn("executions interrupted by the last shutdown", slog.Int64("count", res.Interrupted))
	}
	slog.Info("flows started", slog.Int("started", res.Started), slog.Int("failed", res.Failed))

	apiAddr := apiLn.Addr().String()
	if !isLoopback(apiLn.Addr()) {
		slog.Warn("the editor API has no authentication and is reachable from the network", slog.String("addr", apiAddr))
	}
	slog.Info("flowforge serving",
		slog.String("data", dir), slog.String("editor", apiAddr), slog.String("flows", flowsLn.Addr().String()))

	apiHandler := api.NewHandler(engine.DefaultRegistry, api.Options{Store: st, Host: h})
	srvErr := serveHTTP(ctx, []listener{{apiLn, apiHandler}, {flowsLn, h.Handler()}})
	return errors.Join(srvErr, h.Shutdown())
}

// isLoopback reports whether addr is a loopback address.
func isLoopback(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)
	return ok && tcp.IP.IsLoopback()
}

// listener is one listener and the handler to serve on it.
type listener struct {
	ln net.Listener
	h  http.Handler
}

// serveHTTP serves each handler on its listener until ctx is cancelled or a
// server fails, then shuts every server down, giving in-flight requests
// shutdownGrace to finish.
func serveHTTP(ctx context.Context, ls []listener) error {
	srvErr := make(chan error, len(ls))
	servers := make([]*http.Server, len(ls))
	for i, l := range ls {
		srv := &http.Server{Handler: l.h, ReadHeaderTimeout: 10 * time.Second}
		servers[i] = srv
		go func() { srvErr <- srv.Serve(l.ln) }()
	}

	var errs []error
	select {
	case err := <-srvErr:
		errs = append(errs, fmt.Errorf("http server: %w", err))
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("http shutdown: %w", err))
		}
	}
	return errors.Join(errs...)
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
