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
	"strings"
	"syscall"
	"time"

	"github.com/filecoin-project/go-jsonrpc"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/api/client"
	"github.com/gorilla/mux"
	"github.com/iand/gonudb"
	"github.com/iand/logfmtr"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
	"github.com/urfave/cli/v2"
)

const (
	LogLevelDiagnostics = 1 // log level increment for diagnostics logging
	LogLevelTrace       = 2 // log level increment for verbose tracing
)

func main() {
	app := &cli.App{
		Name:     "lotus-cpr",
		HelpName: "lotus-cpr",
		Usage:    "A caching proxy for Lotus filecoin nodes.",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:    "log-level",
				Aliases: []string{"ll"},
				Usage:   fmt.Sprintf("Set verbosity of logs to `LEVEL` (%d=diagnostics, %d=trace).", LogLevelDiagnostics, LogLevelTrace),
				Value:   0,
			},
			&cli.BoolFlag{
				Name:    "humanize-logs",
				Aliases: []string{"hl"},
				Usage:   "Use humanized and colorized log output.",
				Value:   false,
			},
			&cli.StringFlag{
				Name:     "api",
				Usage:    "Token and multiaddress of Lotus node (format: <oauth_token>:/ip4/127.0.0.1/tcp/1234/http).",
				EnvVars:  []string{"FULLNODE_API_INFO"},
				Required: true,
			},
			&cli.StringFlag{
				Name:  "store",
				Usage: "Path to directory containing gonudb store.",
			},
			&cli.StringFlag{
				Name:  "s3-bucket",
				Usage: "Name of S3 bucket containing filecoin blocks.",
			},
			&cli.StringFlag{
				Name:  "listen",
				Usage: "Address to start the jsonrpc server on.",
				Value: ":33111",
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

var logger = logfmtr.New()

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

	api, closer, err := connect(ctx, cc.String("api"))
	if err != nil {
		return fmt.Errorf("failed to connect to lotus: %w", err)
	}
	defer closer()

	caches := []BlockCache{
		NewNodeBlockCache(api, logger.WithName("node")),
	}

	if cc.String("s3-bucket") != "" {
		s3Cache := NewS3BlockCache(cc.String("s3-bucket"), logger.WithName("s3"))

		upstream := caches[len(caches)-1]
		s3Cache.SetUpstream(upstream)

		caches = append(caches, s3Cache)
		logger.Info("Added s3 cache", "bucket", cc.String("s3-bucket"))
	}

	if cc.String("store") != "" {
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

		dbCache := NewDBBlockCache(s, logger.WithName("db"))

		upstream := caches[len(caches)-1]
		dbCache.SetUpstream(upstream)

		caches = append(caches, dbCache)
		logger.Info("Added gonudb cache", "path", cc.String("store"))
	}

	rpcServer := jsonrpc.NewServer()
	rpcServer.Register("Filecoin", NewAPIProxy(api, caches[len(caches)-1], logger))

	dlogger := logfmtr.New().V(LogLevelDiagnostics)
	if dlogger.Enabled() {
		go func() {
			for {
				select {
				case <-time.After(1 * time.Minute):
					for i := range caches {
						caches[i].LogStats(dlogger)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}

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

	address := cc.String("listen")
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
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
			logger.Info("Creating store", "path", path)
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

	logger.Info("Opening store", "path", path)
	s, err := gonudb.OpenStore(datPath, keyPath, logPath, &gonudb.StoreOptions{Logger: logger.WithName("gonudb").V(1)})
	if err != nil {
		return nil, fmt.Errorf("Failed to open store: %w", err)
	}
	return s, nil
}

func connect(ctx context.Context, tokenMaddr string) (api.FullNode, jsonrpc.ClientCloser, error) {
	toks := strings.Split(tokenMaddr, ":")
	if len(toks) != 2 {
		return nil, nil, fmt.Errorf("invalid api tokens, expected <token>:<maddr>, got: %s", tokenMaddr)
	}
	rawtoken := toks[0]
	rawaddr := toks[1]

	parsedAddr, err := ma.NewMultiaddr(rawaddr)
	if err != nil {
		return nil, nil, fmt.Errorf("parse listen address: %w", err)
	}

	_, addr, err := manet.DialArgs(parsedAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial multiaddress: %w", err)
	}

	api, closer, err := client.NewFullNodeRPC(ctx, apiURI(addr), apiHeaders(rawtoken))
	if err != nil {
		return nil, nil, fmt.Errorf("new full node rpc: %w", err)
	}

	logger.Info("Connected to lotus", "addr", rawaddr)
	return api, closer, nil
}

func apiURI(addr string) string {
	return "ws://" + addr + "/rpc/v0"
}

func apiHeaders(token string) http.Header {
	headers := http.Header{}
	headers.Add("Authorization", "Bearer "+token)
	return headers
}
