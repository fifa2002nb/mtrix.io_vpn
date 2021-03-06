// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mmm

import (
    "net"
	"errors"
	"io"
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/stack"
	"mtrix.io_vpn/waiter"
	"sync"
)

type mmmPacket struct {
	mmmPacketEntry
	senderAddress global.FullAddress
	data          buffer.View
}

type endpointState int

const (
	stateInitial endpointState = iota
	stateBound
	stateConnected
	stateClosed
)

var errRetryPrepare = errors.New("prepare operation must be retried")

// 每一个传输层的endpoint代表一个端口的数据管道，与真实的udpConn绑定
type endpoint struct {
	// The following fields are initialized at creation time and do not
	// change throughout the lifetime of the endpoint.
	stack       *stack.Stack
	netProto    global.NetworkProtocolNumber
	waiterQueue *waiter.Queue

	// The following fields are used to manage the receive queue, and are
	// protected by rcvMu.
	rcvMu         sync.Mutex
	rcvReady      bool
	rcvList       mmmPacketList
	rcvBufSizeMax int
	rcvBufSize    int
	rcvClosed     bool

	// The following fields are protected by the mu mutex.
	mu         sync.RWMutex
	sndBufSize int
	id         stack.TransportEndpointID
	state      endpointState
	bindNICID  global.NICID
	bindAddr   global.Address
	regNICID   global.NICID
	route      stack.Route
	dstPort    uint16

	// effectiveNetProtos contains the network protocols actually in use. In
	// most cases it will only contain "netProto", but in cases like IPv6
	// endpoints with v6only set to false, this could include multiple
	// protocols (e.g., IPv6 and IPv4) or a single different protocol (e.g.,
	// IPv4 when IPv6 endpoint is bound or connected to an IPv4 mapped
	// address).
	effectiveNetProtos []global.NetworkProtocolNumber

    addr *net.UDPAddr
}

func newEndpoint(stack *stack.Stack, netProto global.NetworkProtocolNumber, waiterQueue *waiter.Queue) *endpoint {
	// TODO: Use the send buffer size initialized here.
	return &endpoint{
		stack:         stack,
		netProto:      netProto,
		waiterQueue:   waiterQueue,
		rcvBufSizeMax: 32 * 1024,
		sndBufSize:    32 * 1024,
	}
}

// NewConnectedEndpoint creates a new endpoint in the connected state using the
// provided route.
func NewConnectedEndpoint(stack *stack.Stack, r *stack.Route, id stack.TransportEndpointID, waiterQueue *waiter.Queue) (global.Endpoint, error) {
	ep := newEndpoint(stack, r.NetProto, waiterQueue)

	// Register new endpoint so that packets are routed to it.
	if err := stack.RegisterTransportEndpoint(r.NICID(), []global.NetworkProtocolNumber{r.NetProto}, ProtocolNumber, id, ep); err != nil {
		ep.Close()
		return nil, err
	}

	ep.id = id
	ep.route = r.Clone()
	ep.dstPort = id.RemotePort
	ep.regNICID = r.NICID()

	ep.state = stateConnected

	return ep, nil
}

func (e *endpoint) GetNetAddr() *net.UDPAddr {
	return e.addr
}

// Close puts the endpoint in a closed state and frees all resources
// associated with it.
func (e *endpoint) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch e.state {
	case stateBound, stateConnected:
		e.stack.UnregisterTransportEndpoint(e.regNICID, e.effectiveNetProtos, ProtocolNumber, e.id)
	}

	// Close the receive list and drain it.
	e.rcvMu.Lock()
	e.rcvClosed = true
	e.rcvBufSize = 0
	for !e.rcvList.Empty() {
		p := e.rcvList.Front()
		e.rcvList.Remove(p)
	}
	e.rcvMu.Unlock()

	e.route.Release()

	// Update the state.
	e.state = stateClosed
}

// Read reads data from the endpoint. This method does not block if
// there is no data pending.
func (e *endpoint) Read(addr *global.FullAddress) (buffer.View, error) {
	e.rcvMu.Lock()

	if e.rcvList.Empty() {
		err := global.ErrWouldBlock
		if e.rcvClosed {
			err = global.ErrClosedForReceive
		}
		e.rcvMu.Unlock()
		return buffer.View{}, err
	}

	p := e.rcvList.Front()
	e.rcvList.Remove(p)
	e.rcvBufSize -= len(p.data)

	e.rcvMu.Unlock()

	if addr != nil {
		*addr = p.senderAddress
	}

	return p.data, nil
}

// RecvMsg implements global.RecvMsg.
func (e *endpoint) RecvMsg(addr *global.FullAddress) (buffer.View, global.ControlMessages, error) {
	v, err := e.Read(addr)
	return v, nil, err
}

// prepareForWrite prepares the endpoint for sending data. In particular, it
// binds it if it's still in the initial state. To do so, it must first
// reacquire the mutex in exclusive mode.
//
// Returns errRetryPrepare if preparation should be retried.
func (e *endpoint) prepareForWrite(to *global.FullAddress) error {
	switch e.state {
	case stateInitial:
	case stateConnected:
		return nil

	case stateBound:
		if to == nil {
			return global.ErrDestinationRequired
		}
		return nil
	default:
		return global.ErrInvalidEndpointState
	}

	e.mu.RUnlock()
	defer e.mu.RLock()

	e.mu.Lock()
	defer e.mu.Unlock()

	// The state changed when we released the shared locked and re-acquired
	// it in exclusive mode. Try again.
	if e.state != stateInitial {
		return errRetryPrepare
	}

	// The state is still 'initial', so try to bind the endpoint.
	if err := e.bindLocked(global.FullAddress{}, nil); err != nil {
		return err
	}

	return errRetryPrepare
}

// Write writes data to the endpoint's peer. This method does not block
// if the data cannot be written.
func (e *endpoint) Write(v buffer.View, to *global.FullAddress) (uintptr, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Prepare for write.
	for {
		err := e.prepareForWrite(to)
		if err == nil {
			break
		}

		if err != errRetryPrepare {
			return 0, err
		}
	}

	route := &e.route
	dstPort := e.dstPort
	if to != nil {
		// Reject destination address if it goes through a different
		// NIC than the endpoint was bound to.
		nicid := to.NIC
		if e.bindNICID != 0 {
			if nicid != 0 && nicid != e.bindNICID {
				return 0, global.ErrNoRoute
			}

			nicid = e.bindNICID
		}

		toCopy := *to
		to = &toCopy
		netProto := e.netProto

		// Find the enpoint.
		r, err := e.stack.FindRoute(nicid, e.bindAddr, to.Addr, netProto)
		if err != nil {
			return 0, err
		}
		defer r.Release()

		route = &r
		dstPort = to.Port
	}
	sendMMM(route, v, e.id.LocalPort, dstPort)
	return uintptr(len(v)), nil
}

func (e *endpoint) WriteToNet(v buffer.View, to *global.FullAddress) (uintptr, error) {
   return uintptr(0), nil 
}

func (e *endpoint) WriteToInterface() error {
    return nil
}

// SendMsg implements global.SendMsg.
func (e *endpoint) SendMsg(v buffer.View, c global.ControlMessages, to *global.FullAddress) (uintptr, error) {
	// Reject control messages.
	if c != nil {
		// global.ErrInvalidEndpointState turns into syscall.EINVAL.
		return 0, global.ErrInvalidEndpointState
	}
	return e.Write(v, to)
}

// Peek only returns data from a single datagram, so do nothing here.
func (e *endpoint) Peek(io.Writer) (uintptr, error) {
	return 0, nil
}

// SetSockOpt sets a socket option. Currently not supported.
func (e *endpoint) SetSockOpt(opt interface{}) error {
	return nil
}

// GetSockOpt implements global.Endpoint.GetSockOpt.
func (e *endpoint) GetSockOpt(opt interface{}) error {
	switch o := opt.(type) {
	case global.ErrorOption:
		return nil

	case *global.SendBufferSizeOption:
		e.mu.Lock()
		*o = global.SendBufferSizeOption(e.sndBufSize)
		e.mu.Unlock()
		return nil

	case *global.ReceiveBufferSizeOption:
		e.rcvMu.Lock()
		*o = global.ReceiveBufferSizeOption(e.rcvBufSizeMax)
		e.rcvMu.Unlock()
		return nil
	}
	return global.ErrInvalidEndpointState
}

// sendMM sends a MM segment via the provided network endpoint and under the
// provided identity.
func sendMMM(r *stack.Route, data buffer.View, localPort, remotePort uint16) error {
	return r.WritePacket(data, ProtocolNumber)
}

// Connect connects the endpoint to its peer. Specifying a NIC is optional.
func (e *endpoint) Connect(addr global.FullAddress) error {
	if addr.Port == 0 {
		// We don't support connecting to port zero.
		return global.ErrInvalidEndpointState
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	nicid := addr.NIC
	localPort := uint16(0)
	switch e.state {
	case stateInitial:
	case stateBound, stateConnected:
		localPort = e.id.LocalPort
		if e.bindNICID == 0 {
			break
		}

		if nicid != 0 && nicid != e.bindNICID {
			return global.ErrInvalidEndpointState
		}

		nicid = e.bindNICID
	default:
		return global.ErrInvalidEndpointState
	}

	netProto := e.netProto

	// Find a route to the desired destination.
	r, err := e.stack.FindRoute(nicid, e.bindAddr, addr.Addr, netProto)
	if err != nil {
		return err
	}
	defer r.Release()

	id := stack.TransportEndpointID{
		LocalAddress:  r.LocalAddress,
		LocalPort:     localPort,
		RemotePort:    addr.Port,
		RemoteAddress: addr.Addr,
	}

	netProtos := []global.NetworkProtocolNumber{netProto}

	id, err = e.registerWithStack(nicid, netProtos, id)
	if err != nil {
		return err
	}

	// Remove the old registration.
	if e.id.LocalPort != 0 {
		e.stack.UnregisterTransportEndpoint(e.regNICID, e.effectiveNetProtos, ProtocolNumber, e.id)
	}

	e.id = id
	e.route = r.Clone()
	e.dstPort = addr.Port
	e.regNICID = nicid
	e.effectiveNetProtos = netProtos

	e.state = stateConnected

	e.rcvMu.Lock()
	e.rcvReady = true
	e.rcvMu.Unlock()

	return nil
}

// ConnectEndpoint is not supported.
func (*endpoint) ConnectEndpoint(global.Endpoint) error {
	return global.ErrInvalidEndpointState
}

// Shutdown closes the read and/or write end of the endpoint connection
// to its peer.
func (e *endpoint) Shutdown(flags global.ShutdownFlags) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.state != stateConnected {
		return global.ErrNotConnected
	}

	if flags&global.ShutdownRead != 0 {
		e.rcvMu.Lock()
		wasClosed := e.rcvClosed
		e.rcvClosed = true
		e.rcvMu.Unlock()

		if !wasClosed {
			e.waiterQueue.Notify(waiter.EventIn)
		}
	}

	return nil
}

func (*endpoint) Listen(int) error {
	return global.ErrNotSupported
}

// Accept is not supported by UDP, it just fails.
func (*endpoint) Accept() (global.Endpoint, *waiter.Queue, error) {
	return nil, nil, global.ErrNotSupported
}

func (e *endpoint) registerWithStack(nicid global.NICID, netProtos []global.NetworkProtocolNumber, id stack.TransportEndpointID) (stack.TransportEndpointID, error) {
	// localPort:0
	err := e.stack.RegisterTransportEndpoint(nicid, netProtos, ProtocolNumber, id, e)
	return id, err
}

func (e *endpoint) bindLocked(addr global.FullAddress, commit func() error) error {
	// Don't allow binding once endpoint is not in the initial state
	// anymore.
	if e.state != stateInitial {
		return global.ErrInvalidEndpointState
	}

	netProto := e.netProto
	netProtos := []global.NetworkProtocolNumber{netProto}

	if len(addr.Addr) != 0 {
		// A local address was specified, verify that it's valid.
		if e.stack.CheckLocalAddress(addr.NIC, addr.Addr) == 0 {
			return global.ErrBadLocalAddress
		}
	}

	id := stack.TransportEndpointID{
		LocalPort:    addr.Port,
		LocalAddress: addr.Addr,
	}
	var err error
	id, err = e.registerWithStack(addr.NIC, netProtos, id)
	if err != nil {
		return err
	}
	if commit != nil {
		if err := commit(); err != nil {
			// Unregister, the commit failed.
			e.stack.UnregisterTransportEndpoint(addr.NIC, netProtos, ProtocolNumber, id)
			return err
		}
	}

	e.id = id
	e.regNICID = addr.NIC
	e.effectiveNetProtos = netProtos

	// Mark endpoint as bound.
	e.state = stateBound

	e.rcvMu.Lock()
	e.rcvReady = true
	e.rcvMu.Unlock()

	return nil
}

// Bind binds the endpoint to a specific local address and port.
// Specifying a NIC is optional.
func (e *endpoint) Bind(addr global.FullAddress, commit func() error) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	err := e.bindLocked(addr, commit)
	if err != nil {
		return err
	}

	e.bindNICID = addr.NIC
	e.bindAddr = addr.Addr

	return nil
}

// GetLocalAddress returns the address to which the endpoint is bound.
func (e *endpoint) GetLocalAddress() (global.FullAddress, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return global.FullAddress{
		NIC:  e.regNICID,
		Addr: e.id.LocalAddress,
		Port: e.id.LocalPort,
	}, nil
}

// GetRemoteAddress returns the address to which the endpoint is connected.
func (e *endpoint) GetRemoteAddress() (global.FullAddress, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.state != stateConnected {
		return global.FullAddress{}, global.ErrInvalidEndpointState
	}

	return global.FullAddress{
		NIC:  e.regNICID,
		Addr: e.id.RemoteAddress,
		Port: e.id.RemotePort,
	}, nil
}

// Readiness returns the current readiness of the endpoint. For example, if
// waiter.EventIn is set, the endpoint is immediately readable.
func (e *endpoint) Readiness(mask waiter.EventMask) waiter.EventMask {
	// The endpoint is always writable.
	result := waiter.EventOut & mask

	// Determine if the endpoint is readable if requested.
	if (mask & waiter.EventIn) != 0 {
		e.rcvMu.Lock()
		if !e.rcvList.Empty() || e.rcvClosed {
			result |= waiter.EventIn
		}
		e.rcvMu.Unlock()
	}

	return result
}

func (e *endpoint) HandlePacket(v buffer.View, udpAddr *net.UDPAddr) {}

func (e *endpoint) ReverseHandlePacket(r *stack.Route, id stack.TransportEndpointID, hdr *buffer.Prependable, vv *buffer.VectorisedView) {
	if nil == hdr {
		return
	}

	e.rcvMu.Lock()

	// Drop the packet if our buffer is currently full.
	if !e.rcvReady || e.rcvClosed || e.rcvBufSize >= e.rcvBufSizeMax {
		e.rcvMu.Unlock()
		return
	}
	wasEmpty := e.rcvBufSize == 0

	// Push new packet into receive list and increment the buffer size.
	pkt := &mmmPacket{
		senderAddress: global.FullAddress{
			NIC:  r.NICID(),
			Addr: id.RemoteAddress, // 因为demuxer中命中的是最后一种情况，因此应该是""
			Port: id.RemotePort,    // 在mmm的protocol中的端口解析方法中写死888
		},
	}
	pkt.data = vv.ToViewWithExtraView(hdr.View()) // 头部和数据拼接
	e.rcvList.PushBack(pkt)                       // push当前端口数据管道中
	e.rcvBufSize += len(pkt.data)

	e.rcvMu.Unlock()
	// Notify any waiters that there's data to be read now.
	if wasEmpty {
		e.waiterQueue.Notify(waiter.EventIn)
	}
}
