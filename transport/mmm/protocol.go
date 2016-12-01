// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mmm

import (
	"math/rand"
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/header"
	"mtrix.io_vpn/stack"
	"mtrix.io_vpn/waiter"
)

const (
	// ProtocolName is the string representation of the udp protocol name.
	ProtocolName = "mmm"

	// ProtocolNumber is the mmm protocol number.
	ProtocolNumber = header.MMMProtocolNumber
)

var (
	StartPort = 40000 //起始端口号
	PortNum   = 10    //端口数，将生成PortNum个数据管道
)

type protocol struct{}

// Number returns the mmm protocol number.
func (*protocol) Number() global.TransportProtocolNumber {
	return ProtocolNumber
}

// NewEndpoint creates a new udp endpoint.
func (*protocol) NewEndpoint(stack *stack.Stack, netProto global.NetworkProtocolNumber, waiterQueue *waiter.Queue) (global.Endpoint, error) {
	return newEndpoint(stack, netProto, waiterQueue), nil
}

// MinimumPacketSize returns the minimum valid udp packet size.
func (*protocol) MinimumPacketSize() int {
	return header.MMMMinimumSize
}

// ParsePorts returns the source and destination ports stored in the given udp
// packet.
func (*protocol) ParsePorts(v buffer.View) (src, dst uint16, err error) {
	dst = uint16(StartPort) + uint16(rand.Intn(10000))%uint16(PortNum)
	return 888, dst, nil
}

// HandleUnknownDestinationPacket handles packets targeted at this protocol but
// that don't match any existing endpoint.
func (p *protocol) HandleUnknownDestinationPacket(*stack.Route, stack.TransportEndpointID, *buffer.VectorisedView) {
}

func init() {
	stack.RegisterTransportProtocol(ProtocolName, &protocol{})
}
