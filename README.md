# mtrix.io_vpn
在google的netstack基础上开发了一个梯子程序，原理是通过tun虚拟网卡将IP包转发到用户空间TCP/IP协议栈包成tcp包，client将tcp包再用udp+混淆包装后做端到端的并发发送，server端收udp包解包后交给netstack解包成IP通过tun虚拟网卡交给内核协议栈处理。

用到了netstack的协议栈+拥塞控制机制，保证用户空间数据包收发的稳定快速可靠。

## 怎么玩，涉及的几个程序
1、mktun.sh脚本负责添加子网路由和卸载子网路由

2、example/tun_tcp_listen部署在服务端

3、example/tun_tcp_connect部署在客户端

4、scripts/chnroute-up_linux.sh mac和linux环境下配置需要代理的ip端
