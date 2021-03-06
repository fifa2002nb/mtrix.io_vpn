// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack

import (
	log "github.com/Sirupsen/logrus"
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"sync"
)

type protocolIDs struct {
	network   global.NetworkProtocolNumber
	transport global.TransportProtocolNumber
}

// transportEndpoints manages all endpoints of a given protocol. It has its own
// mutex so as to reduce interference between protocols.
type transportEndpoints struct {
	mu        sync.RWMutex
	endpoints map[TransportEndpointID]TransportEndpoint
}

// transportDemuxer demultiplexes packets targeted at a transport endpoint
// (i.e., after they've been parsed by the network layer). It does two levels
// of demultiplexing: first based on the network and transport protocols, then
// based on endpoints IDs.
type transportDemuxer struct {
	protocol map[protocolIDs]*transportEndpoints
}

func newTransportDemuxer(stack *Stack) *transportDemuxer {
	d := &transportDemuxer{protocol: make(map[protocolIDs]*transportEndpoints)}

	// Add each network and and transport pair to the demuxer.
	for netProto := range stack.networkProtocols {
		for proto := range stack.transportProtocols {
			d.protocol[protocolIDs{netProto, proto}] = &transportEndpoints{endpoints: make(map[TransportEndpointID]TransportEndpoint)}
		}
	}

	return d
}

// registerEndpoint registers the given endpoint with the dispatcher such that
// packets that match the endpoint ID are delivered to it.
func (d *transportDemuxer) registerEndpoint(netProtos []global.NetworkProtocolNumber, protocol global.TransportProtocolNumber, id TransportEndpointID, ep TransportEndpoint) error {
	for i, n := range netProtos {
		if err := d.singleRegisterEndpoint(n, protocol, id, ep); err != nil {
			d.unregisterEndpoint(netProtos[:i], protocol, id)
			return err
		}
	}

	return nil
}

func (d *transportDemuxer) singleRegisterEndpoint(netProto global.NetworkProtocolNumber, protocol global.TransportProtocolNumber, id TransportEndpointID, ep TransportEndpoint) error {
	eps, ok := d.protocol[protocolIDs{netProto, protocol}]
	if !ok {
		return nil
	}

	eps.mu.Lock()
	defer eps.mu.Unlock()

	if _, ok := eps.endpoints[id]; ok {
		log.Errorf("[<=singleRegisterEndpoint] %v err:%v", id, global.ErrDuplicateAddress)
		//return global.ErrDuplicateAddress
	}

	eps.endpoints[id] = ep

	return nil
}

// unregisterEndpoint unregisters the endpoint with the given id such that it
// won't receive any more packets.
func (d *transportDemuxer) unregisterEndpoint(netProtos []global.NetworkProtocolNumber, protocol global.TransportProtocolNumber, id TransportEndpointID) {
	for _, n := range netProtos {
		if eps, ok := d.protocol[protocolIDs{n, protocol}]; ok {
			eps.mu.Lock()
			delete(eps.endpoints, id)
			eps.mu.Unlock()
		}
	}
}

func (d *transportDemuxer) reverseDeliverPacket(r *Route, protocol global.TransportProtocolNumber, hdr *buffer.Prependable, vv *buffer.VectorisedView, id TransportEndpointID) bool {
	eps, ok := d.protocol[protocolIDs{r.NetProto, protocol}]
	if !ok {
		return false
	}

	eps.mu.RLock()
	defer eps.mu.RUnlock()
	// Try to find a match with the id as provided.
	if ep := eps.endpoints[id]; ep != nil {
		log.Debugf("[=>reverseDeliverPacket] hdrLen:%v dataLen:%v %v", len(hdr.View()), vv.Size(), id)
		ep.ReverseHandlePacket(r, id, hdr, vv)
		return true
	}

	// Try to find a match with the id minus the local address.
	nid := id

	nid.LocalAddress = nid.RemoteAddress
	if ep := eps.endpoints[nid]; ep != nil {
		log.Debugf("[=>reverseDeliverPacket] hdrLen:%v dataLen:%v %v", len(hdr.View()), vv.Size(), nid)
		ep.ReverseHandlePacket(r, id, hdr, vv)
		return true
	}

	nid = id
	nid.RemoteAddress = nid.LocalAddress
	if ep := eps.endpoints[nid]; ep != nil {
		log.Debugf("[=>reverseDeliverPacket] hdrLen:%v len:%v %v", len(hdr.View()), vv.Size(), nid)
		ep.ReverseHandlePacket(r, id, hdr, vv)
		return true
	}

	log.Errorf("[=>reverseDeliverPacket]  %v didn't match any endpoint.", id)
	return false
}
