# mtrix.io_vpn
在google的netstack基础上开发了一个梯子程序，原理是通过tun虚拟网卡将包转发到用户空间，然后client使用udp+混淆做端到端的并发发送，server端收包解包后通过tun虚拟网卡交给内核处理。

用到了netstack的TCP/IP栈+拥塞控制机制，保证用户空间数据包收发的稳定快速可靠。

## 怎么玩，涉及的几个程序
1、mktun.sh脚本负责添加子网路由和卸载子网路由

2、example/tun_tcp_listen部署在服务端

3、example/tun_tcp_connect部署在客户端
