package base

import (
	"github.com/codegangsta/cli"
)

var (
	FlConf = cli.StringFlag{
		Name:  "configure, Y",
		Value: "./conf.ini",
		Usage: "configure file path",
	}
)
