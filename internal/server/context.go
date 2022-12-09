// Copyright © 2022 Circonus, Inc. <support@circonus.com>
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//

package server

type contextKey string

const (
	basicAuthUser = contextKey("basicAuthUser")
	basicAuthPass = contextKey("basicAuthPass")
)
