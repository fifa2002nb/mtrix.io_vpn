// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/header"
)

// Route represents a route through the networking stack to a given destination.
type Route struct {
	// RemoteAddress is the final destination of the route.
	RemoteAddress global.Address

	// LocalAddress is the local address where the route starts.
	LocalAddress global.Address

	// NextHop is the next node in the path to the destination.
	NextHop global.Address

	// NetProto is the network-layer protocol.
	NetProto global.NetworkProtocolNumber

	// ref a reference to the network endpoint through which the route
	// starts.
	ref *referencedNetworkEndpoint
}

// makeRoute initializes a new route. It takes ownership of the provided
// reference to a network endpoint.
func makeRoute(netProto global.NetworkProtocolNumber, localAddr, remoteAddr global.Address, ref *referencedNetworkEndpoint) Route {
	return Route{
		NetProto:      netProto,
		LocalAddress:  localAddr,
		RemoteAddress: remoteAddr,
		ref:           ref,
	}
}

// NICID returns the id of the NIC from which this route originates.
func (r *Route) NICID() global.NICID {
	return r.ref.ep.NICID()
}

// MaxHeaderLength forwards the call to the network endpoint's implementation.
func (r *Route) MaxHeaderLength() uint16 {
	return r.ref.ep.MaxHeaderLength()
}

// PseudoHeaderChecksum forwards the call to the network endpoint's
// implementation.
func (r *Route) PseudoHeaderChecksum(protocol global.TransportProtocolNumber) uint16 {
	return header.PseudoHeaderChecksum(protocol, r.LocalAddress, r.RemoteAddress)
}

// WritePacket writes the packet through the given route.
func (r *Route) WritePacket(payload buffer.View, protocol global.TransportProtocolNumber) error {
	return r.ref.ep.WritePacket(r, payload, protocol)
}

// MTU returns the MTU of the underlying network endpoint.
func (r *Route) MTU() uint32 {
	return r.ref.ep.MTU()
}

// Release frees all resources associated with the route.
func (r *Route) Release() {
	if r.ref != nil {
		r.ref.decRef()
		r.ref = nil
	}
}

// Clone Clone a route such that the original one can be released and the new
// one will remain valid.
func (r *Route) Clone() Route {
	r.ref.incRef()
	return *r
}
