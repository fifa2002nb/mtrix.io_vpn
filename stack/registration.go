// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	"sync"

	"mtrix.io_vpn/global"
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/waiter"
)

// NetworkEndpointID is the identifier of a network layer protocol endpoint.
// Currently the local address is sufficient because all supported protocols
// (i.e., IPv4 and IPv6) have different sizes for their addresses.
type NetworkEndpointID struct {
	LocalAddress global.Address
}

// TransportEndpointID is the identifier of a transport layer protocol endpoint.
type TransportEndpointID struct {
	// LocalPort is the local port associated with the endpoint.
	LocalPort uint16

	// LocalAddress is the local [network layer] address associated with
	// the endpoint.
	LocalAddress global.Address

	// RemotePort is the remote port associated with the endpoint.
	RemotePort uint16

	// RemoteAddress it the remote [network layer] address associated with
	// the endpoint.
	RemoteAddress global.Address
}

// TransportEndpoint is the interface that needs to be implemented by transport
// protocol (e.g., tcp, udp) endpoints that can handle packets.
type TransportEndpoint interface {
	// HandlePacket is called by the stack when new packets arrive to
	// this transport endpoint.
	ReverseHandlePacket(r *Route, id TransportEndpointID, hdr *buffer.Prependable, vv *buffer.VectorisedView)
}

// TransportProtocol is the interface that needs to be implemented by transport
// protocols (e.g., tcp, udp) that want to be part of the networking stack.
type TransportProtocol interface {
	// Number returns the transport protocol number.
	Number() global.TransportProtocolNumber

	// NewEndpoint creates a new endpoint of the transport protocol.
	NewEndpoint(stack *Stack, netProto global.NetworkProtocolNumber, waitQueue *waiter.Queue) (global.Endpoint, error)

	// MinimumPacketSize returns the minimum valid packet size of this
	// transport protocol. The stack automatically drops any packets smaller
	// than this targeted at this protocol.
	MinimumPacketSize() int

	// ParsePorts returns the source and destination ports stored in a
	// packet of this protocol.
	ParsePorts(v buffer.View) (src, dst uint16, err error)

	// HandleUnknownDestinationPacket handles packets targeted at this
	// protocol but that don't match any existing endpoint. For example,
	// it is targeted at a port that have no listeners.
	HandleUnknownDestinationPacket(r *Route, id TransportEndpointID, vv *buffer.VectorisedView)
}

// TransportDispatcher contains the methods used by the network stack to deliver
// packets to the appropriate transport endpoint after it has been handled by
// the network layer.
type TransportDispatcher interface {
	// DeliverTransportPacket delivers the packets to the appropriate
	// transport protocol endpoint.
	ReverseDeliverTransportPacket(r *Route, protocol global.TransportProtocolNumber, hdr *buffer.Prependable, vv *buffer.VectorisedView)
}

// NetworkEndpoint is the interface that needs to be implemented by endpoints
// of network layer protocols (e.g., ipv4, ipv6).
type NetworkEndpoint interface {
	// MTU is the maximum transmission unit for this endpoint. This is
	// generally calculated as the MTU of the underlying data link endpoint
	// minus the network endpoint max header length.
	MTU() uint32

	// MaxHeaderLength returns the maximum size the network (and lower
	// level layers combined) headers can have. Higher levels use this
	// information to reserve space in the front of the packets they're
	// building.
	MaxHeaderLength() uint16

	// WritePacket writes a packet to the given destination address and
	// protocol.
	WritePacket(r *Route, payload buffer.View, protocol global.TransportProtocolNumber) error

	// ID returns the network protocol endpoint ID.
	ID() *NetworkEndpointID

	// NICID returns the id of the NIC this endpoint belongs to.
	NICID() global.NICID

	// HandlePacket is called by the link layer when new packets arrive to
	// this network endpoint.
	ReverseHandlePacket(r *Route, hdr *buffer.Prependable, vv *buffer.VectorisedView)
}

// NetworkProtocol is the interface that needs to be implemented by network
// protocols (e.g., ipv4, ipv6) that want to be part of the networking stack.
type NetworkProtocol interface {
	// Number returns the network protocol number.
	Number() global.NetworkProtocolNumber

	// MinimumPacketSize returns the minimum valid packet size of this
	// network protocol. The stack automatically drops any packets smaller
	// than this targeted at this protocol.
	MinimumPacketSize() int

	// ParsePorts returns the source and destination addresses stored in a
	// packet of this protocol.
	ParseAddresses(v buffer.View) (src, dst global.Address)

	// NewEndpoint creates a new endpoint of this protocol.
	NewEndpoint(nicid global.NICID, addr global.Address, dispatcher TransportDispatcher, sender LinkEndpoint) (NetworkEndpoint, error)
}

// NetworkDispatcher contains the methods used by the network stack to deliver
// packets to the appropriate network endpoint after it has been handled by
// the data link layer.
type NetworkDispatcher interface {
	// DeliverNetworkPacket finds the appropriate network protocol
	// endpoint and hands the packet over for further processing.
	ReverseDeliverNetworkPacket(linkEP LinkEndpoint, protocol global.NetworkProtocolNumber, hdr *buffer.Prependable, vv *buffer.VectorisedView)
}

// LinkEndpoint is the interface implemented by data link layer protocols (e.g.,
// ethernet, loopback, raw) and used by network layer protocols to send packets
// out through the implementer's data link endpoint.
type LinkEndpoint interface {
	// MTU is the maximum transmission unit for this endpoint. This is
	// usually dictated by the backing physical network; when such a
	// physical network doesn't exist, the limit is generally 64k, which
	// includes the maximum size of an IP packet.
	MTU() uint32

	// MaxHeaderLength returns the maximum size the data link (and
	// lower level layers combined) headers can have. Higher levels use this
	// information to reserve space in the front of the packets they're
	// building.
	MaxHeaderLength() uint16

	// WritePacket writes a packet with the given protocol through the given
	// route.
	WritePacket(r *Route, payload buffer.View, protocol global.NetworkProtocolNumber) error

	// Attach attaches the data link layer endpoint to the network-layer
	// dispatcher of the stack.
	Attach(dispatcher NetworkDispatcher)
}

var (
	transportProtocols = make(map[string]TransportProtocol)
	networkProtocols   = make(map[string]NetworkProtocol)

	linkEPMu           sync.RWMutex
	nextLinkEndpointID global.LinkEndpointID = 1
	linkEndpoints                            = make(map[global.LinkEndpointID]LinkEndpoint)
)

// RegisterTransportProtocol registers a new transport protocol with the stack
// so that it becomes available to users of the stack. This function is intended
// to be called by init() functions of the protocols.
func RegisterTransportProtocol(name string, p TransportProtocol) {
	transportProtocols[name] = p
}

// RegisterNetworkProtocol registers a new network protocol with the stack so
// that it becomes available to users of the stack. This function is intended
// to be called by init() functions of the protocols.
func RegisterNetworkProtocol(name string, p NetworkProtocol) {
	networkProtocols[name] = p
}

// RegisterLinkEndpoint register a link-layer protocol endpoint and returns an
// ID that can be used to refer to it.
func RegisterLinkEndpoint(linkEP LinkEndpoint) global.LinkEndpointID {
	linkEPMu.Lock()
	defer linkEPMu.Unlock()

	v := nextLinkEndpointID
	nextLinkEndpointID++

	linkEndpoints[v] = linkEP

	return v
}

// FindLinkEndpoint finds the link endpoint associated with the given ID.
func FindLinkEndpoint(id global.LinkEndpointID) LinkEndpoint {
	linkEPMu.RLock()
	defer linkEPMu.RUnlock()

	return linkEndpoints[id]
}
