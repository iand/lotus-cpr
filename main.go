package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/filecoin-project/go-jsonrpc"
	"github.com/gorilla/mux"
	"github.com/iand/gonudb"
	"github.com/iand/logfmtr"
	"github.com/urfave/cli/v2"
)

const (
	LogLevelInfo        = 1 // log level increment for informational logging
	LogLevelDiagnostics = 2 // log level increment for diagnostics logging
	LogLevelTrace       = 3 // log level increment for verbose tracing
)

const (
	diagLogInterval         = 5 * time.Minute // interval between logging metrics when diagnostics logging is enabled
	metricReportingInterval = 2 * time.Second // interval between reporting metrics
)

var ErrLotusUnavailable = errors.New("upstream lotus server not available")

func main() {
	app := &cli.App{
		Name:     "lotus-cpr",
		HelpName: "lotus-cpr",
		Usage:    "A caching proxy for Lotus filecoin nodes.",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:    "log-level",
				Aliases: []string{"ll"},
				Usage:   fmt.Sprintf("Set verbosity of logs to `LEVEL` (0: off, %d: info, %d:diagnostics, %d:trace).", LogLevelInfo, LogLevelDiagnostics, LogLevelTrace),
				Value:   1,
				EnvVars: []string{"LOTUS_CPR_LOG_LEVEL"},
			},
			&cli.BoolFlag{
				Name:    "humanize-logs",
				Aliases: []string{"hl"},
				Usage:   "Use humanized and colorized log output.",
				Value:   false,
				EnvVars: []string{"LOTUS_CPR_HUMANIZE_LOGS"},
			},
			&cli.StringFlag{
				Name:    "api",
				Usage:   "Multiaddress of Lotus node.",
				EnvVars: []string{"LOTUS_CPR_API"},
				Value:   "/ip4/127.0.0.1/tcp/1234/http",
			},
			&cli.StringFlag{
				Name:     "api-token",
				Usage:    "Read only API token for Lotus node.",
				EnvVars:  []string{"LOTUS_CPR_API_TOKEN"},
				Required: true,
			},
			&cli.StringFlag{
				Name:    "store",
				Usage:   "Path to directory containing gonudb store.",
				EnvVars: []string{"LOTUS_CPR_STORE_PATH"},
			},
			&cli.StringFlag{
				Name:    "blockstore-baseurl",
				Usage:   "Base URL of a web server that serves blocks (urls follow pattern: {blockstore-baseurl}/{block_cid}/data.raw)",
				EnvVars: []string{"LOTUS_CPR_BLOCKSTORE_BASEURL"},
			},
			&cli.StringFlag{
				Name:    "listen",
				Usage:   "Address to start the jsonrpc server on.",
				EnvVars: []string{"LOTUS_CPR_LISTEN"},
				Value:   ":33111",
			},
			&cli.StringFlag{
				Name:    "diag",
				Usage:   "Address to start the diagnostics server on.",
				EnvVars: []string{"LOTUS_CPR_DIAG"},
				Value:   ":33112",
			},
			&cli.IntFlag{
				Name:    "api-concurrency",
				Usage:   "Maximum number of concurrent requests to make to the Lotus node API before triggering disconnection.",
				Value:   2000,
				EnvVars: []string{"LOTUS_CPR_API_CONCURRENCY"},
			},
			&cli.IntFlag{
				Name:    "api-errors",
				Usage:   "Maximum number of errors received from the Lotus node API before triggering disconnection.",
				Value:   8,
				EnvVars: []string{"LOTUS_CPR_API_ERRORS"},
			},
			&cli.DurationFlag{
				Name:    "disconnect-timeout",
				Usage:   "Time to wait after a disconnect from the Lotus node before attempting to reconnect.",
				Value:   30 * time.Second,
				EnvVars: []string{"LOTUS_CPR_DISCONNECT_TIMEOUT"},
			},
		},
		Action:          run,
		HideHelpCommand: true,
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
}

func run(cc *cli.Context) error {
	ctx, cancel := context.WithCancel(cc.Context)
	defer cancel()

	logfmtr.SetVerbosity(cc.Int("log-level"))
	loggerOpts := logfmtr.DefaultOptions()
	if cc.Bool("humanize-logs") {
		loggerOpts.Humanize = true
		loggerOpts.Colorize = true
	}
	logfmtr.UseOptions(loggerOpts)
	logger := logfmtr.New().V(LogLevelInfo)

	// Init metric reporting if required
	reportMetrics := false
	dlogger := logfmtr.New().V(LogLevelDiagnostics)
	if dlogger.Enabled() || cc.String("diag") != "" {
		reportMetrics = true
		if err := initMetricReporting(metricReportingInterval); err != nil {
			return fmt.Errorf("failed to initialize metric reporting: %w", err)
		}
	}

	client, err := newAPIClient(cc.String("api"), cc.String("api-token"), cc.Int("api-errors"), cc.Int("api-concurrency"), cc.Duration("disconnect-timeout"), logfmtr.NewNamed("client"))
	if err != nil {
		return fmt.Errorf("failed to create api client: %w", err)
	}
	defer client.Close()

	caches := []BlockCache{
		NewNodeBlockCache(client, logfmtr.NewNamed("node")),
	}

	if cc.String("blockstore-baseurl") != "" {
		hCache := NewHttpBlockCache(cc.String("blockstore-baseurl"), "http")

		upstream := caches[len(caches)-1]
		hCache.SetUpstream(upstream)

		caches = append(caches, hCache)
		logger.Info("Added http blockstore", "base_url", cc.String("blockstore-baseurl"))
	}

	if cc.String("store") != "" {
		logger.Info("Opening store", "path", cc.String("store"))
		s, err := openStore(ctx, cc.String("store"))
		if err != nil {
			return fmt.Errorf("failed to open gonudb store: %w", err)
		}
		defer func() {
			err := s.Close()
			if err != nil {
				logger.Error(err, "failed to close store cleanly")
			}
		}()

		dbCache := NewDBBlockCache(s, logfmtr.NewNamed("gonudb"))

		if reportMetrics {
			go func() {
				timer := time.NewTicker(2 * time.Second)
				for {
					select {
					case <-timer.C:
						dbCache.ReportMetrics(ctx)
					case <-ctx.Done():
						timer.Stop()
						return
					}
				}
			}()
		}

		upstream := caches[len(caches)-1]
		dbCache.SetUpstream(upstream)

		caches = append(caches, dbCache)
		logger.Info("Added gonudb cache", "path", cc.String("store"))
	}

	rpcServer := jsonrpc.NewServer()
	rpcServer.Register("Filecoin", NewAPIProxy(client, caches[len(caches)-1], logfmtr.NewNamed("proxy")))

	// Set up a signal handler to cancel the context
	go func() {
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, syscall.SIGTERM, syscall.SIGINT)
		select {
		case <-interrupt:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Log metrics?
	if dlogger.Enabled() {
		go func() {
			timer := time.NewTicker(diagLogInterval)
			ml := NewMetricLogger(dlogger)
			for {
				select {
				case <-timer.C:
					ml.Log()
				case <-ctx.Done():
					timer.Stop()
					return
				}
			}
		}()
	}

	// Serve metrics via http?
	if cc.String("diag") != "" {
		diagListener, err := net.Listen("tcp", cc.String("diag"))
		if err != nil {
			return fmt.Errorf("failed to listen on %q: %w", cc.String("diag"), err)
		}

		pe, err := registerPrometheusExporter("lotuscpr")
		if err != nil {
			return fmt.Errorf("failed to register prometheus exporter: %w", err)
		}

		diagMux := mux.NewRouter()
		diagMux.Handle("/metrics", pe)

		diagSrv := &http.Server{
			Handler: diagMux,
		}

		go func() {
			<-ctx.Done()
			if err := diagSrv.Shutdown(context.Background()); err != nil {
				logger.Error(err, "failed to shut down diagnostics server")
			}
		}()

		logger.Info("Starting diagnostics server", "addr", cc.String("diag"))
		go diagSrv.Serve(diagListener)
	}

	address := cc.String("listen")
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %q: %w", cc.String("listen"), err)
	}

	mux := mux.NewRouter()
	mux.Handle("/rpc/v0", rpcServer)
	mux.PathPrefix("/").Handler(http.DefaultServeMux)

	srv := &http.Server{
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		if err := srv.Shutdown(context.Background()); err != nil {
			logger.Error(err, "failed to shut down RPC server")
		}
	}()

	logger.Info("Starting RPC server", "addr", cc.String("listen"))
	return srv.Serve(listener)
}

func openStore(ctx context.Context, path string) (*gonudb.Store, error) {
	datPath := filepath.Join(path, "blocks.dat")
	keyPath := filepath.Join(path, "blocks.key")
	logPath := filepath.Join(path, "blocks.log")

	_, err := os.Stat(datPath)
	if err != nil {
		var pathErr *os.PathError
		if errors.As(err, &pathErr) && os.IsNotExist(pathErr) {
			err := gonudb.CreateStore(
				datPath,
				keyPath,
				logPath,
				1,
				gonudb.NewSalt(),
				4096,
				0.5,
			)
			if err != nil {
				return nil, fmt.Errorf("create store: %w", err)
			}
		} else {
			return nil, fmt.Errorf("stat store: %w", err)
		}
	}

	s, err := gonudb.OpenStore(datPath, keyPath, logPath, &gonudb.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to open store: %w", err)
	}
	return s, nil
}
