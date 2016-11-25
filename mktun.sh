#!/bin/sh
# only for server-end configuration
flag=$1

if [ -z ${flag} ];then
    echo "plaase specific command type."
    exit 2
fi

if [ 0 -eq ${flag} ];then
    `ip tuntap add mode tun tun0` && \
    `ip link set dev tun0 up mtu 1500 qlen 100` && \
    `ip addr add dev tun0 local 10.1.1.1 peer 10.1.1.2` && \
    `ip route add 10.1.1.0/24 via 10.1.1.2 dev tun0` && \
    `sysctl net.ipv4.ip_forward=1 > /dev/null` && \
    `iptables -t nat -A POSTROUTING -j MASQUERADE`
    exit 0
elif [ 1 -eq ${flag} ];then
    `ip route del 10.1.1.0/24 via 10.1.1.2 dev tun0` && \
    `ip addr del dev tun0 local 10.1.1.1 peer 10.1.1.2`
    `ip link set tun0 down` && \
    `ip tuntap del mode tun tun0` && \
    `sysctl net.ipv4.ip_forward=0 > /dev/null` && \
    `iptables -t nat -D POSTROUTING -j MASQUERADE`
    exit 0
else
    echo "unknown command type."
    exit 1
fi
