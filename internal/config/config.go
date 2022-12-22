// Copyright © 2022 Circonus, Inc. <support@circonus.com>
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      Server      `yaml:"server"`
	Destination Destination `yaml:"destination"`
	Circonus    Circonus    `yaml:"circonus"`
	Debug       bool
}

type Destination struct {
	TLSConfig  *tls.Config
	Host       string `yaml:"host"`
	Port       string `yaml:"port"`
	CAFile     string `yaml:"ca_file"`
	SkipVerify bool   `yaml:"tls_skip_verify"`
	EnableTLS  bool   `yaml:"enable_tls"`
}

type Server struct {
	Address           string `yaml:"listen_address"`      // :19200
	CertFile          string `yaml:"cert_file"`           // empty means no tls
	KeyFile           string `yaml:"key_file"`            // empty means no tls
	ReadTimeout       string `yaml:"read_timeout"`        // 60 second
	WriteTimeout      string `yaml:"write_timeout"`       // 60 second
	IdleTimeout       string `yaml:"idle_timeout"`        // 30 seconds
	ReadHeaderTimeout string `yaml:"read_header_timeout"` // 5 seconds
	HandlerTimeout    string `yaml:"handler_timeout"`     // 30 seconds
}

type Circonus struct {
	APIKey        string `yaml:"api_key"`
	APIURL        string `yaml:"api_url"`
	CheckTarget   string `yaml:"check_target"`
	FlushDuration string `yaml:"flush_interval"`
	FlushInterval time.Duration
}

func cfgFromEnv() Config {
	envPrefix := "C3E_"

	cfg := Config{
		Server: Server{
			Address:           os.Getenv(envPrefix + "SVR_ADDRESS"),
			CertFile:          os.Getenv(envPrefix + "SVR_CERT_FILE"),
			KeyFile:           os.Getenv(envPrefix + "SVR_KEY_FILE"),
			ReadTimeout:       os.Getenv(envPrefix + "SVR_READ_TIMEOUT"),
			WriteTimeout:      os.Getenv(envPrefix + "SVR_WRITE_TIMEOUT"),
			IdleTimeout:       os.Getenv(envPrefix + "SVR_IDLE_TIMEOUT"),
			ReadHeaderTimeout: os.Getenv(envPrefix + "SVR_READ_HEADER_TIMEOUT"),
			HandlerTimeout:    os.Getenv(envPrefix + "SVR_HANDLER_TIMEOUT"),
		},
		Destination: Destination{
			Host:   os.Getenv(envPrefix + "DEST_HOST"),
			Port:   os.Getenv(envPrefix + "DEST_PORT"),
			CAFile: os.Getenv(envPrefix + "DEST_CA_FILE"),
		},
		Circonus: Circonus{
			CheckTarget:   os.Getenv(envPrefix + "CIRC_CHECK_TARGET"),
			APIKey:        os.Getenv(envPrefix + "CIRC_API_KEY"),
			APIURL:        os.Getenv(envPrefix + "CIRC_API_URL"),
			FlushDuration: os.Getenv(envPrefix + "CIRC_FLUSH_INTERVAL"),
		},
	}

	if val, ok := os.LookupEnv(envPrefix + "DEST_ENABLE_TLS"); ok {
		if val != "" {
			setting, err := strconv.ParseBool(val)
			if err != nil {
				log.Warn().Err(err).Str("value", val).Msgf("parsing %sENABLE_TLS", envPrefix)
			} else {
				cfg.Destination.EnableTLS = setting
			}

		}
	}

	if val, ok := os.LookupEnv(envPrefix + "DEST_TLS_SKIP_VERIFY"); ok {
		if val != "" {
			setting, err := strconv.ParseBool(val)
			if err != nil {
				log.Warn().Err(err).Str("value", val).Msgf("parsing %sTLS_SKIP_VERIFY", envPrefix)
			} else {
				cfg.Destination.SkipVerify = setting
			}
		}
	}

	if val, ok := os.LookupEnv(envPrefix + "DEBUG"); ok {
		if val != "" {
			setting, err := strconv.ParseBool(val)
			if err != nil {
				log.Warn().Err(err).Str("value", val).Msgf("parsing %sDEBUG", envPrefix)
			} else {
				cfg.Debug = setting
			}
		}
	}

	return cfg
}

func Load(file string) (*Config, error) {
	if file == "" {
		return nil, fmt.Errorf("invalid config file path (empty)")
	}

	var cfg Config
	data, err := os.ReadFile(file)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Warn().Err(err).Msg("config not found, trying environment")
			cfg = cfgFromEnv()
		} else {
			return nil, err
		}
	} else {
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, err
		}
	}

	if cfg.Destination.Host == "" {
		return nil, fmt.Errorf("invalid config, destination host is required")
	}
	if cfg.Circonus.APIKey == "" {
		return nil, fmt.Errorf("invalid config, circonus api key is required")
	}

	// backfill defaults

	if cfg.Circonus.APIURL == "" {
		cfg.Circonus.APIURL = "https://api.circonus.com/"
	}

	if cfg.Circonus.FlushDuration == "" {
		cfg.Circonus.FlushDuration = "60s"
	}
	dur, err := time.ParseDuration(cfg.Circonus.FlushDuration)
	if err != nil {
		return nil, err
	}
	cfg.Circonus.FlushInterval = dur

	if cfg.Server.Address == "" {
		cfg.Server.Address = ":9200"
	}

	if cfg.Server.ReadTimeout == "" {
		cfg.Server.ReadTimeout = "60s"
	}

	if cfg.Server.WriteTimeout == "" {
		cfg.Server.WriteTimeout = "60s"
	}

	if cfg.Server.IdleTimeout == "" {
		cfg.Server.IdleTimeout = "30s"
	}

	if cfg.Server.ReadHeaderTimeout == "" {
		cfg.Server.ReadHeaderTimeout = "5s"
	}

	if cfg.Server.HandlerTimeout == "" {
		cfg.Server.HandlerTimeout = "30s"
	}

	// create destination TLS Config
	if cfg.Destination.EnableTLS {
		var err error
		tc := &tls.Config{
			MinVersion: tls.VersionTLS12, //nolint:gosec // G402 -- AWS doesn't support TLS13
		}
		if cfg.Destination.CAFile != "" {
			tc, err = loadCAFile(cfg.Destination.CAFile)
			if err != nil {
				log.Fatal().Err(err).Str("ca_file", cfg.Destination.CAFile).Msg("loading destination ca file")
			}
		}
		if cfg.Destination.SkipVerify {
			tc.InsecureSkipVerify = true
		}
		cfg.Destination.TLSConfig = tc
	}

	return &cfg, nil
}

func loadCAFile(fn string) (*tls.Config, error) {
	data, err := os.ReadFile(fn)
	if err != nil {
		return nil, err
	}

	ca := x509.NewCertPool()
	ok := ca.AppendCertsFromPEM(data)
	if !ok {
		return nil, fmt.Errorf("failed to parse ca certificate")
	}

	return &tls.Config{
		RootCAs:    ca,
		MinVersion: tls.VersionTLS13,
	}, nil
}
