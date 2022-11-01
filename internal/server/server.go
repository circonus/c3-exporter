// Copyright © 2022 Circonus, Inc. <support@circonus.com>
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/circonus/c3-exporter/internal/config"
	"github.com/circonus/c3-exporter/internal/logger"
	"github.com/circonus/c3-exporter/internal/release"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/rs/zerolog/log"
)

type Server struct {
	srv             *http.Server
	cfg             *config.Config
	idleConnsClosed chan struct{}
	tls             bool
}

type genericHandler struct{}

func (genericHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Warn().Str("method", r.Method).Str("uri", r.RequestURI).Msg("request received")
	http.Error(w, "not found", http.StatusNotFound)
}

type bulkHandler struct {
	log  logger.Logger
	dest config.Destination
}

func (h bulkHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not supported", http.StatusMethodNotAllowed)
		return
	}

	handleStart := time.Now()

	// extract basic auth credentials
	// we're not going to verify them, but they must be present so they can be
	// passed upstream and ultimately to opensearch.
	username, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	remote := r.Header.Get("X-Forwarded-For")
	if remote == "" {
		remote = r.RemoteAddr
	}

	method := r.Method
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	defer r.Body.Close()
	contentSize, err := io.Copy(gz, r.Body)
	if err != nil {
		log.Error().Err(err).Msg("compressing body")
		http.Error(w, "compressing body", http.StatusInternalServerError)
		return
	}
	if err = gz.Close(); err != nil {
		log.Error().Err(err).Msg("closing compressed buffer")
		http.Error(w, "closing compressed buffer", http.StatusInternalServerError)
		return
	}

	destURL := url.URL{}
	var client *http.Client
	if h.dest.EnableTLS {
		destURL.Scheme = "https"
		client = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:       10 * time.Second,
					KeepAlive:     3 * time.Second,
					FallbackDelay: -1 * time.Millisecond,
				}).DialContext,
				TLSClientConfig:     h.dest.TLSConfig.Clone(),
				TLSHandshakeTimeout: 10 * time.Second,
				DisableKeepAlives:   true,
				DisableCompression:  false,
				MaxIdleConns:        1,
				MaxIdleConnsPerHost: 0,
			},
		}
	} else {
		destURL.Scheme = "http"
		client = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:       10 * time.Second,
					KeepAlive:     3 * time.Second,
					FallbackDelay: -1 * time.Millisecond,
				}).DialContext,
				DisableKeepAlives:   true,
				DisableCompression:  false,
				MaxIdleConns:        1,
				MaxIdleConnsPerHost: 0,
			},
		}
	}

	destURL.Host = net.JoinHostPort(h.dest.Host, h.dest.Port)
	destURL.Path = r.URL.Path

	req, err := retryablehttp.NewRequestWithContext(r.Context(), method, destURL.String(), &buf)
	if err != nil {
		log.Error().Err(err).Msg("creating destination request")
		http.Error(w, "creating destination request", http.StatusInternalServerError)
		return
	}

	// pass along the basic auth
	req.SetBasicAuth(username, password)

	req.Header.Set("X-Circonus-Auth-Token", h.dest.DataToken)
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Connection", "close")
	req.Header.Set("User-Agent", release.NAME+"/"+release.Version)
	req.Header.Set("X-Forwarded-For", remote)

	var reqStart time.Time
	retries := 0

	retryClient := retryablehttp.NewClient()
	retryClient.HTTPClient = client
	retryClient.Logger = h.log
	retryClient.RetryWaitMin = 50 * time.Millisecond
	retryClient.RetryWaitMax = 2 * time.Second
	retryClient.RetryMax = 7
	retryClient.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, attempt int) {
		if attempt > 0 {
			reqStart = time.Now()
			l.Printf("retrying... %s %d", r.URL.String(), attempt)
			retries++
		}
	}

	retryClient.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, attempt int) {
		if attempt > 0 {
			reqStart = time.Now()
			l.Printf("retrying... %s %d", r.URL.String(), attempt)
			retries++
		}
	}

	retryClient.ResponseLogHook = func(l retryablehttp.Logger, r *http.Response) {
		if r.StatusCode != http.StatusOK {
			l.Printf("non-200 response %s: %s", r.Request.URL.String(), r.Status)
			if r.StatusCode == http.StatusNotAcceptable {
				l.Printf("broker couldn't parse payload - try tracing metrics for this specific check")
			}
		} else if r.StatusCode == http.StatusOK && retries > 0 {
			l.Printf("succeeded after %d attempt(s)", retries+1) // add one for first failed attempt
		}
	}

	retryClient.CheckRetry = func(ctx context.Context, resp *http.Response, origErr error) (bool, error) {
		retry, rhErr := retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, origErr)
		if retry && rhErr != nil {
			log.Warn().Err(rhErr).Err(origErr).Str("req_url", resp.Request.URL.String()).Msg("request error")
		}

		return retry, nil
	}

	defer retryClient.HTTPClient.CloseIdleConnections()

	reqStart = time.Now()
	resp, err := retryClient.Do(req) //nolint:contextcheck
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		log.Error().Err(err).Str("req", req.URL.String()).Msg("making destination request")
		http.Error(w, "making destination request", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(resp.StatusCode)
	responseSize, err := io.Copy(w, resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("reading/writing response body")
		http.Error(w, "reading/writing response", http.StatusInternalServerError)
		return
	}

	log.Info().
		Str("remote", remote).
		Str("method", r.Method).
		Str("url", r.URL.String()).
		Int("resp_code", resp.StatusCode).
		Str("handle_dur", time.Since(handleStart).String()).
		Str("upstream_req_dur", time.Since(reqStart).String()).
		Int64("orig_size", contentSize).
		Int("gz_size", buf.Len()).
		Str("ratio", fmt.Sprintf("%.2f", float64(contentSize)/float64(buf.Len()))).
		Int64("resp_size", responseSize).
		Msg("request processed")
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

	mux := http.NewServeMux()
	mux.Handle("/", genericHandler{})
	mux.Handle("/_bulk", http.TimeoutHandler(bulkHandler{
		dest: cfg.Destination,
		log: logger.LogWrapper{
			Log:   log.With().Str("component", "retryablehttp").Logger(),
			Debug: cfg.Debug,
		},
	}, handlerTimeout, "Handler timeout"))

	s := &Server{
		cfg: cfg,
		tls: cfg.Server.CertFile != "" && cfg.Server.KeyFile != "",
		srv: &http.Server{
			Addr:              cfg.Server.Address,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			ReadHeaderTimeout: readHeaderTimeout,
			Handler:           mux,
		},
		idleConnsClosed: make(chan struct{}),
	}

	return s, nil
}

func (s *Server) Start(ctx context.Context) error {

	if done(ctx) {
		return ctx.Err()
	}

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
