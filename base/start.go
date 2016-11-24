package base

import (
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"github.com/codegangsta/cli"
	"github.com/widuu/goini"
	"os"
	"strconv"
)

var (
	mode      string
	portStart int
	portEnd   int
	key       string
	// only for server
	addr        string
	peertimeout int
	// for client
	server            string
	heartbeatInterval int
)

func Start(c *cli.Context) {
	if err := confParser(c); nil != err {
		log.Fatalf("%v", err)
		os.Exit(9)
	}
	if "server" == mode {
		// code here
	} else if "client" == mode {
		// code here
	} else {
		log.Fatal("unknown mode.")
		os.Exit(8)
	}
}

func confParser(c *cli.Context) error {
	var err error
	if c.IsSet("configure") || c.IsSet("C") {
		var conf *goini.Config
		if c.IsSet("configure") {
			conf = goini.SetConfig(c.String("configure"))
		} else {
			conf = goini.SetConfig(c.String("C"))
		}

		mode = conf.GetValue("main", "mode")
		portS := conf.GetValue("main", "port_start")
		if portStart, err = strconv.Atoi(portS); nil != err {
			return errors.New(fmt.Sprintf("port_start is required to start a vpn job. err:%v", err))
		}

		portE := conf.GetValue("main", "port_end")
		if portEnd, err = strconv.Atoi(portE); nil != err {
			return errors.New(fmt.Sprintf("port_end is required to start a vpn job. err:%v", err))
		}

		key = conf.GetValue("main", "key")
		if "" == key {
			return errors.New(fmt.Sprintf("key is required to start a vpn job."))
		}

		addr = conf.GetValue("server", "addr")
		if "" == key {
			return errors.New(fmt.Sprintf("addr is required to start a vpn job."))
		}

		peert := conf.GetValue("server", "peer_timeout")
		if peertimeout, err = strconv.Atoi(peert); nil != err {
			return errors.New(fmt.Sprintf("peer_timeout is required to start a vpn job. err:%v", err))
		}

		server = conf.GetValue("client", "server")
		if "" == server {
			return errors.New(fmt.Sprintf("server is required to start a vpn job."))
		}

		heartbeatI := conf.GetValue("client", "heartbeat_interval")
		if heartbeatInterval, err = strconv.Atoi(heartbeatI); nil != err {
			return errors.New(fmt.Sprintf("heartbeat_interval is required to start a vpn job. err:%v", err))
		}
	} else {
		return errors.New(fmt.Sprintf("configure is required to run a job. See '%s start --help'.", c.App.Name))
	}

	return nil
}