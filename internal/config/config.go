// Copyright © 2022 Circonus, Inc. <support@circonus.com>
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
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
	SkipVerify bool   `yaml:"skip_verify"`
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
	FlushDuration string `yaml:"flush_interval"`
	FlushInterval time.Duration
}

func Load(file string) (*Config, error) {
	if file == "" {
		return nil, fmt.Errorf("invalid config file path (empty)")
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
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
		tc := &tls.Config{MinVersion: tls.VersionTLS13}
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
