// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mm

import (
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/header"
	"mtrix.io_vpn/stack"
)

const (
	ProtocolName = "mm"

	// ProtocolNumber is the mm protocol number.
	ProtocolNumber = header.MMProtocolNumber

	maxTotalSize = 0xffff
)

type endpoint struct {
	nicid      global.NICID
	id         stack.NetworkEndpointID
	address    address
	linkEP     stack.LinkEndpoint
	dispatcher stack.TransportDispatcher
}

type address [header.MMAddressSize]byte

func newEndpoint(nicid global.NICID, addr global.Address, dispatcher stack.TransportDispatcher, linkEP stack.LinkEndpoint) *endpoint {
	e := &endpoint{
		nicid:      nicid,
		linkEP:     linkEP,
		dispatcher: dispatcher,
	}
	copy(e.address[:], addr)
	e.id = stack.NetworkEndpointID{global.Address(e.address[:])}

	return e
}

// MTU implements stack.NetworkEndpoint.MTU. It returns the link-layer MTU minus
// the network layer max header length.
func (e *endpoint) MTU() uint32 {
	lmtu := e.linkEP.MTU()
	if lmtu > maxTotalSize {
		lmtu = maxTotalSize
	}
	return lmtu - uint32(e.MaxHeaderLength())
}

// NICID returns the ID of the NIC this endpoint belongs to.
func (e *endpoint) NICID() global.NICID {
	return e.nicid
}

// ID returns the mv4 endpoint ID.
func (e *endpoint) ID() *stack.NetworkEndpointID {
	return &e.id
}

// MaxHeaderLength returns the maximum length needed by mpv4 headers (and
// underlying protocols).
func (e *endpoint) MaxHeaderLength() uint16 {
	return e.linkEP.MaxHeaderLength() + header.MMMinimumSize
}

// WritePacket writes a packet to the given destination address and protocol.
func (e *endpoint) WritePacket(r *stack.Route, payload buffer.View, protocol global.TransportProtocolNumber) error {
	// 取MM协议头部
	m := header.MM(payload)
	// 做一些MM协议的验证
	if header.MM_FLG_DAT != m.Flag() || m.PayloadLength() >= m.TotalLength() {
		return nil
	}
	// 剥掉MM协议的头部，发送数据部分
    payload.TrimFront(header.MMMinimumSize)
	return e.linkEP.WritePacket(r, payload, ProtocolNumber)
}

func (e *endpoint) ReverseHandlePacket(r *stack.Route, vv *buffer.VectorisedView) {
	h := header.IPv4(vv.First())
	if !h.IsValid(vv.Size()) {
		return
	}

    hdr := buffer.NewPrependable(int(r.MaxHeaderLength()))

	// 组装MM协议头部
	m := header.MM(hdr.Prepend(header.MMMinimumSize))

	m.Encode(&header.MMFields{
		Magic:         uint16(header.MMMagic), // MM协议的魔幻数
		Flag:          header.MM_FLG_DAT,      // 暂时初始化为DAT类型
		Seq:           uint32(0),              // 暂时初始化为0
		SessionId:     r.LocalAddress,         // sessionId用客户端4字节IP来代替
		PayloadLength: uint16(vv.Size()),      // 数据报大小
		TotalLength:   uint16(0),              // 暂时初始化为0，传输层中增加noise后计算总长度
	})

	// do something
	e.dispatcher.ReverseDeliverTransportPacket(r, header.MMMProtocolNumber, &hdr, vv)
}

type protocol struct{}

// NewProtocol creates a new protocol mv4 protocol descriptor. This is exported
// only for tests that short-circuit the stack. Regular use of the protocol is
// done via the stack, which gets a protocol descriptor from the init() function
// below.
func NewProtocol() stack.NetworkProtocol {
	return &protocol{}
}

// Number returns the mv4 protocol number.
func (p *protocol) Number() global.NetworkProtocolNumber {
	return ProtocolNumber
}

// MinimumPacketSize returns the minimum valid mv4 packet size.
func (p *protocol) MinimumPacketSize() int {
	return header.MMMinimumSize
}

// ParseAddresses implements NetworkProtocol.ParseAddresses.
func (*protocol) ParseAddresses(v buffer.View) (src, dst global.Address) {
	h := header.IPv4(v)
	return h.SourceAddress(), h.DestinationAddress()
}

// NewEndpoint creates a new mv4 endpoint.
func (p *protocol) NewEndpoint(nicid global.NICID, addr global.Address, dispatcher stack.TransportDispatcher, linkEP stack.LinkEndpoint) (stack.NetworkEndpoint, error) {
	return newEndpoint(nicid, addr, dispatcher, linkEP), nil
}

func init() {
	stack.RegisterNetworkProtocol(ProtocolName, NewProtocol())
}
