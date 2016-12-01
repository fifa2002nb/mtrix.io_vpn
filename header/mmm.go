// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package header

import (
	"mtrix.io_vpn/global"
)

type MMMFields struct {
}

type MMM []byte

const (
	MMMMinimumSize = 0

	MMMAddressSize = 4

	MMMVersion = 4

	MMMProtocolNumber global.TransportProtocolNumber = 10
)
