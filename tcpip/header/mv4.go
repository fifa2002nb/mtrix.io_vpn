// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package header

import (
	"mtrix.io_vpn/tcpip"
)

const (
	randnum = 0
)

type Mv4Fields struct {
	RandNum uint32
}

type Mv4 []byte

const (
	Mv4MinimumSize = 4

	MMv4AddressSize = 4

	Mv4Version = 4

	Mv4ProtocolNumber tcpip.NetworkProtocolNumber = 0x9998
)

func (b Mv4) RandNum() uint32 {
	return uint32(binary.BigEndian.Uint32[b[randum:]])
}

func (b Mv4) SetRandNum(randNum uint32) {
	binary.BigEndian.PutUint32(b[randnum:], randNum)
}

func (b Mv4) Encode(i *Mv4Fields) {
	b.SetRandNum(i.RandNum)
}
