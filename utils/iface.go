package utils

import (
	"errors"
	"fmt"
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
func SetTunIP(tunName string, ip net.IP, subnet *net.IPNet) error {
	ip = ip.To4()
	if ip[3]%2 == 0 {
		return errors.New("Invalid ip address.")
	}
	peer := net.IP(make([]byte, 4))
	copy([]byte(peer), []byte(ip))
	peer[3]++

	sargs := fmt.Sprintf("ip tuntap add mode tun %s", tunName)
	args := strings.Split(sargs, " ")
	cmd := exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return err
	}

	sargs = fmt.Sprintf("link set dev %s up mtu 1500 qlen 100", tunName)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return err
	}

	sargs = fmt.Sprintf("addr add dev %s local %s peer %s", tunName, ip, peer)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return err
	}

	sargs = fmt.Sprintf("route add %s via %s dev %s", subnet, peer, tunName)
	args = strings.Split(sargs, " ")
	cmd = exec.Command("ip", args...)
	if err := cmd.Run(); nil != err {
		return err
	}
	return nil
}
