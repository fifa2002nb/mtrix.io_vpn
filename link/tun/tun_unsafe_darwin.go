// Copyright 2016 The Netstack Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package tun

import (
	"fmt"
	"syscall"
)

// Open opens the specified TUN device, sets it to non-blocking mode, and
// returns its file descriptor.
func Open(name string) (int, error) {
	devPath := fmt.Sprintf("/dev/%s", name)
	fd, err := syscall.Open(devPath, syscall.O_RDWR, 0)
	if err != nil {
		return -1, err
	}
	return fd, nil
}

func Close(fd int) {
	if 0 >= fd {
		return
	}
	syscall.Close(fd)
}
