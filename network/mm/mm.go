// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package mm

import (
	log "github.com/Sirupsen/logrus"
	"mtrix.io_vpn/buffer"
	"mtrix.io_vpn/global"
	"mtrix.io_vpn/header"
	"mtrix.io_vpn/stack"
)

const (
	ProtocolName = "mm"

	// ProtocolNumber is the mm protocol number.
	ProtocolNumber = header.MMProtocolNumber

	maxTotalSize = 0xffff
)

type endpoint struct {
	nicid      global.NICID
	id         stack.NetworkEndpointID
	address    address
	linkEP     stack.LinkEndpoint
	dispatcher stack.TransportDispatcher
}

type address [header.MMAddressSize]byte

func newEndpoint(nicid global.NICID, addr global.Address, dispatcher stack.TransportDispatcher, linkEP stack.LinkEndpoint) *endpoint {
	e := &endpoint{
		nicid:      nicid,
		linkEP:     linkEP,
		dispatcher: dispatcher,
	}
	copy(e.address[:], addr)
	e.id = stack.NetworkEndpointID{global.Address(e.address[:])}

	return e
}

// MTU implements stack.NetworkEndpoint.MTU. It returns the link-layer MTU minus
// the network layer max header length.
func (e *endpoint) MTU() uint32 {
	lmtu := e.linkEP.MTU()
	if lmtu > maxTotalSize {
		lmtu = maxTotalSize
	}
	return lmtu + uint32(e.MaxHeaderLength())
}

// NICID returns the ID of the NIC this endpoint belongs to.
func (e *endpoint) NICID() global.NICID {
	return e.nicid
}

// ID returns the mv4 endpoint ID.
func (e *endpoint) ID() *stack.NetworkEndpointID {
	return &e.id
}

// MaxHeaderLength returns the maximum length needed by mpv4 headers (and
// underlying protocols).
func (e *endpoint) MaxHeaderLength() uint16 {
	return e.linkEP.MaxHeaderLength() + header.MMMinimumSize
}

func (e *endpoint) ParsePacketHeaders(v buffer.View, direction string) error {
	nv := buffer.NewView(len(v))
	copy(nv, v)

	var src global.Address
	var dst global.Address
	var srcPort uint16
	var dstPort uint16
	var p global.TransportProtocolNumber

	// parse ip header
	if header.IPv4Version == header.IPVersion(nv) {
		h := header.IPv4(nv)
		if !h.IsValid(len(nv)) {
			log.Errorf("[%sParsePacketHeaders] !ipv4.IsValid(%v)", direction, len(nv))
			return nil
		}
		// For now we don't support fragmentation, so reject fragmented packets.
		if h.FragmentOffset() != 0 || (h.Flags()&header.IPv4FlagMoreFragments) != 0 {
			log.Errorf("[%sParsePacketHeaders] now we don't support fragmentation. reject it.", direction)
			return nil
		}
		src, dst = h.SourceAddress(), h.DestinationAddress()
		hlen := int(h.HeaderLength())
		tlen := int(h.TotalLength())
		nv.TrimFront(hlen)
		nv.CapLength(tlen - hlen)
		p = global.TransportProtocolNumber(h.Protocol())
	} else if header.IPv6Version == header.IPVersion(nv) {
		h := header.IPv6(nv)
		if !h.IsValid(len(nv)) {
			log.Errorf("[%sParsePacketHeaders] !ipv6.IsValid(%v)", direction, len(nv))
			return nil
		}
		src, dst = h.SourceAddress(), h.DestinationAddress()
		nv.TrimFront(header.IPv6MinimumSize)
		p = global.TransportProtocolNumber(h.NextHeader())
	} else {
		log.Errorf("unknown network protocol.")
		return nil
	}

	if p == header.UDPProtocolNumber {
		// parse udp header
		hdr := header.UDP(nv)
		if int(hdr.Length()) > len(nv) {
			// Malformed packet.
			log.Errorf("[%sParsePacketHeaders] malformed packet %v", direction, nv)
			return nil
		}
		srcPort, dstPort = hdr.SourcePort(), hdr.DestinationPort()
		nv.TrimFront(header.UDPMinimumSize)
		log.Debugf("[%sParsePacketHeaders] udp src %v:%v dst %v:%v", direction, src, srcPort, dst, dstPort)
	} else if p == header.TTPProtocolNumber {
		// parse udp header
		hdr := header.TTP(nv)
		offset := int(hdr.DataOffset())
		if offset < header.TTPMinimumSize || offset > len(hdr) {
			log.Errorf("[%sParsePacketHeaders] offset < header.TTPMinimumSize || offset > len(hdr)", direction)
			return nil
		}
		options := []byte(hdr[header.TTPMinimumSize:offset])
		sequenceNumber := hdr.SequenceNumber()
		ackNumber := hdr.AckNumber()
		flags := hdr.Flags()
		window := hdr.WindowSize()

		srcPort, dstPort = hdr.SourcePort(), hdr.DestinationPort()
		nv.TrimFront(header.TTPMinimumSize)
		log.Debugf("[%sParsePacketHeaders] tcp src %v:%v dst %v:%v options:%v seqNum:%v ackNum:%v flags:%v window:%v", direction, src, srcPort, dst, dstPort, options, sequenceNumber, ackNumber, flags, window)
	} else {
		//log.Debugf("[%sParsePacketHeaders] maybe icmp protocol.", direction)
	}
	return nil
}

// WritePacket writes a packet to the given destination address and protocol.
func (e *endpoint) WritePacket(r *stack.Route, payload buffer.View, protocol global.TransportProtocolNumber) error {
	// 取MM协议头部
	m := header.MM(payload)
	// 做一些MM协议的验证
	if header.MM_FLG_DAT != m.Flag() || m.PayloadLength() > m.TotalLength() {
		log.Errorf("flag: %v!=%v and len: %v>=%v", header.MM_FLG_DAT, m.Flag(), m.PayloadLength(), m.TotalLength())
		//return nil
	}
	// 剥掉MM协议的头部，发送数据部分
	payload.TrimFront(header.MMMinimumSize)
	log.Debugf("[<=WritePacket] payloadSize:%v route:%v", len(payload), r)
	//e.ParsePacketHeaders(payload, "<=") // for test
	return e.linkEP.WritePacket(r, payload, ProtocolNumber)
}

func (e *endpoint) ReverseHandlePacket(r *stack.Route, vv *buffer.VectorisedView) {
	//e.ParsePacketHeaders(vv.ToView(), "=>") // for test

	h := header.IPv4(vv.First())
	if !h.IsValid(vv.Size()) {
		return
	}

	hdr := buffer.NewPrependable(int(r.MaxHeaderLength()))
	m := header.MM(hdr.Prepend(header.MMMinimumSize))
	m.Encode(&header.MMFields{
		Magic:         uint16(header.MMMagic), // MM协议的魔幻数
		Flag:          header.MM_FLG_DAT,      // 暂时初始化为DAT类型
		Seq:           uint32(0),              // 暂时初始化为0
		SessionId:     r.LocalAddress,         // sessionId用客户端4字节IP来代替
		PayloadLength: uint16(vv.Size()),      // 数据报大小
		TotalLength:   uint16(vv.Size()),      // 暂时初始化为0，传输层中增加noise后计算总长度
	})

	e.dispatcher.ReverseDeliverTransportPacket(r, header.TCPProtocolNumber, &hdr, vv)
}

type protocol struct{}

// NewProtocol creates a new protocol mv4 protocol descriptor. This is exported
// only for tests that short-circuit the stack. Regular use of the protocol is
// done via the stack, which gets a protocol descriptor from the init() function
// below.
func NewProtocol() stack.NetworkProtocol {
	return &protocol{}
}

// Number returns the mv4 protocol number.
func (p *protocol) Number() global.NetworkProtocolNumber {
	return ProtocolNumber
}

// MinimumPacketSize returns the minimum valid mv4 packet size.
func (p *protocol) MinimumPacketSize() int {
	return header.MMMinimumSize
}

// ParseAddresses implements NetworkProtocol.ParseAddresses.
func (*protocol) ParseAddresses(v buffer.View) (src, dst global.Address) {
	h := header.IPv4(v)
	return h.SourceAddress(), h.DestinationAddress()
}

// NewEndpoint creates a new mv4 endpoint.
func (p *protocol) NewEndpoint(nicid global.NICID, addr global.Address, dispatcher stack.TransportDispatcher, linkEP stack.LinkEndpoint) (stack.NetworkEndpoint, error) {
	return newEndpoint(nicid, addr, dispatcher, linkEP), nil
}

func init() {
	stack.RegisterNetworkProtocol(ProtocolName, NewProtocol())
}
