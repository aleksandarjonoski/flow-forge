// Command flowforge runs a FlowForge flow, or serves the editor API and the
// deployed flows. See docs/engine-v1.md §4.6, docs/api-v1.md and
// docs/server-v1.md §10.
//
//	flowforge run [-addr :8080] [-log-format text|json] <flow.json>
//	flowforge serve [-addr 127.0.0.1:8090] [-flows-addr :8080] [-data dir]
//	                [-keep-executions 1000] [-log-format text|json]
//	                [-api-token-file path] [-insecure-no-auth]
//	flowforge token
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
	"strings"
	"syscall"
	"time"
	// Time zone data built in, so CRON_TZ schedules work on Windows and in
	// minimal containers without zoneinfo (server-v1.md §8).
	_ "time/tzdata"

	"github.com/aleksandarjonoski/flow-forge/backend/internal/api"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/engine"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/flow"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/health"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/history"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/host"
	_ "github.com/aleksandarjonoski/flow-forge/backend/internal/nodes" // register built-in nodes
	"github.com/aleksandarjonoski/flow-forge/backend/internal/store"
	"github.com/aleksandarjonoski/flow-forge/backend/internal/webui"
)

const usage = `usage:
  flowforge run [-addr :8080] [-log-format text|json] <flow.json>
      run a flow until interrupted
  flowforge serve [-addr 127.0.0.1:8090] [-flows-addr :8080] [-data dir]
                  [-keep-executions 1000] [-log-format text|json]
                  [-api-token-file path] [-insecure-no-auth]
      serve the editor API and the deployed flows; the API needs the token
      in $FLOWFORGE_API_TOKEN or -api-token-file, if one is set
  flowforge token
      print a new random API token`

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
	case "token":
		return tokenCmd(args[1:], os.Stdout, stderr)
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
	logFormat := fs.String("log-format", "text", "log format on stderr: text or json")
	if code := parseFlags(fs, args, 1, stderr); code >= 0 {
		return code
	}
	if code := setLogger(*logFormat, stderr); code >= 0 {
		return code
	}

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
	sink := engine.NewMultiSink(
		engine.NewStdoutTraceSink(),
		engine.NewRunLogSink(slog.Default(), slog.String("flow", cf.Flow.ID)),
	)
	if err := serve(ctx, cf, svc, ln, sink); err != nil {
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
	addr := fs.String("addr", "127.0.0.1:8090", "listen address for the editor and its API")
	flowsAddr := fs.String("flows-addr", ":8080", "listen address for the deployed flows' http_trigger routes")
	data := fs.String("data", "", "data directory (default $FLOWFORGE_DATA, else ~/.flowforge)")
	keep := fs.Int("keep-executions", history.DefaultKeep, "executions kept per flow in the run history (0 keeps all)")
	logFormat := fs.String("log-format", "text", "log format on stderr: text or json")
	tokenFile := fs.String("api-token-file", "", "file holding the API token (default $FLOWFORGE_API_TOKEN)")
	insecure := fs.Bool("insecure-no-auth", false, "allow the editor API on a non-loopback -addr without a token")
	if code := parseFlags(fs, args, 0, stderr); code >= 0 {
		return code
	}
	if code := setLogger(*logFormat, stderr); code >= 0 {
		return code
	}

	token, err := apiToken(*tokenFile)
	if err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}
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
	if token == "" && !*insecure && !isLoopback(apiLn.Addr()) {
		apiLn.Close()
		fmt.Fprintf(stderr, "flowforge: the editor API would be reachable from the network at %s without authentication.\n"+
			"Set an API token in FLOWFORGE_API_TOKEN or a file named by -api-token-file (make one with `flowforge token`),\n"+
			"or bind -addr to 127.0.0.1. -insecure-no-auth serves it without a token anyway.\n", apiLn.Addr())
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
	if err := runServer(ctx, serverConfig{dir: dir, keep: *keep, token: token}, apiLn, flowsLn); err != nil {
		fmt.Fprintf(stderr, "flowforge: %v\n", err)
		return 1
	}
	slog.Info("flowforge stopped")
	return 0
}

// tokenCmd implements `flowforge token`: print a new random API token.
func tokenCmd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("token", flag.ContinueOnError)
	if code := parseFlags(fs, args, 0, stderr); code >= 0 {
		return code
	}
	fmt.Fprintln(stdout, api.NewToken())
	return 0
}

// apiToken resolves the API token: the contents of the -api-token-file
// file, else $FLOWFORGE_API_TOKEN, else none. Surrounding whitespace is
// trimmed. A token that is too short is an error (prod-v1.md §3.1).
func apiToken(file string) (string, error) {
	token := os.Getenv("FLOWFORGE_API_TOKEN")
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("API token: %w", err)
		}
		token = string(b)
	}
	token = strings.TrimSpace(token)
	if token == "" {
		if file != "" {
			return "", fmt.Errorf("API token: %s is empty", file)
		}
		return "", nil
	}
	return token, api.CheckToken(token)
}

// setLogger makes the default slog logger write to stderr in format (text or
// json), with every level enabled. It returns the exit code to stop with, or
// -1 to continue.
func setLogger(format string, stderr io.Writer) int {
	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	switch format {
	case "text":
		slog.SetDefault(slog.New(slog.NewTextHandler(stderr, opts)))
	case "json":
		slog.SetDefault(slog.New(slog.NewJSONHandler(stderr, opts)))
	default:
		fmt.Fprintf(stderr, "flowforge: -log-format must be text or json, not %q\n", format)
		return 2
	}
	return -1
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
	dir   string // data directory
	keep  int    // executions kept per flow
	token string // API token; empty serves the API without authentication
}

// runServer opens the store in cfg.dir and serves the editor API on apiLn
// and the flows on flowsLn, starting the deployed flows meanwhile, until ctx
// is cancelled. While the flows start, /_flowforge/ready and the API answer
// 503 (prod-v1.md §4). On shutdown readiness goes 503, both listeners stop
// accepting requests, the flows stop (staying deployed for the next start),
// the run history is flushed and the store closes (server-v1.md §4.5). It
// closes both listeners.
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
	h := host.New(st, engine.DefaultRegistry, host.Options{Sink: func(flowID string, revision int64) engine.TraceSink {
		return engine.NewMultiSink(
			hist.Sink(flowID, revision),
			engine.NewRunLogSink(slog.Default(), slog.String("flow", flowID), slog.Int64("revision", revision)),
		)
	}})
	probes := health.New(func() health.Counts {
		running, failed := h.Counts()
		return health.Counts{Running: running, Failed: failed}
	})

	// Serve from the start, so probes see "starting" during the boot.
	srvCtx, stopServing := context.WithCancel(context.Background())
	defer stopServing()
	stopWatch := context.AfterFunc(ctx, func() {
		probes.SetStopping()
		stopServing()
	})
	defer stopWatch()
	srvDone := make(chan error, 1)
	go func() {
		srvDone <- serveHTTP(srvCtx, []listener{
			{apiLn, booting(probes, editorHandler(st, h, cfg.token))},
			{flowsLn, probes.Handler(h.Handler())},
		})
	}()

	res, err := h.Boot(ctx)
	if err != nil {
		stopServing()
		return errors.Join(err, <-srvDone, h.Shutdown())
	}
	probes.SetReady()
	if res.Interrupted > 0 {
		slog.Warn("executions interrupted by the last shutdown", slog.Int64("count", res.Interrupted))
	}
	slog.Info("flows started", slog.Int("started", res.Started), slog.Int("failed", res.Failed))

	apiAddr := apiLn.Addr().String()
	switch {
	case isLoopback(apiLn.Addr()):
	case cfg.token == "":
		slog.Warn("the editor API has no authentication and is reachable from the network (-insecure-no-auth)",
			slog.String("addr", apiAddr))
	default:
		slog.Warn("the API token travels in a header: serve the editor over HTTPS through a reverse proxy",
			slog.String("addr", apiAddr))
	}
	if cfg.token != "" {
		slog.Info("the editor API needs the API token")
	}
	slog.Info("flowforge serving",
		slog.String("data", dir), slog.String("editor", "http://"+apiAddr), slog.String("flows", flowsLn.Addr().String()))
	if !webui.Built() {
		slog.Warn("this binary has no editor built in; only the API is served (build with `make build`)")
	}

	srvErr := <-srvDone
	return errors.Join(srvErr, h.Shutdown())
}

// booting answers 503 while the server boots, so that the editor API (and
// GET /api/health) only answers once the deployed flows have started.
func booting(probes *health.Probes, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probes.Starting() && strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintln(w, `{"error":"the server is starting"}`)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// editorHandler serves the editor listener: the API under /api/, and the
// editor built into the binary everywhere else (server-v1.md §8).
func editorHandler(st *store.Store, h *host.Host, token string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/", api.NewHandler(engine.DefaultRegistry, api.Options{Store: st, Host: h, Token: token}))
	mux.Handle("/", webui.Handler())
	return mux
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

// serve starts the flow and serves svc.HTTP, with the health and readiness
// probes, on ln until ctx is cancelled or the server fails. The probes
// answer from the start; the flow is ready once its triggers have started.
// If one fails to start, serve returns its error. On shutdown readiness goes
// 503, the HTTP server stops accepting requests, then the flow stops.
func serve(ctx context.Context, cf *engine.CompiledFlow, svc *engine.EngineServices, ln net.Listener, sink engine.TraceSink) error {
	probes := health.New(func() health.Counts { return health.Counts{Running: 1} })
	srv := &http.Server{Handler: probes.Handler(svc.HTTP), ReadHeaderTimeout: 10 * time.Second}
	srvErr := make(chan error, 1)
	go func() { srvErr <- srv.Serve(ln) }()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	var errs []error
	inst, err := engine.Start(cf, svc, sink)
	if err != nil {
		errs = append(errs, err)
	} else {
		probes.SetReady()
		select {
		case <-ctx.Done():
		case err := <-srvErr:
			errs = append(errs, fmt.Errorf("http server: %w", err))
		}
	}

	probes.SetStopping()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		errs = append(errs, fmt.Errorf("http shutdown: %w", err))
	}
	if inst != nil {
		errs = append(errs, inst.Stop())
	}
	return errors.Join(errs...)
}
