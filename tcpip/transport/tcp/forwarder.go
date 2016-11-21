// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tcp

import (
	"sync"

	"mtrix.io_vpn/tcpip"
	"mtrix.io_vpn/tcpip/buffer"
	"mtrix.io_vpn/tcpip/seqnum"
	"mtrix.io_vpn/tcpip/stack"
	"mtrix.io_vpn/waiter"
)

// Forwarder is a connection request forwarder, which allows clients to decide
// what to do with a connection request, for example: ignore it, send a RST, or
// attempt to complete the 3-way handshake.
//
// The canonical way of using it is to pass the Forwarder.HandlePacket function
// to stack.SetTransportProtocolHandler.
type Forwarder struct {
	maxInFlight int
	handler     func(*ForwarderRequest)

	mu       sync.Mutex
	inFlight map[stack.TransportEndpointID]struct{}
	listen   *listenContext
}

// NewForwarder allocates and initializes a new forwarder with the given
// maximum number of in-flight connection attempts. Once the maximum is reached
// new incoming connection requests will be ignored.
func NewForwarder(s *stack.Stack, rcvWnd, maxInFlight int, handler func(*ForwarderRequest)) *Forwarder {
	return &Forwarder{
		maxInFlight: maxInFlight,
		handler:     handler,
		inFlight:    make(map[stack.TransportEndpointID]struct{}),
		listen:      newListenContext(s, seqnum.Size(rcvWnd), true, 0),
	}
}

// HandlePacket handles a packet if it is of interest to the forwarder (i.e., if
// it's a SYN packet), returning true if it's the case. Otherwise the packet
// is not handled and false is returned.
//
// This function is expected to be passed as an argument to the
// stack.SetTransportProtocolHandler function.
func (f *Forwarder) HandlePacket(r *stack.Route, id stack.TransportEndpointID, vv *buffer.VectorisedView) bool {
	s := newSegment(r, id, vv)
	defer s.decRef()

	// We only care about well-formed SYN packets.
	if !s.parse() || s.flags != flagSyn {
		return false
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// We have an inflight request for this id, ignore this one for now.
	if _, ok := f.inFlight[id]; ok {
		return true
	}

	// Ignore the segment if we're beyond the limit.
	if len(f.inFlight) >= f.maxInFlight {
		return true
	}

	// Launch a new goroutine to handle the request.
	f.inFlight[id] = struct{}{}
	s.incRef()
	go f.handler(&ForwarderRequest{
		forwarder: f,
		segment:   s,
	})

	return true
}

// ForwarderRequest represents a connection request received by the forwarder
// and passed to the client. Clients must eventually call Complete() on it, and
// may optionally create an endpoint to represent it via CreateEndpoint.
type ForwarderRequest struct {
	mu        sync.Mutex
	forwarder *Forwarder
	segment   *segment
}

// ID returns the 4-tuple (src address, src port, dst address, dst port) that
// represents the connection request.
func (r *ForwarderRequest) ID() stack.TransportEndpointID {
	return r.segment.id
}

// Complete completes the request, and optinally sends a RST segment back to the
// sender.
func (r *ForwarderRequest) Complete(sendReset bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.segment == nil {
		panic("Completing already completed forwarder request")
	}

	// Remove request from the forwarder.
	r.forwarder.mu.Lock()
	delete(r.forwarder.inFlight, r.segment.id)
	r.forwarder.mu.Unlock()

	// If the caller requested, send a reset.
	if sendReset {
		replyWithReset(r.segment)
	}

	// Release all resources.
	r.segment.decRef()
	r.segment = nil
	r.forwarder = nil
}

// CreateEndpoint creates a TCP endpoint for the connection request, performing
// the 3-way handshake in the process.
func (r *ForwarderRequest) CreateEndpoint(queue *waiter.Queue) (tcpip.Endpoint, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.segment == nil {
		return nil, tcpip.ErrInvalidEndpointState
	}

	f := r.forwarder
	ep, err := f.listen.createEndpointAndPerformHandshake(r.segment)
	if err != nil {
		return nil, err
	}

	// Start the protocol goroutine.
	ep.startAcceptedLoop(queue)

	return ep, nil
}
