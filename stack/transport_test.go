// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stack_test

import (
	"io"
	"testing"

	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/link/channel"
	"mtrix.io_vpn/stack"
	"mtrix.io_vpn/waiter"
)

const (
	fakeTransNumber    global.TransportProtocolNumber = 1
	fakeTransHeaderLen                                = 3
)

// fakeTransportEndpoint is a transport-layer protocol endpoint. It counts
// received packets; the counts of all endpoints are aggregated in the protocol
// descriptor.
//
// Headers of this protocol are fakeTransHeaderLen bytes, but we currently don't
// use it.
type fakeTransportEndpoint struct {
	id       stack.TransportEndpointID
	stack    *stack.Stack
	netProto global.NetworkProtocolNumber
	proto    *fakeTransportProtocol
	peerAddr global.Address
	route    stack.Route
}

func newFakeTransportEndpoint(stack *stack.Stack, proto *fakeTransportProtocol, netProto global.NetworkProtocolNumber) global.Endpoint {
	return &fakeTransportEndpoint{stack: stack, netProto: netProto, proto: proto}
}

func (f *fakeTransportEndpoint) Close() {
	f.route.Release()
}

func (*fakeTransportEndpoint) Readiness(mask waiter.EventMask) waiter.EventMask {
	return mask
}

func (*fakeTransportEndpoint) Read(*global.FullAddress) (buffer.View, error) {
	return buffer.View{}, nil
}

func (f *fakeTransportEndpoint) Write(v buffer.View, _ *global.FullAddress) (uintptr, error) {
	if len(f.route.RemoteAddress) == 0 {
		return 0, global.ErrNoRoute
	}

	hdr := buffer.NewPrependable(int(f.route.MaxHeaderLength()))
	err := f.route.WritePacket(&hdr, v, fakeTransNumber)
	if err != nil {
		return 0, err
	}

	return uintptr(len(v)), nil
}

func (f *fakeTransportEndpoint) RecvMsg(*global.FullAddress) (buffer.View, global.ControlMessages, error) {
	return buffer.View{}, nil, nil
}

func (f *fakeTransportEndpoint) SendMsg(v buffer.View, _ global.ControlMessages, _ *global.FullAddress) (uintptr, error) {
	return f.Write(v, nil)
}

func (f *fakeTransportEndpoint) Peek(io.Writer) (uintptr, error) {
	return 0, nil
}

// SetSockOpt sets a socket option. Currently not supported.
func (*fakeTransportEndpoint) SetSockOpt(interface{}) error {
	return global.ErrInvalidEndpointState
}

// GetSockOpt implements global.Endpoint.GetSockOpt.
func (*fakeTransportEndpoint) GetSockOpt(opt interface{}) error {
	switch opt.(type) {
	case global.ErrorOption:
		return nil
	}
	return global.ErrInvalidEndpointState
}

func (f *fakeTransportEndpoint) Connect(addr global.FullAddress) error {
	f.peerAddr = addr.Addr

	// Find the route.
	r, err := f.stack.FindRoute(addr.NIC, "", addr.Addr, fakeNetNumber)
	if err != nil {
		return global.ErrNoRoute
	}
	defer r.Release()

	// Try to register so that we can start receiving packets.
	f.id.RemoteAddress = addr.Addr
	err = f.stack.RegisterTransportEndpoint(0, []global.NetworkProtocolNumber{fakeNetNumber}, fakeTransNumber, f.id, f)
	if err != nil {
		return err
	}

	f.route = r.Clone()

	return nil
}

func (f *fakeTransportEndpoint) ConnectEndpoint(e global.Endpoint) error {
	return nil
}

func (*fakeTransportEndpoint) Shutdown(global.ShutdownFlags) error {
	return nil
}

func (*fakeTransportEndpoint) Reset() {
}

func (*fakeTransportEndpoint) Listen(int) error {
	return nil
}

func (*fakeTransportEndpoint) Accept() (global.Endpoint, *waiter.Queue, error) {
	return nil, nil, nil
}

func (*fakeTransportEndpoint) Bind(_ global.FullAddress, commit func() error) error {
	return commit()
}

func (*fakeTransportEndpoint) GetLocalAddress() (global.FullAddress, error) {
	return global.FullAddress{}, nil
}

func (*fakeTransportEndpoint) GetRemoteAddress() (global.FullAddress, error) {
	return global.FullAddress{}, nil
}

func (f *fakeTransportEndpoint) HandlePacket(*stack.Route, stack.TransportEndpointID, *buffer.VectorisedView) {
	// Increment the number of received packets.
	f.proto.packetCount++
}

// fakeTransportProtocol is a transport-layer protocol descriptor. It
// aggregates the number of packets received via endpoints of this protocol.
type fakeTransportProtocol struct {
	packetCount int
}

func (*fakeTransportProtocol) Number() global.TransportProtocolNumber {
	return fakeTransNumber
}

func (f *fakeTransportProtocol) NewEndpoint(stack *stack.Stack, netProto global.NetworkProtocolNumber, _ *waiter.Queue) (global.Endpoint, error) {
	return newFakeTransportEndpoint(stack, f, netProto), nil
}

func (*fakeTransportProtocol) MinimumPacketSize() int {
	return fakeTransHeaderLen
}

func (*fakeTransportProtocol) ParsePorts(buffer.View) (src, dst uint16, err error) {
	return 0, 0, nil
}

func (*fakeTransportProtocol) HandleUnknownDestinationPacket(*stack.Route, stack.TransportEndpointID, *buffer.VectorisedView) {
}

func TestTransportReceive(t *testing.T) {
	id, linkEP := channel.New(10, defaultMTU)
	s := stack.New([]string{"fakeNet"}, []string{"fakeTrans"})
	if err := s.CreateNIC(1, id); err != nil {
		t.Fatalf("CreateNIC failed: %v", err)
	}

	s.SetRouteTable([]global.Route{{"\x00", "\x00", "\x00", 1}})

	if err := s.AddAddress(1, fakeNetNumber, "\x01"); err != nil {
		t.Fatalf("AddAddress failed: %v", err)
	}

	// Create endpoint and connect to remote address.
	wq := waiter.Queue{}
	ep, err := s.NewEndpoint(fakeTransNumber, fakeNetNumber, &wq)
	if err != nil {
		t.Fatalf("NewEndpoint failed: %v", err)
	}

	if err := ep.Connect(global.FullAddress{0, "\x02", 0}); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	var views [1]buffer.View
	// Create buffer that will hold the packet.
	buf := buffer.NewView(30)

	// Make sure packet with wrong protocol is not delivered.
	buf[0] = 1
	buf[2] = 0
	vv := buf.ToVectorisedView(views)
	linkEP.Inject(fakeNetNumber, &vv)
	if fakeTrans.packetCount != 0 {
		t.Errorf("packetCount = %d, want %d", fakeTrans.packetCount, 0)
	}

	// Make sure packet from the wrong source is not delivered.
	buf[0] = 1
	buf[1] = 3
	buf[2] = byte(fakeTransNumber)
	vv = buf.ToVectorisedView(views)
	linkEP.Inject(fakeNetNumber, &vv)
	if fakeTrans.packetCount != 0 {
		t.Errorf("packetCount = %d, want %d", fakeTrans.packetCount, 0)
	}

	// Make sure packet is delivered.
	buf[0] = 1
	buf[1] = 2
	buf[2] = byte(fakeTransNumber)
	vv = buf.ToVectorisedView(views)
	linkEP.Inject(fakeNetNumber, &vv)
	if fakeTrans.packetCount != 1 {
		t.Errorf("packetCount = %d, want %d", fakeTrans.packetCount, 1)
	}
}

func TestTransportSend(t *testing.T) {
	id, _ := channel.New(10, defaultMTU)
	s := stack.New([]string{"fakeNet"}, []string{"fakeTrans"})
	if err := s.CreateNIC(1, id); err != nil {
		t.Fatalf("CreateNIC failed: %v", err)
	}

	if err := s.AddAddress(1, fakeNetNumber, "\x01"); err != nil {
		t.Fatalf("AddAddress failed: %v", err)
	}

	s.SetRouteTable([]global.Route{{"\x00", "\x00", "\x00", 1}})

	// Create endpoint and bind it.
	wq := waiter.Queue{}
	ep, err := s.NewEndpoint(fakeTransNumber, fakeNetNumber, &wq)
	if err != nil {
		t.Fatalf("NewEndpoint failed: %v", err)
	}

	if err := ep.Connect(global.FullAddress{0, "\x02", 0}); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Create buffer that will hold the payload.
	view := buffer.NewView(30)
	_, err = ep.Write(view, nil)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if fakeNet.sendPacketCount[2] != 1 {
		t.Errorf("sendPacketCount = %d, want %d", fakeNet.sendPacketCount[2], 1)
	}
}

var fakeTrans fakeTransportProtocol

func init() {
	stack.RegisterTransportProtocol("fakeTrans", &fakeTrans)
}
