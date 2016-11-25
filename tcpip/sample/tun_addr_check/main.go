// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This sample creates a stack with TCP and IPv4 protocols on top of a TUN
// device, and listens on a port. Data received by the server in the accepted
// connections is echoed back to the clients.
package main

import (
	log "github.com/Sirupsen/logrus"
	"math/rand"
	"mtrix.io_vpn/tcpip"
	"mtrix.io_vpn/tcpip/link/fdbased"
	"mtrix.io_vpn/tcpip/link/rawfile"
	"mtrix.io_vpn/tcpip/link/tun"
	"mtrix.io_vpn/tcpip/network/ipv4"
	"mtrix.io_vpn/tcpip/stack"
	"mtrix.io_vpn/tcpip/transport/udp"
	"mtrix.io_vpn/waiter"
	"os"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("Usage: ", os.Args[0], " <tun-device> <local-port>")
	}

	tunName := os.Args[1]
	portName := os.Args[2]

	rand.Seed(time.Now().UnixNano())

	localPort, err := strconv.Atoi(portName)
	if err != nil {
		log.Fatalf("Unable to convert port %v: %v", portName, err)
	}

	// Create the stack with ip and tcp protocols, then add a tun-based
	// NIC and address.
	s := stack.New([]string{ipv4.ProtocolName}, []string{udp.ProtocolName})

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

	if err := s.AddAddress(1, ipv4.ProtocolNumber, tcpip.Address("\x0A\x01\x01\x02")); err != nil {
		log.Fatal(err)
	}

	// Add default route. 10.1.1.0/24
	s.SetRouteTable([]tcpip.Route{
		{
			Destination: tcpip.Address("\x0A\x01\x01\x00"), //10.1.1.0
			Mask:        tcpip.Address("\xFF\xFF\xFF\x00"), //255.255.255.0
			Gateway:     "",
			NIC:         1,
		},
	})

	// Create TCP endpoint, bind it, then start listening.
	var wq waiter.Queue
	ep, err := s.NewEndpoint(udp.ProtocolNumber, ipv4.ProtocolNumber, &wq)
	if err != nil {
		log.Fatal(err)
	}

	defer ep.Close()

	// bind to 10.1.1.2:999
	if err := ep.Bind(tcpip.FullAddress{1, tcpip.Address("\x0A\x01\x01\x02"), uint16(localPort)}, nil); err != nil {
		log.Fatal("Bind failed: ", err)
	}

	// Wait for connections to appear.
	waitEntry, notifyCh := waiter.NewChannelEntry(nil)
	wq.EventRegister(&waitEntry, waiter.EventIn)
	defer wq.EventUnregister(&waitEntry)

	remoteAddr := tcpip.FullAddress{}

	for {
		v, err := ep.Read(&remoteAddr)
		if err != nil {
			if err == tcpip.ErrWouldBlock {
				<-notifyCh
				continue
			}
			return
		}
		log.Infof("rcv remote:%v packet:%v", remoteAddr, v)
	}
}
