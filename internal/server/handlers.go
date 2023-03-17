// Copyright © 2022 Circonus, Inc. <support@circonus.com>
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"time"

	"github.com/circonus-labs/go-trapmetrics"
	"github.com/circonus/c3-exporter/internal/config"
	"github.com/circonus/c3-exporter/internal/logger"
	"github.com/circonus/c3-exporter/internal/release"
	"github.com/google/uuid"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/rs/zerolog/log"
)

func (s *Server) serverError(w http.ResponseWriter, err error) {
	stack := string(debug.Stack())
	log.Error().Err(err).Str("stack", stack).Msg("server error")
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

type genericHandler struct {
	s *Server
}

func (h genericHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodHead, http.MethodGet:
		h.s.genericRequest(w, r)
	default:
		log.Warn().Str("method", r.Method).Str("uri", r.RequestURI).Msg("request received")
		http.Error(w, "not found", http.StatusNotFound)
	}
}

type healthHandler struct{}

func (healthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = w.Write([]byte("OK"))
}

type bulkHandler struct {
	metrics   *trapmetrics.TrapMetrics
	dataToken string
	dest      config.Destination
	debug     bool
}

func (h bulkHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not supported", http.StatusMethodNotAllowed)
		return
	}

	// extract basic auth credentials
	// we're not going to verify them, but they must be present so they can be
	// passed upstream and ultimately to opensearch.
	username, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	reqID := uuid.New()
	reqLogger := log.With().Str("req_id", reqID.String()).Logger()
	handleStart := time.Now()

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
		reqLogger.Error().Err(err).Msg("compressing body")
		http.Error(w, "compressing body", http.StatusInternalServerError)
		return
	}
	if err = gz.Close(); err != nil {
		reqLogger.Error().Err(err).Msg("closing compressed buffer")
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
			Timeout: 60 * time.Second,
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
			Timeout: 60 * time.Second,
		}
	}

	destURL.Host = net.JoinHostPort(h.dest.Host, h.dest.Port)
	destURL.Path = r.URL.Path

	req, err := retryablehttp.NewRequestWithContext(r.Context(), method, destURL.String(), &buf)
	if err != nil {
		reqLogger.Error().Err(err).Msg("creating destination request")
		http.Error(w, "creating destination request", http.StatusInternalServerError)
		return
	}

	reqLogger = log.With().
		Str("req_id", reqID.String()).
		Str("url", req.URL.String()).
		Str("method", req.Method).
		Logger()

	// pass along the basic auth
	req.SetBasicAuth(username, password)

	req.Header.Set("X-Circonus-Auth-Token", h.dataToken)
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	req.Header.Set("Content-Encoding", "gzip")
	// req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Connection", "close")
	req.Header.Set("User-Agent", release.NAME+"/"+release.Version)
	req.Header.Set("X-Forwarded-For", remote)

	var reqStart time.Time
	retries := 0

	retryClient := retryablehttp.NewClient()
	retryClient.HTTPClient = client
	retryClient.Logger = logger.LogWrapper{
		Log:   reqLogger.With().Str("handler", "/_bulk").Str("component", "retryablehttp").Logger(),
		Debug: h.debug,
	}
	retryClient.RetryWaitMin = 50 * time.Millisecond
	retryClient.RetryWaitMax = 2 * time.Second
	retryClient.RetryMax = 7
	retryClient.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, attempt int) {
		if attempt > 0 {
			reqStart = time.Now()
			reqLogger.Info().Int("attempt", attempt).Msg("retrying")
			retries++
		}
	}

	retryClient.ResponseLogHook = func(l retryablehttp.Logger, r *http.Response) {
		if r.StatusCode != http.StatusOK {
			reqLogger.Warn().Int("status_code", r.StatusCode).Str("status", r.Status).Msg("non-200 response")
		} else if r.StatusCode == http.StatusOK && retries > 0 {
			reqLogger.Info().Int("retries", retries+1).Msg("succeeded")
		}
	}

	retryClient.CheckRetry = func(ctx context.Context, resp *http.Response, origErr error) (bool, error) {
		retry, rhErr := retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, origErr)
		if retry && rhErr != nil {
			reqLogger.Warn().Err(rhErr).Err(origErr).Msg("request error")
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
		reqLogger.Error().Err(err).Msg("making destination request")
		// http.Error(w, "making destination request", http.StatusInternalServerError)
		// return
	}

	tags := trapmetrics.Tags{
		{Category: "units", Value: "bytes"},
		{Category: "path", Value: r.URL.Path},
	}
	_ = h.metrics.CounterIncrementByValue("log_size", tags, uint64(r.ContentLength))
	_ = h.metrics.HistogramRecordValue("log_size_h", tags, float64(r.ContentLength))
	tags = append(tags, trapmetrics.Tag{Category: "ingest_acct", Value: username})
	_ = h.metrics.CounterIncrementByValue("log_size", tags, uint64(r.ContentLength))
	_ = h.metrics.HistogramRecordValue("log_size_h", tags, float64(r.ContentLength))

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(resp.StatusCode)
	responseSize, err := io.Copy(w, resp.Body)
	if err != nil {
		reqLogger.Error().Err(err).Msg("reading/writing response body")
		http.Error(w, "reading/writing response", http.StatusInternalServerError)
		return
	}

	var ratio float64
	if r.ContentLength > 0 {
		ratio = float64(contentSize) / float64(buf.Len())
	}

	reqLogger.Info().
		Str("remote", remote).
		Str("proto", r.Proto).
		Int("upstream_resp_code", resp.StatusCode).
		Str("handle_dur", time.Since(handleStart).String()).
		Str("upstream_req_dur", time.Since(reqStart).String()).
		Int64("orig_size", contentSize).
		Int("gz_size", buf.Len()).
		Str("ratio", fmt.Sprintf("%.2f", ratio)).
		Int64("resp_size", responseSize).
		Msg("request processed")
}

type clusterSettingsHandler struct {
	s *Server
}

func (h clusterSettingsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not supported", http.StatusMethodNotAllowed)
		return
	}

	h.s.genericRequest(w, r)
}

type templateHandler struct {
	s *Server
}

func (h templateHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut, http.MethodHead, http.MethodGet:
	default:
		http.Error(w, "method not supported", http.StatusMethodNotAllowed)
		return
	}

	h.s.genericRequest(w, r)
}

type otelv1apmservicemapHandler struct {
	s *Server
}

func (h otelv1apmservicemapHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut, http.MethodHead, http.MethodGet:
	default:
		http.Error(w, "method not supported", http.StatusMethodNotAllowed)
		return
	}

	h.s.genericRequest(w, r)
}

type ismPolicyHandler struct {
	s *Server
}

func (h ismPolicyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut, http.MethodHead, http.MethodGet:
	default:
		http.Error(w, "method not supported", http.StatusMethodNotAllowed)
		return
	}

	h.s.genericRequest(w, r)
}

type otelSpanHandler struct {
	s *Server
}

func (h otelSpanHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPut, http.MethodHead, http.MethodGet:
	default:
		http.Error(w, "method not supported", http.StatusMethodNotAllowed)
		return
	}

	h.s.genericRequest(w, r)
}

type otelSpanSearchHandler struct {
	s *Server
}

func (h otelSpanSearchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	default:
		http.Error(w, "method not supported", http.StatusMethodNotAllowed)
		return
	}

	h.s.genericRequest(w, r)
}

func (s *Server) genericRequest(w http.ResponseWriter, r *http.Request) {

	username, ok := r.Context().Value(basicAuthUser).(string)
	if !ok {
		s.serverError(w, fmt.Errorf("reading context(bauser)"))
		return
	}

	password, ok := r.Context().Value(basicAuthPass).(string)
	if !ok {
		s.serverError(w, fmt.Errorf("reading context(bapass)"))
		return
	}

	reqID := uuid.New()
	reqLogger := log.With().Str("req_id", reqID.String()).Logger()
	handleStart := time.Now()

	remote := r.Header.Get("X-Forwarded-For")
	if remote == "" {
		remote = r.RemoteAddr
	}

	var contentSize int64
	var buf bytes.Buffer
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		gz := gzip.NewWriter(&buf)
		defer r.Body.Close()
		sz, err := io.Copy(gz, r.Body)
		if err != nil {
			s.serverError(w, fmt.Errorf("compressing body: %w", err))
			return
		}
		if err = gz.Close(); err != nil {
			s.serverError(w, fmt.Errorf("closing compressed buffer: %w", err))
			return
		}
		contentSize = sz
	}

	newURL := ""
	var client *http.Client
	if s.cfg.Destination.EnableTLS {
		newURL = "https://"
		client = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:       10 * time.Second,
					KeepAlive:     3 * time.Second,
					FallbackDelay: -1 * time.Millisecond,
				}).DialContext,
				TLSClientConfig:     s.cfg.Destination.TLSConfig.Clone(),
				TLSHandshakeTimeout: 10 * time.Second,
				DisableKeepAlives:   true,
				DisableCompression:  false,
				MaxIdleConns:        1,
				MaxIdleConnsPerHost: 0,
			},
			Timeout: 60 * time.Second,
		}
	} else {
		newURL = "http://"
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
			Timeout: 60 * time.Second,
		}
	}

	newURL += net.JoinHostPort(s.cfg.Destination.Host, s.cfg.Destination.Port)
	newURL += r.URL.String()

	var req *retryablehttp.Request
	var err error
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		req, err = retryablehttp.NewRequestWithContext(r.Context(), r.Method, newURL, &buf)
	} else {
		req, err = retryablehttp.NewRequestWithContext(r.Context(), r.Method, newURL, nil)
	}
	if err != nil {
		s.serverError(w, fmt.Errorf("creating destination request: %w", err))
		return
	}

	reqLogger = log.With().
		Str("req_id", reqID.String()).
		Str("url", req.URL.String()).
		Str("method", req.Method).
		Logger()

	// pass along the basic auth
	req.SetBasicAuth(username, password)

	req.Header.Set("X-Circonus-Auth-Token", s.cfg.Circonus.APIKey)
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		req.Header.Set("Content-Encoding", "gzip")
		// req.Header.Set("Accept-Encoding", "gzip")
	}
	req.Header.Set("Connection", "close")
	req.Header.Set("User-Agent", release.NAME+"/"+release.Version)
	req.Header.Set("X-Forwarded-For", remote)

	var reqStart time.Time
	retries := 0

	retryClient := retryablehttp.NewClient()
	retryClient.HTTPClient = client
	retryClient.Logger = logger.LogWrapper{
		Log:   reqLogger.With().Str("handler", "/_bulk").Str("component", "retryablehttp").Logger(),
		Debug: s.cfg.Debug,
	}
	retryClient.RetryWaitMin = 50 * time.Millisecond
	retryClient.RetryWaitMax = 2 * time.Second
	retryClient.RetryMax = 7
	retryClient.RequestLogHook = func(l retryablehttp.Logger, r *http.Request, attempt int) {
		if attempt > 0 {
			reqStart = time.Now()
			reqLogger.Info().Int("attempt", attempt).Msg("retrying")
			retries++
		}
	}

	retryClient.ResponseLogHook = func(l retryablehttp.Logger, r *http.Response) {
		if r.StatusCode != http.StatusOK {
			reqLogger.Warn().Int("status_code", r.StatusCode).Str("status", r.Status).Msg("non-200 response")
		} else if r.StatusCode == http.StatusOK && retries > 0 {
			reqLogger.Info().Int("retries", retries+1).Msg("succeeded") // add one for first failed attempt
		}
	}

	retryClient.CheckRetry = func(ctx context.Context, resp *http.Response, origErr error) (bool, error) {
		retry, rhErr := retryablehttp.ErrorPropagatedRetryPolicy(ctx, resp, origErr)
		if retry && rhErr != nil {
			reqLogger.Warn().Err(rhErr).Err(origErr).Msg("request error")
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
		reqLogger.Error().Err(err).Msg("making destination request")
		// s.serverError(w, fmt.Errorf("making destination request (%s): %w", req.URL.String(), err))
		// return
	}

	tags := trapmetrics.Tags{
		{Category: "units", Value: "bytes"},
		{Category: "path", Value: r.URL.Path},
	}
	_ = s.metrics.CounterIncrementByValue("log_size", tags, uint64(r.ContentLength))
	_ = s.metrics.HistogramRecordValue("log_size_h", tags, float64(r.ContentLength))
	tags = append(tags, trapmetrics.Tag{Category: "ingest_acct", Value: username})
	_ = s.metrics.CounterIncrementByValue("log_size", tags, uint64(r.ContentLength))
	_ = s.metrics.HistogramRecordValue("log_size_h", tags, float64(r.ContentLength))

	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	var ratio float64
	if r.ContentLength > 0 {
		ratio = float64(contentSize) / float64(buf.Len())
	}

	if resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		responseSize, err := io.Copy(w, resp.Body)
		if err != nil {
			s.serverError(w, fmt.Errorf("reading/writing response body: %w", err))
			return
		}

		reqLogger.Info().
			Str("remote", remote).
			Str("proto", r.Proto).
			Int("resp_code", resp.StatusCode).
			Str("handle_dur", time.Since(handleStart).String()).
			Str("upstream_req_dur", time.Since(reqStart).String()).
			Int64("orig_size", contentSize).
			Int("gz_size", buf.Len()).
			Str("ratio", fmt.Sprintf("%.2f", ratio)).
			Int64("resp_size", responseSize).
			Msg("request processed")
		return
	}

	w.WriteHeader(http.StatusOK)
	responseSize, err := io.Copy(w, resp.Body)
	if err != nil {
		s.serverError(w, fmt.Errorf("writing response body: %w", err))
		return
	}

	reqLogger.Info().
		Str("remote", remote).
		Str("proto", r.Proto).
		Int("resp_code", resp.StatusCode).
		Str("handle_dur", time.Since(handleStart).String()).
		Str("upstream_req_dur", time.Since(reqStart).String()).
		Int64("orig_size", contentSize).
		Int("gz_size", buf.Len()).
		Str("ratio", fmt.Sprintf("%.2f", ratio)).
		Int64("resp_size", responseSize).
		Msg("request processed")
}

func (s *Server) verifyBasicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// extract basic auth credentials
		// we're not going to verify them, but they must be present so they can be
		// passed upstream and ultimately to opensearch.
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		r = r.WithContext(context.WithValue(r.Context(), basicAuthUser, username))
		r = r.WithContext(context.WithValue(r.Context(), basicAuthPass, password))

		next.ServeHTTP(w, r)
	})
}
