// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tcp

import (
	log "github.com/Sirupsen/logrus"
	"io"
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/header"
	"mtrix.io_vpn/seqnum"
	"mtrix.io_vpn/stack"
	"mtrix.io_vpn/waiter"
	"net"
	"sync"
	"sync/atomic"
)

type endpointState int

const (
	stateInitial endpointState = iota
	stateBound
	stateListen
	stateConnecting
	stateConnected
	stateClosed
	stateError
)

// Reasons for notifying the protocol goroutine.
const (
	notifyNonZeroReceiveWindow = 1 << iota
	notifyReceiveWindowChanged
	notifyClose
)

// endpoint represents a TCP endpoint. This struct serves as the interface
// between users of the endpoint and the protocol implementation; it is legal to
// have concurrent goroutines make calls into the endpoint, they are properly
// synchronized. The protocol implementation, however, runs in a single
// goroutine.
type endpoint struct {
	// The following fields are initialized at creation time and do not
	// change throughout the lifetime of the endpoint.
	stack       *stack.Stack
	netProto    global.NetworkProtocolNumber
	waiterQueue *waiter.Queue

	// lastError represents the last error that the endpoint reported;
	// access to it is protected by the following mutex.
	lastErrorMu sync.Mutex
	lastError   error

	// The following fields are used to manage the receive queue. The
	// protocol goroutine adds ready-for-delivery segments to rcvList,
	// which are returned by Read() calls to users.
	//
	// Once the peer has closed the its send side, rcvClosed is set to true
	// to indicate to users that no more data is coming.
	rcvListMu  sync.Mutex
	rcvList    segmentList
	rcvClosed  bool
	rcvBufSize int
	rcvBufUsed int

	// The following fields are protected by the mutex.
	mu             sync.RWMutex
	id             stack.TransportEndpointID
	state          endpointState
	isPortReserved bool
	isRegistered   bool
	boundNICID     global.NICID
	route          stack.Route
	v6only         bool

	// effectiveNetProtos contains the network protocols actually in use. In
	// most cases it will only contain "netProto", but in cases like IPv6
	// endpoints with v6only set to false, this could include multiple
	// protocols (e.g., IPv6 and IPv4) or a single different protocol (e.g.,
	// IPv4 when IPv6 endpoint is bound or connected to an IPv4 mapped
	// address).
	effectiveNetProtos []global.NetworkProtocolNumber

	// hardError is meaningful only when state is stateError, it stores the
	// error to be returned when read/write syscalls are called and the
	// endpoint is in this state.
	hardError error

	// workerRunning specifies if a worker goroutine is running.
	workerRunning bool

	// workerCleanup specifies if the worker goroutine must perform cleanup
	// before exitting. This can only be set to true when workerRunning is
	// also true, and they're both protected by the mutex.
	workerCleanup bool

	// The options below aren't implemented, but we remember the user
	// settings because applications expect to be able to set/query these
	// options.
	noDelay   bool
	reuseAddr bool

	// segmentChan is used to hand received segments to the protocol
	// goroutine. Segments are queued in the channel as long as it is not
	// full, and dropped when it is.
	segmentChan chan *segment

	// The following fields are used to manage the send buffer. When
	// segments are ready to be sent, they are added to sndQueue and the
	// protocol goroutine is signaled by a write to sndChan.
	//
	// When the send side is closed, the channel is closed (so that the
	// protocol goroutine is aware), and sndBufSize is set to -1.
	sndBufMu      sync.Mutex
	sndBufSize    int
	sndBufUsed    int
	sndBufInQueue seqnum.Size
	sndQueue      segmentList
	sndChan       chan struct{}

	// notifyChan is used to indicate to the protocol goroutine that it
	// needs to wake up and check for notifications.
	notifyChan chan struct{}

	// notifyFlags is a bitmask of flags used to indicate to the protocol
	// goroutine what it was notified; this is only accessed atomically.
	notifyFlags uint32

	// acceptedChan is used by a listening endpoint protocol goroutine to
	// send newly accepted connections to the endpoint so that they can be
	// read by Accept() calls.
	acceptedChan chan *endpoint

	// The following are only used from the protocol goroutine, and
	// therefore don't need locks to protect them.
	rcv *receiver
	snd *sender

	clientIP global.Address
	addrsArr []*net.UDPAddr
	addrsMap map[[6]byte]*net.UDPAddr // hash(ip+port) -> *net.UDPAddr
	addrMu   sync.Mutex
	addrIdx  int

	subnetInited bool
	subnetIP     global.Address
	subnetMask   uint8
}

func newEndpoint(stack *stack.Stack, netProto global.NetworkProtocolNumber, waiterQueue *waiter.Queue) *endpoint {
	return &endpoint{
		stack:        stack,
		netProto:     netProto,
		waiterQueue:  waiterQueue,
		v6only:       true,
		segmentChan:  make(chan *segment, 10),
		rcvBufSize:   208 * 1024,
		sndBufSize:   208 * 1024,
		sndChan:      make(chan struct{}, 1),
		notifyChan:   make(chan struct{}, 1),
		noDelay:      true,
		reuseAddr:    true,
		clientIP:     global.Address("\x00\x00\x00\x00"),
		addrsArr:     make([]*net.UDPAddr, 0),
		addrsMap:     make(map[[6]byte]*net.UDPAddr),
		addrIdx:      0,
		subnetInited: false,
		subnetIP:     global.Address("\x00\x00\x00\x00"),
		subnetMask:   0,
	}
}

func (e *endpoint) InitSubnet(ip global.Address, netmask uint8) error {
	if !e.subnetInited {
		e.subnetIP = ip
		e.subnetMask = netmask

		// register peer's addr
		//peer := []byte(ip)
		//peer[3]++
		/*if err := e.stack.AddAddress(e.boundNICID, e.netProto, e.subnetIP); nil != err {
			return err
		}*/
		// RegisterTransportEndpoint by peer's addr
		if err := e.BindToStack(e.subnetIP); nil != err {
			return err
		}
		e.subnetInited = true
	}
	return nil
}

func (e *endpoint) InitedSubnet() bool {
	return e.subnetInited
}

func (e *endpoint) GetSubnetIP() global.Address {
	return e.subnetIP
}

func (e *endpoint) GetSubnetMask() uint8 {
	return e.subnetMask
}

func (e *endpoint) GetClientIP() global.Address {
	return e.clientIP
}

func (e *endpoint) PushNetAddr(addr *net.UDPAddr) {
	if nil == addr {
		return
	}
	hash := e.stack.NetAddrHash(addr)
	if _, ok := e.addrsMap[hash]; !ok {
		e.addrMu.Lock()
		e.addrsArr = append(e.addrsArr, addr)
		e.addrsMap[hash] = addr
		e.addrMu.Unlock()
	}
}

func (e *endpoint) PopNetAddr() *net.UDPAddr {
	size := len(e.addrsArr)
	if 0 == size {
		return nil
	}
	if e.addrIdx >= size {
		e.addrIdx = 0
	}
	addr := e.addrsArr[e.addrIdx]
	e.addrIdx++
	return addr
}

// Readiness returns the current readiness of the endpoint. For example, if
// waiter.EventIn is set, the endpoint is immediately readable.
func (e *endpoint) Readiness(mask waiter.EventMask) waiter.EventMask {
	result := waiter.EventMask(0)

	e.mu.RLock()
	defer e.mu.RUnlock()

	switch e.state {
	case stateInitial, stateBound, stateConnecting:
		// Ready for nothing.

	case stateClosed, stateError:
		// Ready for anything.
		result = mask

	case stateListen:
		// Check if there's anything in the accepted channel.
		if (mask & waiter.EventIn) != 0 {
			if len(e.acceptedChan) > 0 {
				result |= waiter.EventIn
			}
		}

	case stateConnected:
		// Determine if the endpoint is writable if requested.
		if (mask & waiter.EventOut) != 0 {
			e.sndBufMu.Lock()
			if e.sndBufSize < 0 || e.sndBufUsed <= e.sndBufSize {
				result |= waiter.EventOut
			}
			e.sndBufMu.Unlock()
		}

		// Determine if the endpoint is readable if requested.
		if (mask & waiter.EventIn) != 0 {
			e.rcvListMu.Lock()
			if e.rcvBufUsed > 0 || e.rcvClosed {
				result |= waiter.EventIn
			}
			e.rcvListMu.Unlock()
		}
	}

	return result
}

func (e *endpoint) fetchNotifications() uint32 {
	return atomic.SwapUint32(&e.notifyFlags, 0)
}

func (e *endpoint) notifyProtocolGoroutine(n uint32) {
	for {
		v := atomic.LoadUint32(&e.notifyFlags)
		if v&n == n {
			// The flags are already set.
			return
		}

		if atomic.CompareAndSwapUint32(&e.notifyFlags, v, v|n) {
			if v == 0 {
				// We are causing a transition from no flags to
				// at least one flag set, so we must cause the
				// protocol goroutine to wake up.
				select {
				case e.notifyChan <- struct{}{}:
				default:
				}
			}
			return
		}
	}
}

// Close puts the endpoint in a closed state and frees all resources associated
// with it. It must be called only once and with no other concurrent calls to
// the endpoint.
func (e *endpoint) Close() {
	// Issue a shutdown so that the peer knows we won't send any more data
	// if we're connected, or stop accepting if we're listening.
	e.Shutdown(global.ShutdownWrite | global.ShutdownRead)

	// While we hold the lock, determine if the cleanup should happen
	// inline or if we should tell the worker (if any) to do the cleanup.
	e.mu.Lock()
	worker := e.workerRunning
	if worker {
		e.workerCleanup = true
	}
	e.mu.Unlock()

	// Now that we don't hold the lock anymore, either perform the local
	// cleanup or kick the worker to make sure it knows it needs to cleanup.
	if !worker {
		e.cleanup()
	} else {
		e.notifyProtocolGoroutine(notifyClose)
	}
}

// cleanup frees all resources associated with the endpoint. It is called after
// Close() is called and the worker goroutine (if any) is done with its work.
func (e *endpoint) cleanup() {
	// Close all endpoints that might have been accepted by TCP but not by
	// the client.
	if e.acceptedChan != nil {
		close(e.acceptedChan)
		for n := range e.acceptedChan {
			n.resetConnection(global.ErrConnectionAborted)
			n.Close()
		}
	}

	if e.isPortReserved {
		e.stack.ReleasePort(e.effectiveNetProtos, ProtocolNumber, e.id.LocalAddress, e.id.LocalPort)
	}

	if e.isRegistered {
		e.stack.UnregisterTransportEndpoint(e.boundNICID, e.effectiveNetProtos, ProtocolNumber, e.id)
	}

	e.route.Release()
	// unregister connectedTransportEndpoint hashtable's item
	e.stack.UnregisterConnectedTransportEndpoint(e)
	// release endpoint's subnetIP
	e.stack.ReleaseIP(e.subnetIP)

	// remove peer's addr
	//peer := []byte(e.subnetIP)
	//peer[3]++
	// delete subnetIP addr
	e.stack.RemoveAddress(e.boundNICID, e.subnetIP)
	// delete realIP addr
	e.stack.RemoveAddress(e.boundNICID, e.clientIP)

	// unbind peer from stack
	e.UnBindFStack(e.subnetIP)
}

// Read reads data from the endpoint.
func (e *endpoint) Read(*global.FullAddress) (buffer.View, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// The endpoint cannot be read from if it's not connected.
	if e.state != stateConnected {
		switch e.state {
		case stateClosed:
			return buffer.View{}, global.ErrClosedForReceive
		case stateError:
			return buffer.View{}, e.hardError
		default:
			return buffer.View{}, global.ErrInvalidEndpointState
		}
	}

	e.rcvListMu.Lock()
	defer e.rcvListMu.Unlock()

	if e.rcvBufUsed == 0 {
		if e.rcvClosed {
			return buffer.View{}, global.ErrClosedForReceive
		}
		return buffer.View{}, global.ErrWouldBlock
	}

	s := e.rcvList.Front()
	views := s.data.Views()
	v := views[s.viewToDeliver]
	s.viewToDeliver++

	if s.viewToDeliver >= len(views) {
		e.rcvList.Remove(s)
		s.decRef()
	}

	wasZero := e.rcvBufUsed >= e.rcvBufSize
	e.rcvBufUsed -= len(v)
	if wasZero && e.rcvBufUsed < e.rcvBufSize {
		e.notifyProtocolGoroutine(notifyNonZeroReceiveWindow)
	}

	return v, nil
}

// RecvMsg implements global.RecvMsg.
func (e *endpoint) RecvMsg(addr *global.FullAddress) (buffer.View, global.ControlMessages, error) {
	v, err := e.Read(addr)
	return v, nil, err
}

func (e *endpoint) ReverseHandlePacket(r *stack.Route, id stack.TransportEndpointID, hdr *buffer.Prependable, vv *buffer.VectorisedView) {
	if nil == hdr {
		return
	}
	v := vv.ToViewWithExtraView(hdr.View())
	_, err := e.WriteToNet(v, nil)
	if nil != err {
		log.Errorf("[=>ReverseHandlePacket]%v", err)
	}
	log.Infof("[=>ReverseHandlePacket] hdrLen:%v dataLen:%v", len(hdr.View()), vv.Size())
}

func (e *endpoint) Write(v buffer.View, to *global.FullAddress) (uintptr, error) {
	return uintptr(0), nil
}

// Write writes data to the endpoint's peer.
func (e *endpoint) WriteToNet(v buffer.View, to *global.FullAddress) (uintptr, error) {
	if to != nil {
		return 0, global.ErrAlreadyConnected
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	// The endpoint cannot be written to if it's not connected.
	if e.state != stateConnected {
		switch e.state {
		case stateError:
			return 0, e.hardError
		default:
			return 0, global.ErrInvalidEndpointState
		}
	}

	var views [1]buffer.View
	vv := v.ToVectorisedView(views)
	s := newSegment(&e.route, e.id, &vv, nil)

	e.sndBufMu.Lock()

	// Check if the connection has already been closed for sends.
	if e.sndBufSize < 0 {
		e.sndBufMu.Unlock()
		s.decRef()
		return 0, global.ErrClosedForSend
	}

	// Check if we're already over the limit.
	if e.sndBufUsed > e.sndBufSize {
		e.sndBufMu.Unlock()
		s.decRef()
		return 0, global.ErrWouldBlock
	}

	// Add data to the send queue.
	e.sndBufUsed += len(v)
	e.sndBufInQueue += seqnum.Size(len(v))
	e.sndQueue.PushBack(s)

	e.sndBufMu.Unlock()

	// Wake up the protocol goroutine.
	select {
	case e.sndChan <- struct{}{}:
	default:
	}

	return uintptr(len(v)), nil
}

// SendMsg implements global.SendMsg.
func (e *endpoint) SendMsg(v buffer.View, c global.ControlMessages, to *global.FullAddress) (uintptr, error) {
	// Reject control messages.
	if c != nil {
		// global.ErrInvalidEndpointState turns into syscall.EINVAL.
		return 0, global.ErrInvalidEndpointState
	}
	return e.WriteToNet(v, to)
}

// Peek reads data without consuming it from the endpoint.
//
// This method does not block if there is no data pending.
func (e *endpoint) Peek(w io.Writer) (uintptr, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// The endpoint cannot be read from if it's not connected.
	if e.state != stateConnected {
		switch e.state {
		case stateClosed:
			return 0, global.ErrClosedForReceive
		case stateError:
			return 0, e.hardError
		default:
			return 0, global.ErrInvalidEndpointState
		}
	}

	e.rcvListMu.Lock()
	defer e.rcvListMu.Unlock()

	if e.rcvBufUsed == 0 {
		if e.rcvClosed {
			return 0, global.ErrClosedForReceive
		}
		return 0, global.ErrWouldBlock
	}

	var num uintptr

	for s := e.rcvList.Front(); s != nil; s = s.Next() {
		views := s.data.Views()
		for i := s.viewToDeliver; i < len(views); i++ {
			n, err := w.Write(views[i])
			num += uintptr(n)
			if err != nil {
				return num, err
			}
		}
	}
	return num, nil
}

// SetSockOpt sets a socket option. Currently not supported.
func (e *endpoint) SetSockOpt(opt interface{}) error {
	// TODO: Actually implement this.
	switch v := opt.(type) {
	case global.NoDelayOption:
		e.mu.Lock()
		e.noDelay = v != 0
		e.mu.Unlock()
		return nil

	case global.ReuseAddressOption:
		e.mu.Lock()
		e.reuseAddr = v != 0
		e.mu.Unlock()
		return nil

	case global.ReceiveBufferSizeOption:
		mask := uint32(notifyReceiveWindowChanged)

		e.rcvListMu.Lock()
		wasZero := e.rcvBufUsed >= e.rcvBufSize
		e.rcvBufSize = int(v)
		if wasZero && e.rcvBufUsed < e.rcvBufSize {
			mask |= notifyNonZeroReceiveWindow
		}
		e.rcvListMu.Unlock()

		e.notifyProtocolGoroutine(mask)
		return nil

	case global.V6OnlyOption:
		// We only recognize this option on v6 endpoints.
		if e.netProto != header.IPv6ProtocolNumber {
			return global.ErrInvalidEndpointState
		}

		e.mu.Lock()
		defer e.mu.Unlock()

		// We only allow this to be set when we're in the initial state.
		if e.state != stateInitial {
			return global.ErrInvalidEndpointState
		}

		e.v6only = v != 0
	}

	return nil
}

// readyReceiveSize returns the number of bytes ready to be received.
func (e *endpoint) readyReceiveSize() (int, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// The endpoint cannot be in listen state.
	if e.state == stateListen {
		return 0, global.ErrInvalidEndpointState
	}

	e.rcvListMu.Lock()
	defer e.rcvListMu.Unlock()

	return e.rcvBufUsed, nil
}

// GetSockOpt implements global.Endpoint.GetSockOpt.
func (e *endpoint) GetSockOpt(opt interface{}) error {
	switch o := opt.(type) {
	case global.ErrorOption:
		e.lastErrorMu.Lock()
		err := e.lastError
		e.lastError = nil
		e.lastErrorMu.Unlock()
		return err

	case *global.SendBufferSizeOption:
		e.sndBufMu.Lock()
		*o = global.SendBufferSizeOption(e.sndBufSize)
		e.sndBufMu.Unlock()
		return nil

	case *global.ReceiveBufferSizeOption:
		e.rcvListMu.Lock()
		*o = global.ReceiveBufferSizeOption(e.rcvBufSize)
		e.rcvListMu.Unlock()
		return nil

	case *global.ReceiveQueueSizeOption:
		v, err := e.readyReceiveSize()
		if err != nil {
			return err
		}

		*o = global.ReceiveQueueSizeOption(v)
		return nil

	case *global.NoDelayOption:
		e.mu.RLock()
		v := e.noDelay
		e.mu.RUnlock()

		*o = 0
		if v {
			*o = 1
		}
		return nil

	case *global.ReuseAddressOption:
		e.mu.RLock()
		v := e.reuseAddr
		e.mu.RUnlock()

		*o = 0
		if v {
			*o = 1
		}
		return nil

	case *global.V6OnlyOption:
		// We only recognize this option on v6 endpoints.
		if e.netProto != header.IPv6ProtocolNumber {
			return global.ErrInvalidEndpointState
		}

		e.mu.Lock()
		v := e.v6only
		e.mu.Unlock()

		*o = 0
		if v {
			*o = 1
		}
		return nil
	}

	return global.ErrInvalidEndpointState
}

func (e *endpoint) checkV4Mapped(addr *global.FullAddress) (global.NetworkProtocolNumber, error) {
	netProto := e.netProto
	if header.IsV4MappedAddress(addr.Addr) {
		// Fail if using a v4 mapped address on a v6only endpoint.
		if e.v6only {
			return 0, global.ErrNoRoute
		}

		netProto = header.MMProtocolNumber
		addr.Addr = addr.Addr[header.IPv6AddressSize-header.IPv4AddressSize:]
		if addr.Addr == "\x00\x00\x00\x00" {
			addr.Addr = ""
		}
	}

	// Fail if we're bound to an address length different from the one we're
	// checking.
	if l := len(e.id.LocalAddress); l != 0 && l != len(addr.Addr) {
		return 0, global.ErrInvalidEndpointState
	}

	return netProto, nil
}

// Connect connects the endpoint to its peer.
func (e *endpoint) Connect(addr global.FullAddress) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	netProto, err := e.checkV4Mapped(&addr)
	if err != nil {
		return err
	}

	nicid := addr.NIC
	switch e.state {
	case stateBound:
		// If we're already bound to a NIC but the caller is requesting
		// that we use a different one now, we cannot proceed.
		if e.boundNICID == 0 {
			break
		}

		if nicid != 0 && nicid != e.boundNICID {
			return global.ErrNoRoute
		}

		nicid = e.boundNICID

	case stateInitial:
		// Nothing to do. We'll eventually fill-in the gaps in the ID
		// (if any) when we find a route.

	case stateConnecting:
		// A connection request has already been issued but hasn't
		// completed yet.
		return global.ErrAlreadyConnecting

	case stateConnected:
		// The endpoint is already connected.
		return global.ErrAlreadyConnected

	default:
		return global.ErrInvalidEndpointState
	}

	// Find a route to the desired destination.
	r, err := e.stack.FindRoute(nicid, e.id.LocalAddress, addr.Addr, netProto)
	if err != nil {
		return err
	}
	defer r.Release()

	netProtos := []global.NetworkProtocolNumber{netProto}
	e.id.LocalAddress = r.LocalAddress
	e.id.LocalPort = 0
	//e.id.RemoteAddress = addr.Addr
	//e.id.RemotePort = addr.Port

	// The endpoint is bound to a port, attempt to register it.
	if err := e.stack.RegisterTransportEndpoint(nicid, netProtos, ProtocolNumber, e.id, e); err != nil {
		return err
	}

	// Remove the port reservation. This can happen when Bind is called
	// before Connect: in such a case we don't want to hold on to
	// reservations anymore.
	if e.isPortReserved {
		e.stack.ReleasePort(e.effectiveNetProtos, ProtocolNumber, e.id.LocalAddress, e.id.LocalPort)
		e.isPortReserved = false
	}

	e.isRegistered = true
	e.state = stateConnecting
	e.route = r.Clone()
	e.boundNICID = nicid
	e.effectiveNetProtos = netProtos
	e.workerRunning = true

	go e.protocolMainLoop(false)

	return global.ErrConnectStarted
}

func (e *endpoint) BindToStack(Addr global.Address) error {
	newid := e.id
	// 双向
	newid.LocalAddress = Addr
	newid.RemoteAddress = Addr
	err := e.stack.RegisterTransportEndpoint(e.boundNICID, e.effectiveNetProtos, ProtocolNumber, newid, e)
	return err
}

func (e *endpoint) UnBindFStack(Addr global.Address) {
	newid := e.id
	newid.LocalAddress = Addr
	newid.RemoteAddress = Addr
	e.stack.UnregisterTransportEndpoint(e.boundNICID, e.effectiveNetProtos, ProtocolNumber, newid)
}

// ConnectEndpoint is not supported.
func (*endpoint) ConnectEndpoint(global.Endpoint) error {
	return global.ErrInvalidEndpointState
}

// Shutdown closes the read and/or write end of the endpoint connection to its
// peer.
func (e *endpoint) Shutdown(flags global.ShutdownFlags) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	switch e.state {
	case stateConnected:
		// Close for write.
		if (flags & global.ShutdownWrite) != 0 {
			e.sndBufMu.Lock()
			defer e.sndBufMu.Unlock()

			if e.sndBufSize >= 0 {
				e.sndBufSize = -1
				close(e.sndChan)
			}
		}

	case stateListen:
		// Tell protocolListenLoop to stop.
		if flags&global.ShutdownRead != 0 {
			e.notifyProtocolGoroutine(notifyClose)
		}

	default:
		return global.ErrInvalidEndpointState
	}

	return nil
}

// Listen puts the endpoint in "listen" mode, which allows it to accept
// new connections.
func (e *endpoint) Listen(backlog int) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Endpoint must be bound before it can transition to listen mode.
	if e.state != stateBound {
		return global.ErrInvalidEndpointState
	}

	// Register the endpoint.
	if err := e.stack.RegisterTransportEndpoint(e.boundNICID, e.effectiveNetProtos, ProtocolNumber, e.id, e); err != nil {
		return err
	}

	e.isRegistered = true
	e.state = stateListen
	e.acceptedChan = make(chan *endpoint, backlog)
	e.workerRunning = true

	go e.protocolListenLoop(seqnum.Size(e.rcvBufSize))

	return nil
}

// startAcceptedLoop sets up required state and starts a goroutine with the
// main loop for accepted connections.
func (e *endpoint) startAcceptedLoop(waiterQueue *waiter.Queue) {
	e.waiterQueue = waiterQueue
	e.workerRunning = true
	go e.protocolMainLoop(true)
}

// Accept returns a new endpoint if a peer has established a connection
// to an endpoint previously set to listen mode.
func (e *endpoint) Accept() (global.Endpoint, *waiter.Queue, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	// Endpoint must be in listen state before it can accept connections.
	if e.state != stateListen {
		return nil, nil, global.ErrInvalidEndpointState
	}

	// Get the new accepted endpoint.
	var n *endpoint
	select {
	case n = <-e.acceptedChan:
	default:
		return nil, nil, global.ErrWouldBlock
	}

	// Start the protocol goroutine.
	wq := &waiter.Queue{}
	n.startAcceptedLoop(wq)

	return n, wq, nil
}

// Bind binds the endpoint to a specific local port and optionally address.
func (e *endpoint) Bind(addr global.FullAddress, commit func() error) (retErr error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Don't allow binding once endpoint is not in the initial state
	// anymore. This is because once the endpoint goes into a connected or
	// listen state, it is already bound.
	if e.state != stateInitial {
		return global.ErrAlreadyBound
	}

	netProto, err := e.checkV4Mapped(&addr)
	if err != nil {
		return err
	}

	// Expand netProtos to include v4 and v6 if the caller is binding to a
	// wildcard (empty) address, and this is an IPv6 endpoint with v6only
	// set to false.
	netProtos := []global.NetworkProtocolNumber{netProto}
	if netProto == header.IPv6ProtocolNumber && !e.v6only && addr.Addr == "" {
		netProtos = []global.NetworkProtocolNumber{
			header.IPv6ProtocolNumber,
			header.IPv4ProtocolNumber,
		}
	}

	// Reserve the port.
	port, err := e.stack.ReservePort(netProtos, ProtocolNumber, addr.Addr, addr.Port)
	if err != nil {
		return err
	}

	e.isPortReserved = true
	e.effectiveNetProtos = netProtos
	e.id.LocalPort = port

	// Any failures beyond this point must remove the port registration.
	defer func() {
		if retErr != nil {
			e.stack.ReleasePort(netProtos, ProtocolNumber, addr.Addr, port)
			e.isPortReserved = false
			e.effectiveNetProtos = nil
			e.id.LocalPort = 0
			e.id.LocalAddress = ""
			e.boundNICID = 0
		}
	}()

	// If an address is specified, we must ensure that it's one of our
	// local addresses.
	if len(addr.Addr) != 0 {
		nic := e.stack.CheckLocalAddress(addr.NIC, addr.Addr)
		if nic == 0 {
			return global.ErrBadLocalAddress
		}

		e.boundNICID = nic
		e.id.LocalAddress = addr.Addr
	}

	// Check the commit function.
	if commit != nil {
		if err := commit(); err != nil {
			// The defer takes care of unwind.
			return err
		}
	}

	// Mark endpoint as bound.
	e.state = stateBound

	return nil
}

// GetLocalAddress returns the address to which the endpoint is bound.
func (e *endpoint) GetLocalAddress() (global.FullAddress, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return global.FullAddress{
		Addr: e.id.LocalAddress,
		Port: e.id.LocalPort,
		NIC:  e.boundNICID,
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
		Addr: e.id.RemoteAddress,
		Port: e.id.RemotePort,
		NIC:  e.boundNICID,
	}, nil
}

// HandlePacket is called by the stack when new packets arrive to this transport
// endpoint.
func (e *endpoint) HandlePacket(v buffer.View, udpAddr *net.UDPAddr) {
	if nil == udpAddr {
		return
	}
	remote := global.Address(udpAddr.IP.To4())
	route, err := e.stack.FindRoute(e.boundNICID, e.id.LocalAddress, remote, header.MMProtocolNumber)
	if nil != err {
		log.Errorf("didn't found any matched route:{local:%v remote:%v}. err:%v.", err, e.id.LocalAddress, remote)
		return
	}
	var views [1]buffer.View
	vv := v.ToVectorisedView(views)
	id := stack.TransportEndpointID{uint16(0), e.id.LocalAddress, uint16(0), remote}

	log.Infof("[<=HandlePacket] dataLen:%v ID:%v", len(v), id)

	s := newSegment(&route, id, &vv, udpAddr)
	if !s.parse() {
		// TODO: Inform the stack that the packet is malformed.
		s.decRef()
		e.stack.RemoveAddress(e.boundNICID, remote)
		return
	}

	e.PushNetAddr(udpAddr) // push newAddr to addrsArr & addrsMap

	// Send packet to worker goroutine.
	select {
	case e.segmentChan <- s:
	default:
		// The channel is full, so we drop the segment.
		// TODO: Add some stat on this.
		s.decRef()
	}
}

func (e *endpoint) WriteToInterface() error {
	log.Infof("Now %v interfaceWriter running.", e.route)
	waitEntry, notifyCh := waiter.NewChannelEntry(nil)
	e.waiterQueue.EventRegister(&waitEntry, waiter.EventIn)
	defer e.waiterQueue.EventUnregister(&waitEntry)
	for {
		v, err := e.Read(nil)
		if err != nil {
			if err == global.ErrWouldBlock {
				<-notifyCh
				continue
			} else {
				e.Close()
				return err
			}
		}
		e.route.WritePacket(v, header.TCPProtocolNumber)
	}
	return nil
}

// updateSndBufferUsage is called by the protocol goroutine when room opens up
// in the send buffer. The number of newly available bytes is v.
func (e *endpoint) updateSndBufferUsage(v int) {
	e.sndBufMu.Lock()
	notify := e.sndBufUsed > e.sndBufSize
	e.sndBufUsed -= v
	notify = notify && e.sndBufUsed <= e.sndBufSize
	e.sndBufMu.Unlock()

	if notify {
		e.waiterQueue.Notify(waiter.EventOut)
	}
}

// readyToRead is called by the protocol goroutine when a new segment is ready
// to be read, or when the connection is closed for receiving (in which case
// s will be nil).
func (e *endpoint) readyToRead(s *segment) {
	e.rcvListMu.Lock()
	if s != nil {
		s.incRef()
		e.rcvBufUsed += s.data.Size()
		e.rcvList.PushBack(s)
	} else {
		e.rcvClosed = true
	}
	e.rcvListMu.Unlock()

	e.waiterQueue.Notify(waiter.EventIn)
}

// receiveBufferAvailable calculates how many bytes are still available in the
// receive buffer.
func (e *endpoint) receiveBufferAvailable() int {
	e.rcvListMu.Lock()
	size := e.rcvBufSize
	used := e.rcvBufUsed
	e.rcvListMu.Unlock()

	// We may use more bytes than the buffer size when the receive buffer
	// shrinks.
	if used >= size {
		return 0
	}

	return size - used
}

func (e *endpoint) receiveBufferSize() int {
	e.rcvListMu.Lock()
	size := e.rcvBufSize
	e.rcvListMu.Unlock()

	return size
}
