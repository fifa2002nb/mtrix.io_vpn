// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	log "github.com/Sirupsen/logrus"
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/ilist"
	"strings"
	"sync"
	"sync/atomic"
)

// NIC represents a "network interface card" to which the networking stack is
// attached.
type NIC struct {
	stack  *Stack
	id     global.NICID
	linkEP LinkEndpoint

	demux *transportDemuxer

	mu          sync.RWMutex
	promiscuous bool
	primary     map[global.NetworkProtocolNumber]*ilist.List
	endpoints   map[NetworkEndpointID]*referencedNetworkEndpoint
	subnets     []global.Subnet
}

func newNIC(stack *Stack, id global.NICID, ep LinkEndpoint) *NIC {
	return &NIC{
		stack:     stack,
		id:        id,
		linkEP:    ep,
		demux:     newTransportDemuxer(stack),
		primary:   make(map[global.NetworkProtocolNumber]*ilist.List),
		endpoints: make(map[NetworkEndpointID]*referencedNetworkEndpoint),
	}
}

// attachLinkEndpoint attaches the NIC to the endpoint, which will enable it
// to start delivering packets.
func (n *NIC) attachLinkEndpoint() {
	n.linkEP.Attach(n)
}

// setPromiscuousMode enables or disables promiscuous mode.
func (n *NIC) setPromiscuousMode(enable bool) {
	n.mu.Lock()
	n.promiscuous = enable
	n.mu.Unlock()
}

// primaryEndpoint returns the primary endpoint of n for the given network
// protocol.
func (n *NIC) primaryEndpoint(protocol global.NetworkProtocolNumber) *referencedNetworkEndpoint {
	n.mu.RLock()
	defer n.mu.RUnlock()

	list := n.primary[protocol]
	if list == nil {
		return nil
	}

	for e := list.Front(); e != nil; e = e.Next() {
		r := e.(*referencedNetworkEndpoint)
		if r.tryIncRef() {
			return r
		}
	}

	return nil
}

// findEndpoint finds the endpoint, if any, with the given address.
func (n *NIC) findEndpoint(address global.Address) *referencedNetworkEndpoint {
	n.mu.RLock()
	defer n.mu.RUnlock()

	ref := n.endpoints[NetworkEndpointID{address}]
	if ref == nil || !ref.tryIncRef() {
		return nil
	}

	return ref
}

func (n *NIC) refIfNotExistedCreateOne(protocol global.NetworkProtocolNumber, dst global.Address) (*referencedNetworkEndpoint, error) {
	id := NetworkEndpointID{dst}
	if ref, ok := n.endpoints[id]; !ok {
		return n.addAddressLocked(protocol, dst, false)
	} else {
		return ref, nil
	}
}

func (n *NIC) addAddressLocked(protocol global.NetworkProtocolNumber, addr global.Address, replace bool) (*referencedNetworkEndpoint, error) {
	netProto, ok := n.stack.networkProtocols[protocol]
	if !ok {
		return nil, global.ErrUnknownProtocol
	}

	// Create the new network endpoint.
	ep, err := netProto.NewEndpoint(n.id, addr, n, n.linkEP)
	if err != nil {
		return nil, err
	}

	id := *ep.ID()
	if ref, ok := n.endpoints[id]; ok {
		if !replace {
			return nil, global.ErrDuplicateAddress
		}

		n.removeEndpointLocked(ref)
	}

	ref := newReferencedNetworkEndpoint(ep, protocol, n)

	n.endpoints[id] = ref

	l, ok := n.primary[protocol]
	if !ok {
		l = &ilist.List{}
		n.primary[protocol] = l
	}

	l.PushBack(ref)

	return ref, nil
}

// AddAddress adds a new address to n, so that it starts accepting packets
// targeted at the given address (and network protocol).
func (n *NIC) AddAddress(protocol global.NetworkProtocolNumber, addr global.Address) error {
	// Add the endpoint.
	n.mu.Lock()
	_, err := n.addAddressLocked(protocol, addr, false)
	n.mu.Unlock()

	return err
}

// AddSubnet adds a new subnet to n, so that it starts accepting packets
// targeted at the given address and network protocol.
func (n *NIC) AddSubnet(protocol global.NetworkProtocolNumber, subnet global.Subnet) {
	n.mu.Lock()
	n.subnets = append(n.subnets, subnet)
	n.mu.Unlock()
}

// Subnets returns the Subnets associated with this NIC.
func (n *NIC) Subnets() []global.Subnet {
	n.mu.RLock()
	defer n.mu.RUnlock()
	sns := make([]global.Subnet, 0, len(n.subnets)+len(n.endpoints))
	for nid := range n.endpoints {
		sn, err := global.NewSubnet(nid.LocalAddress, global.AddressMask(strings.Repeat("\xff", len(nid.LocalAddress))))
		if err != nil {
			// This should never happen as the mask has been carefully crafted to
			// match the address.
			panic("Invalid endpoint subnet: " + err.Error())
		}
		sns = append(sns, sn)
	}
	return append(sns, n.subnets...)
}

func (n *NIC) removeEndpointLocked(r *referencedNetworkEndpoint) {
	id := *r.ep.ID()

	// Nothing to do if the reference has already been replaced with a
	// different one.
	if n.endpoints[id] != r {
		return
	}

	if r.holdsInsertRef {
		panic("Reference count dropped to zero before being removed")
	}

	delete(n.endpoints, id)
	n.primary[r.protocol].Remove(r)
}

func (n *NIC) removeEndpoint(r *referencedNetworkEndpoint) {
	n.mu.Lock()
	n.removeEndpointLocked(r)
	n.mu.Unlock()
}

// RemoveAddress removes an address from n.
func (n *NIC) RemoveAddress(addr global.Address) error {
	n.mu.Lock()
	r := n.endpoints[NetworkEndpointID{addr}]
	if r == nil || !r.holdsInsertRef {
		n.mu.Unlock()
		return global.ErrBadLocalAddress
	}

	r.holdsInsertRef = false
	n.mu.Unlock()

	//r.decRef()  // template plan
	n.removeEndpointLocked(r)
	return nil
}

func (n *NIC) ReverseDeliverNetworkPacket(linkEP LinkEndpoint, protocol global.NetworkProtocolNumber, vv *buffer.VectorisedView) {
	netProto, ok := n.stack.networkProtocols[protocol]
	if !ok {
		atomic.AddUint64(&n.stack.stats.UnknownProtocolRcvdPackets, 1)
		return
	}

	if len(vv.First()) < netProto.MinimumPacketSize() {
		atomic.AddUint64(&n.stack.stats.MalformedRcvdPackets, 1)
		return
	}

	src, dst := netProto.ParseAddresses(vv.First())
	id := NetworkEndpointID{dst}

	n.mu.RLock()
	ref := n.endpoints[id]
	if ref != nil && !ref.tryIncRef() {
		ref = nil
	}
	promiscuous := n.promiscuous
	subnets := n.subnets
	n.mu.RUnlock()

	if ref == nil {
		// Check if the packet is for a subnet this NIC cares about.
		if !promiscuous {
			for _, sn := range subnets {
				if sn.Contains(dst) {
					promiscuous = true
					break
				}
			}
		}
		if promiscuous {
			// Try again with the lock in exclusive mode. If we still can't
			// get the endpoint, create a new "temporary" one. It will only
			// exist while there's a route through it.
			n.mu.Lock()
			ref = n.endpoints[id]
			if ref == nil || !ref.tryIncRef() {
				ref, _ = n.addAddressLocked(protocol, dst, true)
				if ref != nil {
					ref.holdsInsertRef = false
				}
			}
			n.mu.Unlock()
		}
	}

	if ref == nil {
		atomic.AddUint64(&n.stack.stats.UnknownNetworkEndpointRcvdPackets, 1)
		return
	}

	log.Infof("[=>ReverseDeliverNetworkPacket] %v ID:%v", vv, id)
	//netProto tcpip.NetworkProtocolNumber, localAddr, remoteAddr tcpip.Address, ref *referencedNetworkEndpoint
	r := makeRoute(protocol, dst, src, ref)
	ref.ep.ReverseHandlePacket(&r, vv)
	ref.decRef()
}

func (n *NIC) ReverseDeliverTransportPacket(r *Route, protocol global.TransportProtocolNumber, hdr *buffer.Prependable, vv *buffer.VectorisedView) {
	state, ok := n.stack.transportProtocols[protocol]
	if !ok {
		atomic.AddUint64(&n.stack.stats.UnknownProtocolRcvdPackets, 1)
		return
	}

	transProto := state.proto
	// LocalPort uint16, LocalAddress tcpip.Address, RemotePort uint16, RemoteAddress tcpip.Address
	id := TransportEndpointID{uint16(0), r.LocalAddress, uint16(0), r.RemoteAddress}
	if n.demux.reverseDeliverPacket(r, protocol, hdr, vv, id) {
		return
	}
	if n.stack.demux.reverseDeliverPacket(r, protocol, hdr, vv, id) {
		return
	}

	// Try to deliver to per-stack default handler.
	if state.defaultHandler != nil {
		if state.defaultHandler(r, id, vv) {
			return
		}
	}
	transProto.HandleUnknownDestinationPacket(n.stack, r, id, vv)
}

// ID returns the identifier of n.
func (n *NIC) ID() global.NICID {
	return n.id
}

type referencedNetworkEndpoint struct {
	ilist.Entry
	refs     int32
	ep       NetworkEndpoint
	nic      *NIC
	protocol global.NetworkProtocolNumber

	// holdsInsertRef is protected by the NIC's mutex. It indicates whether
	// the reference count is biased by 1 due to the insertion of the
	// endpoint. It is reset to false when RemoveAddress is called on the
	// NIC.
	holdsInsertRef bool
}

func newReferencedNetworkEndpoint(ep NetworkEndpoint, protocol global.NetworkProtocolNumber, nic *NIC) *referencedNetworkEndpoint {
	return &referencedNetworkEndpoint{
		refs:           1,
		ep:             ep,
		nic:            nic,
		protocol:       protocol,
		holdsInsertRef: true,
	}
}

// decRef decrements the ref count and cleans up the endpoint once it reaches
// zero.
func (r *referencedNetworkEndpoint) decRef() {
	if atomic.AddInt32(&r.refs, -1) == 0 {
		r.nic.removeEndpoint(r)
	}
}

// incRef increments the ref count. It must only be called when the caller is
// known to be holding a reference to the endpoint, otherwise tryIncRef should
// be used.
func (r *referencedNetworkEndpoint) incRef() {
	atomic.AddInt32(&r.refs, 1)
}

// tryIncRef attempts to increment the ref count from n to n+1, but only if n is
// not zero. That is, it will increment the count if the endpoint is still
// alive, and do nothing if it has already been clean up.
func (r *referencedNetworkEndpoint) tryIncRef() bool {
	for {
		v := atomic.LoadInt32(&r.refs)
		if v == 0 {
			return false
		}

		if atomic.CompareAndSwapInt32(&r.refs, v, v+1) {
			return true
		}
	}
}
