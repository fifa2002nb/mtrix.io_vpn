package utils

import (
	"errors"
	"fmt"
	"mtrix.io_vpn/global"
	"net"
	"os/exec"
	"strings"
	//log "github.com/Sirupsen/logrus"
)

/*
10.1.1.0        10.1.1.2        255.255.255.0   UG    0      0        0 tun0
10.1.1.2        0.0.0.0         255.255.255.255 UH    0      0        0 tun0
*/
// ip tuntap add mode tun tun0
// ip link set dev tun0 up mtu 1500 qlen 100
// ip addr add dev tun0 local 10.1.1.1 peer 10.1.1.2
// ip route add 10.1.1.0/24 via 10.1.1.2 dev tun0
func SetTunIP(tunName string, subnetIP global.Address, subnetMask uint8) error {
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

	sargs := fmt.Sprintf("tuntap add mode tun %s", tunName)
	args := strings.Split(sargs, " ")
	cmd := exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		//return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	}

	sargs = fmt.Sprintf("link set dev %s up mtu 1500 qlen 100", tunName)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	}

	sargs = fmt.Sprintf("addr add dev %s local %s peer %s", tunName, ip, peer)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	}

	sargs = fmt.Sprintf("route add %s via %s dev %s", subnet, peer, tunName)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	}
	return nil
}

// ip tuntap del mode tun tun0
func CleanTunIP(tunName string, subnetIP global.Address, subnetMask uint8) error {
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
		return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	}

	sargs = fmt.Sprintf("addr del dev %s local %s peer %s", tunName, ip, peer)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	}

	sargs = fmt.Sprintf("link set %s down", tunName)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	}

	sargs = fmt.Sprintf("tuntap del mode tun %s", tunName)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return errors.New(fmt.Sprintf("ip %v err:%v", sargs, err))
	}
	return nil
}

func ParseCIDR(subnetIP global.Address, subnetMask uint8) (net.IP, *net.IPNet, error) {
	ipStr := fmt.Sprintf("%d.%d.%d.%d/%d", subnetIP[0], subnetIP[1], subnetIP[2], subnetIP[3], subnetMask)
	return net.ParseCIDR(ipStr)
}
