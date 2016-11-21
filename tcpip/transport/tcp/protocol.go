// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package tcp contains the implementation of the TCP transport protocol. To use
// it in the networking stack, this package must be added to the project, and
// activated on the stack by passing tcp.ProtocolName (or "tcp") as one of the
// transport protocols when calling stack.New(). Then endpoints can be created
// by passing tcp.ProtocolNumber as the transport protocol number when calling
// Stack.NewEndpoint().
package tcp

import (
	"mtrix.io_vpn/tcpip"
	"mtrix.io_vpn/tcpip/buffer"
	"mtrix.io_vpn/tcpip/header"
	"mtrix.io_vpn/tcpip/seqnum"
	"mtrix.io_vpn/tcpip/stack"
	"mtrix.io_vpn/waiter"
)

const (
	// ProtocolName is the string representation of the tcp protocol name.
	ProtocolName = "tcp"

	// ProtocolNumber is the tcp protocol number.
	ProtocolNumber = header.TCPProtocolNumber
)

type protocol struct{}

// Number returns the tcp protocol number.
func (*protocol) Number() tcpip.TransportProtocolNumber {
	return ProtocolNumber
}

// NewEndpoint creates a new tcp endpoint.
func (*protocol) NewEndpoint(stack *stack.Stack, netProto tcpip.NetworkProtocolNumber, waiterQueue *waiter.Queue) (tcpip.Endpoint, error) {
	return newEndpoint(stack, netProto, waiterQueue), nil
}

// MinimumPacketSize returns the minimum valid tcp packet size.
func (*protocol) MinimumPacketSize() int {
	return header.TCPMinimumSize
}

// ParsePorts returns the source and destination ports stored in the given tcp
// packet.
func (*protocol) ParsePorts(v buffer.View) (src, dst uint16, err error) {
	h := header.TCP(v)
	return h.SourcePort(), h.DestinationPort(), nil
}

// HandleUnknownDestinationPacket handles packets targeted at this protocol but
// that don't match any existing endpoint.
//
// RFC 793, page 36, states that "If the connection does not exist (CLOSED) then
// a reset is sent in response to any incoming segment except another reset. In
// particular, SYNs addressed to a non-existent connection are rejected by this
// means."
func (*protocol) HandleUnknownDestinationPacket(r *stack.Route, id stack.TransportEndpointID, vv *buffer.VectorisedView) {
	s := newSegment(r, id, vv)
	defer s.decRef()

	if !s.parse() {
		// TODO: Inform stack about malformed packet.
		return
	}

	// There's nothing to do if this is already a reset packet.
	if s.flagIsSet(flagRst) {
		return
	}

	replyWithReset(s)
}

// replyWithReset replies to the given segment with a reset segment.
func replyWithReset(s *segment) {
	// Get the seqnum from the packet if the ack flag is set.
	seq := seqnum.Value(0)
	if s.flagIsSet(flagAck) {
		seq = s.ackNumber
	}

	ack := s.sequenceNumber.Add(s.logicalLen())

	sendTCP(&s.route, s.id, nil, flagRst|flagAck, seq, ack, 0)
}

func init() {
	stack.RegisterTransportProtocol(ProtocolName, &protocol{})
}
