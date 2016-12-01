// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package fdbased provides the implemention of data-link layer endpoints
// backed by boundary-preserving file descriptors (e.g., TUN devices,
// seqpacket/datagram sockets).
//
// FD based endpoints can be used in the networking stack by calling New() to
// create a new endpoint, and then passing it as an argument to
// Stack.CreateNIC().
package fdbased

import (
	"syscall"

	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/header"
	"mtrix.io_vpn/link/rawfile"
	"mtrix.io_vpn/stack"
)

// BufConfig defines the shape of the vectorised view used to read packets from the NIC.
var BufConfig = []int{128, 256, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768}

type endpoint struct {
	// fd is the file descriptor used to send and receive packets.
	fd int

	// mtu (maximum transmission unit) is the maximum size of a packet.
	mtu int

	// closed is a function to be called when the FD's peer (if any) closes
	// its end of the communication pipe.
	closed func(error)

	vv     *buffer.VectorisedView
	iovecs []syscall.Iovec
	views  []buffer.View
}

// New creates a new fd-based endpoint.
func New(fd int, mtu int, closed func(error)) global.LinkEndpointID {
	syscall.SetNonblock(fd, true)

	e := &endpoint{
		fd:     fd,
		mtu:    mtu,
		closed: closed,
		views:  make([]buffer.View, len(BufConfig)),
		iovecs: make([]syscall.Iovec, len(BufConfig)),
	}
	vv := buffer.NewVectorisedView(0, e.views)
	e.vv = &vv
	return stack.RegisterLinkEndpoint(e)
}

// Attach launches the goroutine that reads packets from the file descriptor and
// dispatches them via the provided dispatcher.
func (e *endpoint) Attach(dispatcher stack.NetworkDispatcher) {
	go e.dispatchLoop(dispatcher)
}

// MTU implements stack.LinkEndpoint.MTU. It returns the value initialized
// during construction.
func (e *endpoint) MTU() uint32 {
	return uint32(e.mtu)
}

// MaxHeaderLength returns the maximum size of the header. Given that it
// doesn't have a header, it just returns 0.
func (*endpoint) MaxHeaderLength() uint16 {
	return 0
}

// WritePacket writes outbound packets to the file descriptor. If it is not
// currently writable, the packet is dropped.
func (e *endpoint) WritePacket(_ *stack.Route, payload buffer.View, protocol global.NetworkProtocolNumber) error {
	return rawfile.NonBlockingWrite(e.fd, payload)
}

func (e *endpoint) capViews(n int, buffers []int) int {
	c := 0
	for i, s := range buffers {
		c += s
		if c >= n {
			e.views[i].CapLength(s - (c - n))
			return i + 1
		}
	}
	return len(buffers)
}

func (e *endpoint) allocateViews(bufConfig []int) {
	for i, v := range e.views {
		if v != nil {
			break
		}
		b := buffer.NewView(bufConfig[i])
		e.views[i] = b
		e.iovecs[i] = syscall.Iovec{
			Base: &b[0],
			Len:  uint64(len(b)),
		}
	}
}

// dispatch reads one packet from the file descriptor and dispatches it.
func (e *endpoint) dispatch(d stack.NetworkDispatcher, largeV buffer.View) (bool, error) {
	e.allocateViews(BufConfig)

	n, err := rawfile.BlockingReadv(e.fd, e.iovecs)
	if err != nil {
		return false, err
	}

	if n <= 0 {
		return false, nil
	}

	used := e.capViews(n, BufConfig)
	e.vv.SetViews(e.views[:used])
	e.vv.SetSize(n)

	// We don't get any indication of what the packet is, so try to guess
	// if it's an IPv4 or IPv6 packet.
	var p global.NetworkProtocolNumber
	switch header.IPVersion(e.views[0]) {
	case header.IPv4Version:
		p = header.MMProtocolNumber
	case header.IPv6Version:
		p = header.MMProtocolNumber
	default:
		return true, nil
	}

	// for vpn package packets
	d.ReverseDeliverNetworkPacket(e, p, e.vv)

	// Prepare e.views for another packet: release used views.
	for i := 0; i < used; i++ {
		e.views[i] = nil
	}

	return true, nil
}

// dispatchLoop reads packets from the file descriptor in a loop and dispatches
// them to the network stack.
func (e *endpoint) dispatchLoop(d stack.NetworkDispatcher) error {
	v := buffer.NewView(header.MaxIPPacketSize)
	for {
		cont, err := e.dispatch(d, v)
		if err != nil || !cont {
			if e.closed != nil {
				e.closed(err)
			}
			return err
		}
	}
}
