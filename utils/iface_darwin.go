package utils

import (
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"mtrix.io_vpn/global"
	"net"
	"os/exec"
	"strings"
)

/*
10.1.1.0        10.1.1.2        255.255.255.0   UG    0      0        0 tun0
10.1.1.2        0.0.0.0         255.255.255.255 UH    0      0        0 tun0
*/
// ifconfig tun0 10.1.1.1 10.1.1.2 up
// ip link set dev tun0 up mtu 1500 qlen 100
// ip route add 10.1.1.0/24 via 10.1.1.2 dev tun0
func SetTunIP(tunName string, mtu uint32, subnetIP global.Address, subnetMask uint8, client bool) error {
	ip, subnet, err := ParseCIDR(subnetIP, subnetMask)
	if nil != err {
		return err
	}

	ip = ip.To4()
	if ip[3]%2 == 0 {
		return errors.New("Invalid ip address.")
	}

	peer := net.IP(make([]byte, 4))
	copy([]byte(peer), []byte(ip))
	peer[3]++

	sargs := fmt.Sprintf("%s %s %s up", tunName, ip, peer)
	args := strings.Split(sargs, " ")
	cmd := exec.Command("ifconfig", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ifconfig %v err:%v", sargs, err))
	} else {
		log.Infof("ifconfig %s", sargs)
	}

	sargs = fmt.Sprintf("link set dev %s up mtu %d qlen 100", tunName, mtu)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	} else {
		log.Infof("ip %s", sargs)
	}

	sargs = fmt.Sprintf("route add %s via %s dev %s", subnet, peer, tunName)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	} else {
		log.Infof("ip %s", sargs)
	}

	if client { // for client
		peerStr := fmt.Sprintf("%d.%d.%d.%d", peer[0], peer[1], peer[2], peer[3])
		if err := RedirectGateway(tunName, peerStr); nil != err {
			log.Errorf("%v", err)
		}
	} else { // for server
		sargs = "net.ipv4.ip_forward=1"
		args = strings.Split(sargs, " ")
		cmd = exec.Command("sysctl", args...)
		if err := cmd.Run(); nil != err {
			log.Errorf("sysctl %v err:%v", sargs, err)
		}

		sargs = "-t nat -A POSTROUTING -j MASQUERADE"
		args = strings.Split(sargs, " ")
		cmd = exec.Command("iptables", args...)
		if err := cmd.Run(); nil != err {
			log.Errorf("iptables %v err:%v", sargs, err)
		}
	}
	return nil
}

// ip tuntap del mode tun tun0
func CleanTunIP(tunName string, subnetIP global.Address, subnetMask uint8, client bool) error {
	ip, subnet, err := ParseCIDR(subnetIP, subnetMask)
	if nil != err {
		return err
	}

	ip = ip.To4()
	if ip[3]%2 == 0 {
		return errors.New("Invalid ip address.")
	}

	peer := net.IP(make([]byte, 4))
	copy([]byte(peer), []byte(ip))
	peer[3]++

	sargs := fmt.Sprintf("route del %s via %s dev %s", subnet, peer, tunName)
	args := strings.Split(sargs, " ")
	cmd := exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		log.Errorf("ip %v err:%v", sargs, err)
	} else {
		log.Infof("ip %s", sargs)
	}

	sargs = fmt.Sprintf("link set %s down", tunName)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		log.Errorf("ip %v err:%v", sargs, err)
	} else {
		log.Infof("ip %s", sargs)
	}

	sargs = fmt.Sprintf("%s %s %s down", tunName, ip, peer)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ifconfig", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ifconfig %v err:%v", sargs, err))
	} else {
		log.Infof("ifconfig %s", sargs)
	}

	if client { // for client
		if err := UnRedirectGateway(); nil != err {
			log.Errorf("%v", err)
		}
	} else { // for server
		sargs = "net.ipv4.ip_forward=0"
		args = strings.Split(sargs, " ")
		cmd = exec.Command("sysctl", args...)
		if err := cmd.Run(); nil != err {
			log.Errorf("sysctl %v err:%v", sargs, err)
		}

		sargs = "-t nat -D POSTROUTING -j MASQUERADE"
		args = strings.Split(sargs, " ")
		cmd = exec.Command("iptables", args...)
		if err := cmd.Run(); nil != err {
			log.Errorf("iptables %v err:%v", sargs, err)
		}
	}
	return nil
}

// redirect default gateway
func RedirectGateway(tunName, gw string) error {
	// postup subnetwork's routes
	/*cmd := exec.Command("./scripts/chnroute-up_darwin.sh")
	if err := cmd.Run(); nil != err {
		log.Errorf("[RedirectGateway] postup:%v", err)
		return err
	}*/
	//subnets := []string{"0.0.0.0/1", "128.0.0.0/1"}
	subnets := []string{"108.177.97.104/12", "64.233.189.139/12"} // for test
	for _, subnet := range subnets {
		/* for client
		   10.1.1.4        0.0.0.0         255.255.255.255 UH    0      0        0 tun0
		   10.1.1.0        10.1.1.4        255.255.255.0   UG    0      0        0 tun0
		   0.0.0.0         10.1.1.4        128.0.0.0       UG    0      0        0 tun0
		   128.0.0.0       10.1.1.4        128.0.0.0       UG    0      0        0 tun0
		*/
		sargs := fmt.Sprintf("-4 route add %s via %s dev %s", subnet, gw, tunName)
		args := strings.Split(sargs, " ")
		cmd := exec.Command("ip", args...)
		log.Infof("[RedirectGateway] ip %s", sargs)
		err := cmd.Run()
		if err != nil {
			return err
		}
	}
	return nil
}

// unredirect default gateway
func UnRedirectGateway() error {
	//subnets := []string{"0.0.0.0/1", "128.0.0.0/1"}
	subnets := []string{"64.233.189.139/12", "108.177.97.104/12"} // fot test
	for _, subnet := range subnets {
		/* for client
		   10.1.1.4        0.0.0.0         255.255.255.255 UH    0      0        0 tun0
		   10.1.1.0        10.1.1.4        255.255.255.0   UG    0      0        0 tun0
		   0.0.0.0         10.1.1.4        128.0.0.0       UG    0      0        0 tun0
		   128.0.0.0       10.1.1.4        128.0.0.0       UG    0      0        0 tun0
		*/
		sargs := fmt.Sprintf("-4 route del %s", subnet)
		args := strings.Split(sargs, " ")
		cmd := exec.Command("ip", args...)
		log.Infof("[UnRedirectGateway] ip %s", sargs)
		err := cmd.Run()
		if err != nil {
			log.Errorf("%v", err)
		}
	}
	// postdown subnetwork's routes
	/*cmd := exec.Command("./scripts/chnroute-down_darwin.sh")
	if err := cmd.Run(); nil != err {
		log.Errorf("[UnRedirectGateway] postdown:%v", err)
	}*/
	return nil
}

func ParseCIDR(subnetIP global.Address, subnetMask uint8) (net.IP, *net.IPNet, error) {
	ipStr := fmt.Sprintf("%d.%d.%d.%d/%d", subnetIP[0], subnetIP[1], subnetIP[2], subnetIP[3], subnetMask)
	return net.ParseCIDR(ipStr)
}
