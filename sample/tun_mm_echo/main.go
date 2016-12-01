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
	"os"
	"time"

	"mtrix.io_vpn/global"
	"mtrix.io_vpn/link/fdbased"
	"mtrix.io_vpn/link/rawfile"
	"mtrix.io_vpn/link/tun"
	"mtrix.io_vpn/network/mm"
	"mtrix.io_vpn/stack"
	"mtrix.io_vpn/transport/mmm"
	"mtrix.io_vpn/waiter"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage: ", os.Args[0], " <tun-device> ")
	}

	tunName := os.Args[1]

	rand.Seed(time.Now().UnixNano())

	// Create the stack with ip and tcp protocols, then add a tun-based
	// NIC and address.
	s := stack.New([]string{mm.ProtocolName}, []string{mmm.ProtocolName})

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

	// add default networkEndpoint 10.1.1.2
	if err := s.AddAddress(1, mm.ProtocolNumber, global.Address("\x0A\x01\x01\x02")); err != nil {
		log.Fatal(err)
	}

	// Add default route. 10.1.1.0/24
	s.SetRouteTable([]global.Route{
		{
			Destination: global.Address("\x0A\x01\x01\x00"), // 10.1.1.0
			Mask:        global.Address("\xFF\xFF\xFF\x00"), // 255.255.255.0
			Gateway:     "",
			NIC:         1,
		},
	})

	// Create TCP endpoint, bind it, then start listening.
	var wq waiter.Queue
	ep, err := s.NewEndpoint(mmm.ProtocolNumber, mm.ProtocolNumber, &wq)
	if err != nil {
		log.Fatal(err)
	}

	defer ep.Close()

	// bind to 10.1.1.2:0
	if err := ep.Bind(global.FullAddress{1, global.Address("\x0A\x01\x01\x02"), 0}, nil); err != nil {
		log.Fatal("Bind failed: ", err)
	}

	// Wait for connections to appear.
	waitEntry, notifyCh := waiter.NewChannelEntry(nil)
	wq.EventRegister(&waitEntry, waiter.EventIn)
	defer wq.EventUnregister(&waitEntry)

	remoteAddr := global.FullAddress{}

	for {
		v, err := ep.Read(&remoteAddr)
		if err != nil {
			if err == global.ErrWouldBlock {
				<-notifyCh
				continue
			}
			return
		}

		log.Infof("sample recv:%v", v)
		_, err = ep.Write(v, &remoteAddr)
		if nil != err {
			log.Infof("write err:%v", err)
		}
	}
}
