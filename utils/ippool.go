package utils

import (
	"errors"
	log "github.com/Sirupsen/logrus"
	"net"
	"sync/atomic"
)

type IPPool struct {
	subnet *net.IPNet
	pool   [127]int32
}

func NewIPPool(addr string) *IPPool {
	_, subnet, err := net.ParseCIDR(cfg.Addr)
	if nil != err {
		return nil
	}
	return &IPPool{subnet: subnet.Mask}
}

func (p *IPPool) NextIP() (*net.IPNet, error) {
	found := false
	var i int
	for i = 3; i < 255; i += 2 {
		if atomic.CompareAndSwapInt32(&p.pool[i], 0, 1) {
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("IP Pool Full.")
	}

	ipnet := &net.IPNet{
		make([]byte, 4),
		make([]byte, 4),
	}
	copy([]byte(ipnet.IP), []byte(p.subnet.IP))
	copy([]byte(ipnet.Mask), []byte(p.subnet.Mask))
	ipnet.IP[3] = byte(i)
	return ipnet, nil
}

func (p *IPPool) ReleaseIP(ipnet *net.IPNet) {
	defer func() {
		if err := recover(); err != nil {
			log.Errorf("%v", err)
		}
	}()

	if nil != ipnet {
		i := ipnet.IP[3]
		p.pool[i] = 0
	}
}
