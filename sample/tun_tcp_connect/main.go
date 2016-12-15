// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// This sample creates a stack with TCP and IPv4 protocols on top of a TUN
// device, and listens on a port. Data received by the server in the accepted
// connections is echoed back to the clients.
package main

import (
	"fmt"
	log "github.com/Sirupsen/logrus"
	"math/rand"
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/link/fdbased"
	"mtrix.io_vpn/link/rawfile"
	"mtrix.io_vpn/link/tun"
	"mtrix.io_vpn/network/mm"
	"mtrix.io_vpn/stack"
	"mtrix.io_vpn/transport/tcp"
	"mtrix.io_vpn/utils"
	"mtrix.io_vpn/waiter"
	"net"
	"os"
	"os/signal"
	"time"
)

func connectToNet(clientEP global.Endpoint, s global.Stack, server string, port uint16) error {
	ipport := fmt.Sprintf("%v:%v", server, port)
	udpAddr, err := net.ResolveUDPAddr("udp", ipport)
	if err != nil {
		log.Errorf("Invalid port: %v", err)
		return err
	}
	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		log.Errorf("Failed to connect udp port %v:%v", port, err)
		return err
	}
	go func() {
		for {
			packet := s.GetPacket()
			udpConn.Write(packet.Data)
		}
	}()

	// wait for udp packets
	for {
		buf := make([]byte, 2048)
		plen, udpAddr, err := udpConn.ReadFromUDP(buf)
		if nil != err {
			log.Errorf("%v", err)
		} else {
			clientEP.HandlePacket(buffer.View(buf[:plen]), udpAddr)
		}
	}
	return nil
}

func LazyEnableNIC(clientEP global.Endpoint, s global.Stack, tunName string, linkID global.LinkEndpointID, NICID global.NICID) {
	for {
		timer := time.NewTimer(time.Second * 1)
		<-timer.C
		if clientEP.InitedSubnet() {
			if localIP, _, err := utils.ParseCIDR(clientEP.GetSubnetIP, clientEP.GetSubnetMask); nil != err {
				if err := s.AddAddress(NICID, mm.ProtocolNumber, clientEP.GetSubnetIP()); nil != err {
					clientEP.Close()
					log.Errorf("%v", err)
				}
			} else {
				clientEP.Close()
				log.Errorf("%v", err)
			}
			if err := utils.SetTunIP(tunName, clientEP.GetSubnetIP(), clientEP.GetSubnetMask()); nil != err {
				clientEP.Close()
				log.Errorf("setTunIP err:%v", err)
			}
			mtu, err := rawfile.GetMTU(tunName)
			if err != nil {
				clientEP.Close()
				log.Errorf("getMTU err:%v", err)
				return
			}

			fd, err := tun.Open(tunName)
			if err != nil {
				clientEP.Close()
				log.Errorf("openTun err:%v", err)
				return
			}
			linkEP := stack.FindLinkEndpoint(linkID)
			linkEP.SetMTU(uint32(mtu))
			linkEP.SetFd(fd)
			s.EnableNIC(NICID)
			log.Infof("[waitingForEnableNIC] enabled NIC:%v subnetIP:%v subnetMask:%v", NICID, clientEP.GetSubnetIP(), clientEP.GetSubnetMask())
			break
		}
	}
}

func main() {
	if len(os.Args) != 2 {
		log.Fatal("Usage: ", os.Args[0], " <tun-device> ")
	}

	tunName := os.Args[1]

	rand.Seed(time.Now().UnixNano())

	// Create the stack with ip and tcp protocols, then add a tun-based
	// NIC and address.
	s := stack.New([]string{mm.ProtocolName}, []string{tcp.ProtocolName})

	// fd and mtu inited by default value
	linkID := fdbased.New(-1, -1, nil)
	NICID := global.NICID(1)
	if err := s.CreateDisabledNIC(NICID, linkID); err != nil {
		log.Fatal(err)
	}

	// Add default route. 10.1.1.0/24
	s.SetRouteTable([]global.Route{
		{
			Destination: global.Address("\x00\x00\x00\x00"), // 10.1.1.0
			Mask:        global.Address("\x00\x00\x00\x00"), // 255.255.255.0
			Gateway:     "",
			NIC:         1,
		},
	})

	// Create TCP endpoint, bind it, then start listening.
	var wq waiter.Queue
	connectEP, err := s.NewEndpoint(tcp.ProtocolNumber, mm.ProtocolNumber, &wq)
	if err != nil {
		log.Infof("%v", err)
	}

	defer connectEP.Close()

	// connect to 10.1.1.2:0
	addrName := "115.29.175.52"
	srv := net.ParseIP(addrName)
	if err := s.AddAddress(NICID, mm.ProtocolNumber, global.Address(srv.To4())); err != nil {
		log.Infof("%v", err)
	}

	if err := connectEP.Connect(global.FullAddress{NICID, global.Address(srv.To4()), 0}); err != nil {
		log.Infof("%v", err)
	}

	go LazyEnableNIC(connectEP, s, tunName, linkID, NICID)

	go func() {
		sc := make(chan os.Signal, 1)
		signal.Notify(sc, os.Interrupt)
		killing := false
		for range sc {
			if killing {
				log.Info("Second interrupt: exiting")
				os.Exit(1)
			}
			killing = true
			go func() {
				log.Info("Interrupt: closing down...")
				connectEP.Close()
				log.Info("done")
				os.Exit(1)
			}()
		}
	}()

	connectToNet(connectEP, s, addrName, 40000)
}
