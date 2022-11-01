// Copyright © 2022 Circonus, Inc. <support@circonus.com>
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"

	"github.com/circonus/c3-exporter/internal/config"
	"github.com/circonus/c3-exporter/internal/release"
	"github.com/circonus/c3-exporter/internal/server"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"golang.org/x/sys/unix"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	cfgFile := flag.String("config", "c3-exporter.yaml", "c3 exporter configuration file")
	debug := flag.Bool("debug", false, "sets log level to debug")
	version := flag.Bool("version", false, "show version and exit")
	flag.Parse()

	if *version {
		log.Info().Str("name", release.NAME).Str("version", release.Version)
		os.Exit(0)
	}

	if *debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		log.Debug().Msg("debug enabled")
	}

	cfg, err := config.Load(*cfgFile)
	if err != nil {
		log.Fatal().Err(err).Msg("loading config")
	}
	cfg.Debug = *debug

	signalCh := make(chan os.Signal, 10)
	signal.Notify(signalCh, os.Interrupt, unix.SIGTERM, unix.SIGHUP, unix.SIGPIPE, unix.SIGTRAP)

	svr, err := server.New(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("creating server")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handleSignals(ctx, signalCh, svr)

	log.Info().Str("name", release.NAME).Str("version", release.Version).Msg("starting")
	if err := svr.Start(ctx); err != nil {
		log.Error().Err(err).Msg("starting server")
	}
}

func handleSignals(ctx context.Context, signalCh chan os.Signal, s *server.Server) {
	const stacktraceBufSize = 1024 * 1024

	// pre-allocate a buffer
	buf := make([]byte, stacktraceBufSize)

	for {
		select {
		case sig := <-signalCh:
			log.Info().Str("signal", sig.String()).Msg("received signal")
			switch sig {
			case os.Interrupt, unix.SIGTERM:
				if err := s.Stop(ctx); err != nil {
					log.Error().Err(err).Msg("stopping server")
				}
				return
			case unix.SIGPIPE, unix.SIGHUP:
				// Noop
			case unix.SIGTRAP:
				stacklen := runtime.Stack(buf, true)
				fmt.Printf("=== received SIGTRAP ===\n*** goroutine dump...\n%s\n*** end\n", buf[:stacklen])
			default:
				log.Warn().Str("signal", sig.String()).Msg("unsupported")
			}
		case <-ctx.Done():
			return
		}
	}
}
