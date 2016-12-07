// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package udp contains the implementation of the UDP transport protocol. To use
// it in the networking stack, this package must be added to the project, and
// activated on the stack by passing udp.ProtocolName (or "udp") as one of the
// transport protocols when calling stack.New(). Then endpoints can be created
// by passing udp.ProtocolNumber as the transport protocol number when calling
// Stack.NewEndpoint().
package udp

import (
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/header"
	"mtrix.io_vpn/stack"
	"mtrix.io_vpn/waiter"
)

const (
	// ProtocolName is the string representation of the udp protocol name.
	ProtocolName = "udp"

	// ProtocolNumber is the udp protocol number.
	ProtocolNumber = header.UDPProtocolNumber
)

type protocol struct{}

// Number returns the udp protocol number.
func (*protocol) Number() global.TransportProtocolNumber {
	return ProtocolNumber
}

// NewEndpoint creates a new udp endpoint.
func (*protocol) NewEndpoint(stack *stack.Stack, netProto global.NetworkProtocolNumber, waiterQueue *waiter.Queue) (global.Endpoint, error) {
	return newEndpoint(stack, netProto, waiterQueue), nil
}

// MinimumPacketSize returns the minimum valid udp packet size.
func (*protocol) MinimumPacketSize() int {
	return header.UDPMinimumSize
}

// ParsePorts returns the source and destination ports stored in the given udp
// packet.
func (*protocol) ParsePorts(v buffer.View) (src, dst uint16, err error) {
	h := header.UDP(v)
	return h.SourcePort(), h.DestinationPort(), nil
}

// HandleUnknownDestinationPacket handles packets targeted at this protocol but
// that don't match any existing endpoint.
func (p *protocol) HandleUnknownDestinationPacket(*stack.Route, stack.TransportEndpointID, *buffer.VectorisedView) {
}

func init() {
	stack.RegisterTransportProtocol(ProtocolName, &protocol{})
}
