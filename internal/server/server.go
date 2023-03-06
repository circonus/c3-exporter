// Copyright © 2022 Circonus, Inc. <support@circonus.com>
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/circonus-labs/go-trapmetrics"
	"github.com/circonus/c3-exporter/internal/config"
	"github.com/circonus/c3-exporter/internal/logger"
	"github.com/rs/zerolog/log"
)

type Server struct {
	srv             *http.Server
	cfg             *config.Config
	idleConnsClosed chan struct{}
	metrics         *trapmetrics.TrapMetrics
	tls             bool
}

func New(cfg *config.Config) (*Server, error) {

	readTimeout, err := time.ParseDuration(cfg.Server.ReadTimeout)
	if err != nil {
		return nil, err
	}
	writeTimeout, err := time.ParseDuration(cfg.Server.WriteTimeout)
	if err != nil {
		return nil, err
	}
	idleTimeout, err := time.ParseDuration(cfg.Server.IdleTimeout)
	if err != nil {
		return nil, err
	}
	readHeaderTimeout, err := time.ParseDuration(cfg.Server.ReadHeaderTimeout)
	if err != nil {
		return nil, err
	}
	handlerTimeout, err := time.ParseDuration(cfg.Server.HandlerTimeout)
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:             cfg,
		tls:             cfg.Server.CertFile != "" && cfg.Server.KeyFile != "",
		idleConnsClosed: make(chan struct{}),
	}

	// create the check for tracking
	metrics, err := initMetrics(cfg.Circonus)
	if err != nil {
		return nil, err
	}

	s.metrics = metrics

	mux := http.NewServeMux()
	mux.Handle("/", s.verifyBasicAuth(genericHandler{s: s}))
	mux.Handle("/health", healthHandler{})
	mux.Handle("/_bulk", s.verifyBasicAuth(http.TimeoutHandler(bulkHandler{
		dest: cfg.Destination,
		log: logger.LogWrapper{
			Log:   log.With().Str("handler", "/_bulk").Logger(),
			Debug: cfg.Debug,
		},
		dataToken: cfg.Circonus.APIKey,
		metrics:   metrics,
	}, handlerTimeout, "Handler timeout")))
	mux.Handle("/_cluster/settings", s.verifyBasicAuth(clusterSettingsHandler{s: s}))
	mux.Handle("/otel-v1-apm-service-map", s.verifyBasicAuth(otelv1apmservicemapHandler{s: s}))
	mux.Handle("/_template/", s.verifyBasicAuth(templateHandler{s: s}))
	mux.Handle("/_component_template/", s.verifyBasicAuth(templateHandler{s: s}))
	mux.Handle("/_opendistro/_ism/policies/raw-span-policy", s.verifyBasicAuth(ismPolicyHandler{s: s}))
	mux.Handle("/otel-v1-apm-span-000001", s.verifyBasicAuth(otelSpanHandler{s: s}))
	mux.Handle("/otel-v1-apm-span/_search", s.verifyBasicAuth(otelSpanSearchHandler{s: s}))
	mux.Handle("/otel-v1-apm-span/_bulk", s.verifyBasicAuth(http.TimeoutHandler(bulkHandler{
		dest: cfg.Destination,
		log: logger.LogWrapper{
			Log:   log.With().Str("handler", "/_bulk").Logger(),
			Debug: cfg.Debug,
		},
		dataToken: cfg.Circonus.APIKey,
		metrics:   metrics,
	}, handlerTimeout, "Handler timeout")))

	s.srv = &http.Server{
		Addr:              cfg.Server.Address,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		Handler:           mux,
	}

	return s, nil
}

func (s *Server) Start(ctx context.Context) error {

	if done(ctx) {
		return ctx.Err()
	}

	go func(ctx context.Context) {
		ticker := time.NewTicker(s.cfg.Circonus.FlushInterval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r, err := s.metrics.Flush(ctx)
				if err != nil {
					log.Warn().Err(err).Msg("flushing circonus metrics")
				}
				log.Debug().
					Str("check_uuid", r.CheckUUID).
					Str("submit_uuid", r.SubmitUUID).
					Str("error", r.Error).
					Uint64("filtered", r.Filtered).
					Uint64("stats", r.Stats).
					Int("bytes", r.BytesSent).
					Str("encode_dur", r.EncodeDuration.String()).
					Str("submit_dur", r.SubmitDuration.String()).
					Str("last_req_dur", r.LastReqDuration.String()).
					Str("flush_dur", r.FlushDuration.String()).
					Msg("flushed metrics")
			}
		}
	}(ctx)

	if s.cfg.Server.CertFile != "" && s.cfg.Server.KeyFile != "" {
		log.Info().Str("listen", s.srv.Addr).Msg("starting TLS server")
		if err := s.srv.ListenAndServeTLS(s.cfg.Server.CertFile, s.cfg.Server.KeyFile); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Msg("listen and serve tls")
			}
		}
	} else {
		log.Info().Str("listen", s.srv.Addr).Msg("starting server")
		if err := s.srv.ListenAndServe(); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				log.Error().Err(err).Msg("listen and serve")
			}
		}
	}

	<-s.idleConnsClosed

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	log.Info().Msg("shutting down server")

	toctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.srv.Shutdown(toctx); err != nil {
		log.Error().Err(err).Msg("server shutdown")
	}

	close(s.idleConnsClosed)

	// if no error, check the ctx and return that error
	if done(ctx) {
		return ctx.Err()
	}

	return nil
}

func done(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
