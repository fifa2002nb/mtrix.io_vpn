// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package stack provides the glue between networking protocols and the
// consumers of the networking stack.
//
// For consumers, the only function of interest is New(), everything else is
// provided by the global/public package.
//
// For protocol implementers, RegisterTransportProtocol() and
// RegisterNetworkProtocol() are used to register protocols with the stack,
// which will then be instantiated when consumers interact with the stack.
package stack

import (
	"errors"
	"fmt"
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/ippool"
	"mtrix.io_vpn/ports"
	"mtrix.io_vpn/waiter"
	"net"
	"sync"
)

type transportProtocolState struct {
	proto          TransportProtocol
	defaultHandler func(*Route, TransportEndpointID, *buffer.VectorisedView) bool
}

// Stack is a networking stack, with all supported protocols, NICs, and route
// table.
type Stack struct {
	transportProtocols map[global.TransportProtocolNumber]*transportProtocolState
	networkProtocols   map[global.NetworkProtocolNumber]NetworkProtocol

	demux *transportDemuxer

	stats global.Stats

	mu   sync.RWMutex
	nics map[global.NICID]*NIC

	// route is the route table passed in by the user via SetRouteTable(),
	// it is used by FindRoute() to build a route for a specific
	// destination.
	routeTable []global.Route

	*ports.PortManager

	// send finalPackage to updconn channel
	// port <-> channel
	//startPort uint16
	//portNum   uint16
	tmu                         sync.RWMutex
	ToNetChan                   chan *global.EndpointData
	ConnectedTransportEndpoints map[[6]byte]global.Endpoint

	*utils.IPPool
}

// New allocates a new networking stack with only the requested networking and
// transport protocols.
func New(addr string, network []string, transport []string) global.Stack {
	s := &Stack{
		transportProtocols: make(map[global.TransportProtocolNumber]*transportProtocolState),
		networkProtocols:   make(map[global.NetworkProtocolNumber]NetworkProtocol),
		nics:               make(map[global.NICID]*NIC),
		PortManager:        ports.NewPortManager(),
		ToNetChan:          make(chan *global.EndpointData, 2048), // fixed
		//startPort:          uint16(40000),                 // fixed
		//portNum:            uint16(1),                     // fixed
		ConnectedTransportEndpoints: make(map[[6]byte]global.Endpoint),
		IPPool: ippool.NewIPPool(addr),
	}

	// Add specified network protocols.
	for _, name := range network {
		netProto, ok := networkProtocols[name]
		if !ok {
			continue
		}

		s.networkProtocols[netProto.Number()] = netProto
	}

	// Add specified transport protocols.
	for _, name := range transport {
		transProto, ok := transportProtocols[name]
		if !ok {
			continue
		}

		s.transportProtocols[transProto.Number()] = &transportProtocolState{
			proto: transProto,
		}
	}

	// Create the global transport demuxer.
	s.demux = newTransportDemuxer(s)

	return s
}

func (s *Stack) GetPacket() *global.EndpointData {
	return <-s.ToNetChan
}

// SetTransportProtocolHandler sets the per-stack default handler for the given
// protocol.
//
// It must be called only during initialization of the stack. Changing it as the
// stack is operating is not supported.
func (s *Stack) SetTransportProtocolHandler(p global.TransportProtocolNumber, h func(*Route, TransportEndpointID, *buffer.VectorisedView) bool) {
	state := s.transportProtocols[p]
	if state != nil {
		state.defaultHandler = h
	}
}

// Stats returns a snapshot of the current stats.
func (s *Stack) Stats() global.Stats {
	return s.stats
}

// SetRouteTable assigns the route table to be used by this stack. It
// specifies which NIC to use for a given destination address mask.
func (s *Stack) SetRouteTable(table []global.Route) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.routeTable = table
}

// NewEndpoint creates a new transport layer endpoint of the given protocol.
func (s *Stack) NewEndpoint(transport global.TransportProtocolNumber, network global.NetworkProtocolNumber, waiterQueue *waiter.Queue) (global.Endpoint, error) {
	t, ok := s.transportProtocols[transport]
	if !ok {
		return nil, global.ErrUnknownProtocol
	}

	return t.proto.NewEndpoint(s, network, waiterQueue)
}

// createNIC creates a NIC with the provided id and link-layer endpoint, and
// optionally enable it.
func (s *Stack) createNIC(id global.NICID, linkEP global.LinkEndpointID, enabled bool) error {
	ep := FindLinkEndpoint(linkEP)
	if ep == nil {
		return global.ErrBadLinkEndpoint
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Make sure id is unique.
	if _, ok := s.nics[id]; ok {
		return global.ErrDuplicateNICID
	}

	n := newNIC(s, id, ep)

	s.nics[id] = n
	if enabled {
		n.attachLinkEndpoint()
	}

	return nil
}

// CreateNIC creates a NIC with the provided id and link-layer endpoint.
func (s *Stack) CreateNIC(id global.NICID, linkEP global.LinkEndpointID) error {
	return s.createNIC(id, linkEP, true)
}

// CreateDisabledNIC creates a NIC with the provided id and link-layer endpoint,
// but leave it disable. Stack.EnableNIC must be called before the link-layer
// endpoint starts delivering packets to it.
func (s *Stack) CreateDisabledNIC(id global.NICID, linkEP global.LinkEndpointID) error {
	return s.createNIC(id, linkEP, false)
}

// EnableNIC enables the given NIC so that the link-layer endpoint can start
// delivering packets to it.
func (s *Stack) EnableNIC(id global.NICID) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nic := s.nics[id]
	if nic == nil {
		return global.ErrUnknownNICID
	}

	nic.attachLinkEndpoint()

	return nil
}

// NICSubnets returns a map of NICIDs to their associated subnets.
func (s *Stack) NICSubnets() map[global.NICID][]global.Subnet {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nics := map[global.NICID][]global.Subnet{}

	for id, nic := range s.nics {
		nics[id] = append(nics[id], nic.Subnets()...)
	}
	return nics
}

// AddAddress adds a new network-layer address to the specified NIC.
func (s *Stack) AddAddress(id global.NICID, protocol global.NetworkProtocolNumber, addr global.Address) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nic := s.nics[id]
	if nic == nil {
		return global.ErrUnknownNICID
	}

	return nic.AddAddress(protocol, addr)
}

// AddSubnet adds a subnet range to the specified NIC.
func (s *Stack) AddSubnet(id global.NICID, protocol global.NetworkProtocolNumber, subnet global.Subnet) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nic := s.nics[id]
	if nic == nil {
		return global.ErrUnknownNICID
	}

	nic.AddSubnet(protocol, subnet)
	return nil
}

// RemoveAddress removes an existing network-layer address from the specified
// NIC.
func (s *Stack) RemoveAddress(id global.NICID, addr global.Address) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nic := s.nics[id]
	if nic == nil {
		return global.ErrUnknownNICID
	}

	return nic.RemoveAddress(addr)
}

// FindRoute creates a route to the given destination address, leaving through
// the given nic and local address (if provided).
func (s *Stack) FindRoute(id global.NICID, localAddr, remoteAddr global.Address, netProto global.NetworkProtocolNumber) (Route, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if nic, ok := s.nics[id]; !ok {
		return Route{}, errors.New(fmt.Sprintf("nic:%v not existed.", id))
	} else {
		if _, err := nic.refIfNotExistedCreateOne(netProto, remoteAddr); nil != err {
			return Route{}, err
		}
	}

	for i := range s.routeTable {
		if id != 0 && id != s.routeTable[i].NIC || !s.routeTable[i].Match(remoteAddr) {
			continue
		}

		nic := s.nics[s.routeTable[i].NIC]
		if nic == nil {
			continue
		}

		var ref *referencedNetworkEndpoint
		if len(localAddr) != 0 {
			ref = nic.findEndpoint(localAddr)
		} else {
			ref = nic.primaryEndpoint(netProto)
		}

		if ref == nil {
			continue
		}

		return makeRoute(netProto, ref.ep.ID().LocalAddress, remoteAddr, ref), nil
	}

	return Route{}, global.ErrNoRoute
}

// CheckNetworkProtocol checks if a given network protocol is enabled in the
// stack.
func (s *Stack) CheckNetworkProtocol(protocol global.NetworkProtocolNumber) bool {
	_, ok := s.networkProtocols[protocol]
	return ok
}

// CheckLocalAddress determines if the given local address exists, and if it
// does, returns the id of the NIC it's bound to. Returns 0 if the address
// does not exist.
func (s *Stack) CheckLocalAddress(nicid global.NICID, addr global.Address) global.NICID {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// If a NIC is specified, we try to find the address there only.
	if nicid != 0 {
		nic := s.nics[nicid]
		if nic == nil {
			return 0
		}

		ref := nic.findEndpoint(addr)
		if ref == nil {
			return 0
		}

		ref.decRef()

		return nic.id
	}

	// Go through all the NICs.
	for _, nic := range s.nics {
		ref := nic.findEndpoint(addr)
		if ref != nil {
			ref.decRef()
			return nic.id
		}
	}

	return 0
}

// SetPromiscuousMode enables or disables promiscuous mode in the given NIC.
func (s *Stack) SetPromiscuousMode(nicID global.NICID, enable bool) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	nic := s.nics[nicID]
	if nic == nil {
		return global.ErrUnknownNICID
	}

	nic.setPromiscuousMode(enable)

	return nil
}

// for udpconn
/*func (s *Stack) RegisterDataChan(uint16 port, dc chan *EndpointData) error {
	if s.startPort > port || s.startPort+portNum <= port {
		return errors.New("port not allowed.")
	}
	if nil == dc {
		return errors.New("endpointdata channel invalided.")
	}
	s.tmu.RLock()
	defer s.tmu.RUnlock()

	s.toNetChan[port-s.startPort] = dc
	return nil
}*/

// for udpconn
func (s *Stack) RegisterConnectedTransportEndpoint(ep global.Endpoint) error {
	if nil == ep.GetNetAddr() {
		return errors.New("nil == ep.GetAddr()")
	}
	s.tmu.RLock()
	defer s.tmu.RUnlock()
	s.ConnectedTransportEndpoints[s.NetAddrHash(ep.GetNetAddr())] = ep
	return nil
}

// for udpconn
func (s *Stack) UnregisterConnectedTransportEndpoint(hash [6]byte) {
	if _, ok := s.ConnectedTransportEndpoints[hash]; ok {
		s.tmu.RLock()
		delete(s.ConnectedTransportEndpoints, hash)
		s.tmu.RUnlock()
	}
}

// for udpconn
func (s *Stack) NetAddrHash(a *net.UDPAddr) [6]byte {
	var b [6]byte
	copy(b[:4], []byte(a.IP)[:4])
	p := uint16(a.Port)
	b[4] = byte((p >> 8) & 0xFF)
	b[5] = byte(p & 0xFF)
	return b
}

// for udpconn
func (s *Stack) GetConnectedTransportEndpointByHash(hash [6]byte) (*global.Endpoint, error) {
	if ep, ok := s.ConnectedTransportEndpoints[hash]; ok {
		return &ep, nil
	} else {
		return nil, errors.New(fmt.Sprintf("connection %v does not existed.", hash))
	}
}

// RegisterTransportEndpoint registers the given endpoint with the stack
// transport dispatcher. Received packets that match the provided id will be
// delivered to the given endpoint; specifying a nic is optional, but
// nic-specific IDs have precedence over global ones.
func (s *Stack) RegisterTransportEndpoint(nicID global.NICID, netProtos []global.NetworkProtocolNumber, protocol global.TransportProtocolNumber, id TransportEndpointID, ep TransportEndpoint) error {
	if nicID == 0 {
		return s.demux.registerEndpoint(netProtos, protocol, id, ep)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	nic := s.nics[nicID]
	if nic == nil {
		return global.ErrUnknownNICID
	}

	return nic.demux.registerEndpoint(netProtos, protocol, id, ep)
}

// UnregisterTransportEndpoint removes the endpoint with the given id from the
// stack transport dispatcher.
func (s *Stack) UnregisterTransportEndpoint(nicID global.NICID, netProtos []global.NetworkProtocolNumber, protocol global.TransportProtocolNumber, id TransportEndpointID) {
	if nicID == 0 {
		s.demux.unregisterEndpoint(netProtos, protocol, id)
		return
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	nic := s.nics[nicID]
	if nic != nil {
		nic.demux.unregisterEndpoint(netProtos, protocol, id)
	}
}
