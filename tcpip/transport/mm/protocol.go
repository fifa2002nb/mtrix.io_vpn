// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mm

import (
	"mtrix.io_vpn/tcpip"
	"mtrix.io_vpn/tcpip/buffer"
	"mtrix.io_vpn/tcpip/header"
	"mtrix.io_vpn/tcpip/stack"
	"mtrix.io_vpn/waiter"
)

const (
	// ProtocolName is the string representation of the udp protocol name.
	ProtocolName = "mm"

	// ProtocolNumber is the udp protocol number.
	ProtocolNumber = header.MMProtocolNumber
)

type protocol struct{}

// Number returns the udp protocol number.
func (*protocol) Number() tcpip.TransportProtocolNumber {
	return ProtocolNumber
}

// NewEndpoint creates a new udp endpoint.
func (*protocol) NewEndpoint(stack *stack.Stack, netProto tcpip.NetworkProtocolNumber, waiterQueue *waiter.Queue) (tcpip.Endpoint, error) {
	return newEndpoint(stack, netProto, waiterQueue), nil
}

// MinimumPacketSize returns the minimum valid udp packet size.
func (*protocol) MinimumPacketSize() int {
	return header.MMMinimumSize
}

// ParsePorts returns the source and destination ports stored in the given udp
// packet.
func (*protocol) ParsePorts(v buffer.View) (src, dst uint16, err error) {
	return 888, 999, nil
}

// HandleUnknownDestinationPacket handles packets targeted at this protocol but
// that don't match any existing endpoint.
func (p *protocol) HandleUnknownDestinationPacket(*stack.Route, stack.TransportEndpointID, *buffer.VectorisedView) {
}

func init() {
	stack.RegisterTransportProtocol(ProtocolName, &protocol{})
}
