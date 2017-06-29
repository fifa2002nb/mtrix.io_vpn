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

func hearFromNet(listenEP global.Endpoint, s global.Stack, server string, port uint16) error {
	ipport := fmt.Sprintf("%v:%v", server, port)
	udpAddr, err := net.ResolveUDPAddr("udp", ipport)
	if err != nil {
		log.Errorf("Invalid port: %v", err)
		return err
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		log.Errorf("Failed to connect udp port %v:%v", port, err)
		return err
	}
	go func() {
		for {
			packet := s.GetPacket(port)
			log.Infof("[=>hearFromNet] port:%d WritToNet %v", port, packet.Addr)
			udpConn.WriteTo(packet.Data, packet.Addr)
		}
	}()

	// waiting for udp packet
	go func() {
		for {
			buf := make([]byte, 2048)
			plen, addr, err := udpConn.ReadFromUDP(buf)
			if nil != err {
				log.Errorf("[<=hearFromNet] port:%v err:%v", port, err)
			} else {
				log.Infof("[<=hearFromNet] port:%d ReadFromUDP %v", port, addr)
				listenEP.DispatchPacket(buffer.View(buf[:plen]), addr, port)
			}
		}
	}()

	return nil
}

func main() {
	if len(os.Args) != 3 {
		log.Fatal("Usage: ", os.Args[0], " <tun-device> <debug>")
	}

	tunName := os.Args[1]
	debug := os.Args[2]
	if "debug" == debug {
		log.SetLevel(log.DebugLevel)
	}

	rand.Seed(time.Now().UnixNano())

	// Create the stack with ip and tcp protocols, then add a tun-based
	// NIC and address.
	s := stack.New([]string{mm.ProtocolName}, []string{tcp.ProtocolName})

	if err := s.EnableIPPool("10.1.1.1/24"); nil != err {
		log.Fatal(err)
	}

	// init tap/tun device
	cltIP, err := s.GetNextIP()
	if nil != err {
		log.Fatal(err)
	}
	// assign subnetIP/Mask
	subnetIP := global.Address(cltIP.IP.To4())
	subnetMask, _ := cltIP.Mask.Size()
	defaultMtu := 1500
	err = utils.SetTunIP(tunName, uint32(defaultMtu), subnetIP, uint8(subnetMask), false)
	if nil != err {
		log.Fatal(err)
	}

	//其实已经在SetTunIP预设，这里装模作样取一下
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
	if err := s.AddAddress(1, mm.ProtocolNumber, global.Address("\x00\x00\x00\x00")); err != nil {
		log.Fatal(err)
	}

	// Add default route. 10.1.1.0/24
	s.SetRouteTable([]global.Route{
		{
			Destination: global.Address("\x00\x00\x00\x00"), // 0.0.0.0
			Mask:        global.Address("\x00\x00\x00\x00"), // 0.0.0.0
			Gateway:     "",
			NIC:         1,
		},
	})

	// Create TCP endpoint, bind it, then start listening.
	var wq waiter.Queue
	listenEP, err := s.NewEndpoint(tcp.ProtocolNumber, mm.ProtocolNumber, &wq)
	if err != nil {
		log.Fatal(err)
	}

	err = listenEP.InitSubnet(subnetIP, uint8(subnetMask))
	if nil != err {
		log.Fatal(err)
	}

	// bind to 10.1.1.2:0
	if err := listenEP.Bind(global.FullAddress{1, global.Address("\x00\x00\x00\x00"), 0}, nil); err != nil {
		log.Fatal("Bind failed: ", err)
	}

	if err := listenEP.Listen(10); err != nil {
		log.Fatal("Listen failed: ", err)
	}
	// Wait for connections to appear.
	waitEntry, notifyCh := waiter.NewChannelEntry(nil)
	wq.EventRegister(&waitEntry, waiter.EventIn)
	defer wq.EventUnregister(&waitEntry)

	go func() {
		for {
			n, _, err := listenEP.Accept()
			if err != nil {
				if err == global.ErrWouldBlock {
					<-notifyCh
					continue
				}
				log.Infof("Accept() err:%v", err)
				break
			} else {
				go n.WriteToInterface() // 注册成功则开始监听并写入数据到interface
			}
		}
	}()

	go hearFromNet(listenEP, s, "", 40000)
	//go hearFromNet(listenEP, s, "", 40001)
	//go hearFromNet(listenEP, s, "", 40002)
	//go hearFromNet(listenEP, s, "", 40003)

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
			listenEP.Close()
			time.Sleep(1 * time.Second)
			// close tun0 fd
			if linkEP := stack.FindLinkEndpoint(linkID); nil != linkEP {
				tun.Close(linkEP.GetFd())
			}
			time.Sleep(1 * time.Second)
			// shut down tun networkCard
			if err := utils.CleanTunIP(tunName, listenEP.GetSubnetIP(), listenEP.GetSubnetMask(), false); nil != err {
				log.Errorf("%v", err)
			}

			log.Info("done")
			os.Exit(1)
		}()
	}
}
