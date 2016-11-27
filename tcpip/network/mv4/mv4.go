// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mv4

import (
	"mtrix.io_vpn/tcpip"
	"mtrix.io_vpn/tcpip/buffer"
	"mtrix.io_vpn/tcpip/header"
	"mtrix.io_vpn/tcpip/stack"
)

const (
	ProtocolName = "mv4"

	// ProtocolNumber is the mv4 protocol number.
	ProtocolNumber = header.Mv4ProtocolNumber

	maxTotalSize = 0xffff
)

type endpoint struct {
	nicid      tcpip.NICID
	id         stack.NetworkEndpointID
	address    address
	linkEP     stack.LinkEndpoint
	dispatcher stack.TransportDispatcher
}

type address [header.MMv4AddressSize]byte

func newEndpoint(nicid tcpip.NICID, addr tcpip.Address, dispatcher stack.TransportDispatcher, linkEP stack.LinkEndpoint) *endpoint {
	e := &endpoint{
		nicid:      nicid,
		linkEP:     linkEP,
		dispatcher: dispatcher,
	}
	copy(e.address[:], addr)
	e.id = stack.NetworkEndpointID{tcpip.Address(e.address[:])}

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
func (e *endpoint) NICID() tcpip.NICID {
	return e.nicid
}

// ID returns the mv4 endpoint ID.
func (e *endpoint) ID() *stack.NetworkEndpointID {
	return &e.id
}

// MaxHeaderLength returns the maximum length needed by mpv4 headers (and
// underlying protocols).
func (e *endpoint) MaxHeaderLength() uint16 {
	return e.linkEP.MaxHeaderLength() + header.Mv4MinimumSize
}

// WritePacket writes a packet to the given destination address and protocol.
func (e *endpoint) WritePacket(r *stack.Route, hdr *buffer.Prependable, payload buffer.View, protocol tcpip.TransportProtocolNumber) error {
	return e.linkEP.WritePacket(r, nil, payload, ProtocolNumber)
}

// HandlePacket is called by the link layer when new mv4 packets arrive for
// this endpoint.
func (e *endpoint) HandlePacket(r *stack.Route, vv *buffer.VectorisedView) {
	h := header.IPv4(vv.First())
	if !h.IsValid(vv.Size()) {
		return
	}

	// only simply dispatch to MM Protocol
	e.dispatcher.DeliverTransportPacket(r, header.MMProtocolNumber, vv)
}

func (e *endpoint) ReverseHandlePacket(r *Route, hdr *buffer.Prependable, vv *buffer.VectorisedView) {
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
func (p *protocol) Number() tcpip.NetworkProtocolNumber {
	return ProtocolNumber
}

// MinimumPacketSize returns the minimum valid mv4 packet size.
func (p *protocol) MinimumPacketSize() int {
	return header.Mv4MinimumSize
}

// ParseAddresses implements NetworkProtocol.ParseAddresses.
func (*protocol) ParseAddresses(v buffer.View) (src, dst tcpip.Address) {
	// src:10.1.1.3, dst:10.1.1.2
	return tcpip.Address("\x0A\x01\x01\x03"), tcpip.Address("\x0A\x01\x01\x02")
}

// NewEndpoint creates a new mv4 endpoint.
func (p *protocol) NewEndpoint(nicid tcpip.NICID, addr tcpip.Address, dispatcher stack.TransportDispatcher, linkEP stack.LinkEndpoint) (stack.NetworkEndpoint, error) {
	return newEndpoint(nicid, addr, dispatcher, linkEP), nil
}

func init() {
	stack.RegisterNetworkProtocol(ProtocolName, NewProtocol())
}
