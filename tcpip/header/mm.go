// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package header

import (
	"encoding/binary"
	"mtrix.io_vpn/tcpip"
)

const (
	flag          = 0
	seq           = 1
	sessionId     = 5
	payloadLength = 9
	totalLength   = 11
)

type MMFields struct {
	Flag          byte
	Seq           uint32
	SessionId     uint32
	PayloadLength uint16 // payload length
	TotalLength   uint16 // total length = header_length + payload_length + noise_length
}

type MM []byte

const (
	MM_FLG_PSH byte = 0x80 // port knocking and heartbeat
	MM_FLG_HSH byte = 0x40 // handshaking
	MM_FLG_FIN byte = 0x20 // finish session
	MM_FLG_MFR byte = 0x08 // more fragments
	MM_FLG_ACK byte = 0x04 // acknowledge
	MM_FLG_DAT byte = 0x00 // acknowledge

	MM_STAT_INIT      int32 = iota // initing
	MM_STAT_HANDSHAKE              // handeshaking
	MM_STAT_WORKING                // working
	MM_STAT_FIN                    // finishing

	MMMinimumSize = 12

	MMVersion = 4

	MMProtocolNumber tcpip.TransportProtocolNumber = 9
)

func (b MM) Flag() byte {
	return b[flag]
}

func (b MM) SetFlag(flag byte) {
	b[flag] = flag
}

func (b MM) Seq() uint32 {
	return uint32(binary.BigEndian.Uint32(b[seq:]))
}

func (b MM) SetSeq(seq uint32) {
	binary.BigEndian.PutUint32(b[seq:], seq)
}

func (b MM) SessionId() uint32 {
	return uint32(binary.BigEndian.Uint32(b[sessionId:]))
}

func (b MM) SetSessionId(sessionId uint32) {
	binary.BigEndian.PutUint32(b[sessionId:], sessionId)
}

func (b MM) PayloadLength() uint16 {
	return uint16(binary.BigEndian.Uint16(b[payloadLength:]))
}

func (b MM) SetPayloadLength(payloadLength uint16) {
	binary.BigEndian.PutUint16(b[payloadLength:], payloadLength)
}

func (b MM) TotalLength() uint16 {
	return uint16(binary.BigEndian.Uint16(b[totalLength:]))
}

func (b MM) SetTotalLength(totalLength uint16) {
	binary.BigEndian.PutUint16(b[totalLength:], totalLength)
}

func (b MM) Payload() []byte {
	return b[MMMinimumSize:][:b.PayloadLength()]
}

func (b MM) Encode(i *MMFields) {
	b.SetFlag(i.Flag)
	b.SetSeq(i.Seq)
	b.SetSessionId(i.SessionId)
	b.SetPayloadLength(i.PayloadLength)
	b.SetTotalLength(i.TotalLength)
}

// IsValid performs basic validation on the packet.
func (b MM) IsValid(pktSize int) bool {
	if len(b) < MMMinimumSize {
		return false
	}

	payloadLength := int(b.PayloadLength())
	totalLength := int(b.TotalLength())
	if payloadLength > totalLength || totalLength > pktSize {
		return false
	}

	return true
}
