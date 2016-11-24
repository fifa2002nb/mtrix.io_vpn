// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package header

import (
	"mtrix.io_vpn/tcpip"
)

type Mv4Fields struct {
}

type Mv4 []byte

const (
	Mv4MinimumSize = 0

	Mv4Version = 4

	Mv4ProtocolNumber tcpip.NetworkProtocolNumber = 0x9998
)

func (b Mv4) Encode(i *Mv4Fields) {
}
