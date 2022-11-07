// Copyright © 2022 Circonus, Inc. <support@circonus.com>
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

package server

import (
	"github.com/circonus-labs/go-apiclient"
	"github.com/circonus-labs/go-trapcheck"
	"github.com/circonus-labs/go-trapmetrics"
	"github.com/circonus/c3-exporter/internal/config"
)

func initMetrics(cfg config.Circonus) (*trapmetrics.TrapMetrics, error) {
	client, err := apiclient.New(&apiclient.Config{TokenKey: cfg.APIKey, URL: cfg.APIURL})
	if err != nil {
		return nil, err
	}

	check, err := trapcheck.New(&trapcheck.Config{Client: client})
	if err != nil {
		return nil, err
	}

	trap, err := trapmetrics.New(&trapmetrics.Config{Trap: check})
	if err != nil {
		return nil, err
	}

	return trap, nil
}
