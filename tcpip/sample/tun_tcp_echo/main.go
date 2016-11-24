// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This sample creates a stack with TCP and IPv4 protocols on top of a TUN
// device, and listens on a port. Data received by the server in the accepted
// connections is echoed back to the clients.
package main

import (
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"mtrix.io_vpn/tcpip"
	"mtrix.io_vpn/tcpip/link/fdbased"
	"mtrix.io_vpn/tcpip/link/rawfile"
	"mtrix.io_vpn/tcpip/link/tun"
	"mtrix.io_vpn/tcpip/network/mv4"
	"mtrix.io_vpn/tcpip/stack"
	"mtrix.io_vpn/tcpip/transport/mm"
	"mtrix.io_vpn/waiter"
)

func main() {
	if len(os.Args) != 4 {
		log.Fatal("Usage: ", os.Args[0], " <tun-device> ")
	}

	tunName := os.Args[1]

	rand.Seed(time.Now().UnixNano())

	// Create the stack with ip and tcp protocols, then add a tun-based
	// NIC and address.
	s := stack.New([]string{mv4.ProtocolName}, []string{mm.ProtocolName})

	mtu, err := rawfile.GetMTU(tunName)
	if err != nil {
		log.Fatal(err)
	}

	fd, err := tun.Open(tunName)
	if err != nil {
		log.Fatal(err)
	}

	linkID := fdbased.New(fd, mtu, nil)
	if err := s.CreateNIC(1, linkID); err != nil {
		log.Fatal(err)
	}

	// add default networkEndpoint 2.2.2.2
	if err := s.AddAddress(1, mv4.ProtocolNumber, tcpip.Address(strings.Repeat("\x02", 4))); err != nil {
		log.Fatal(err)
	}

	// Add default route. 1.1.1.1
	s.SetRouteTable([]tcpip.Route{
		{
			Destination: tcpip.Address(strings.Repeat("\x01", 4)),
			Mask:        tcpip.Address(strings.Repeat("\x00", 4)),
			Gateway:     "",
			NIC:         1,
		},
	})

	// Create TCP endpoint, bind it, then start listening.
	var wq waiter.Queue
	ep, err := s.NewEndpoint(mm.ProtocolNumber, mv4.ProtocolNumber, &wq)
	if err != nil {
		log.Fatal(err)
	}

	defer ep.Close()

	// bind to 2.2.2.2:999
	if err := ep.Bind(tcpip.FullAddress{1, tcpip.Address(strings.Repeat("\x02", 4)), 999}, nil); err != nil {
		log.Fatal("Bind failed: ", err)
	}

	// Wait for connections to appear.
	waitEntry, notifyCh := waiter.NewChannelEntry(nil)
	wq.EventRegister(&waitEntry, waiter.EventIn)
	defer wq.EventUnregister(&waitEntry)

	remoteAddr := tcpip.Address{}

	for {
		v, err := ep.Read(&remoteAddr)
		if err != nil {
			if err == tcpip.ErrWouldBlock {
				<-notifyCh
				continue
			}

			return
		}

		ep.Write(v, &remoteAddr)
	}
}
