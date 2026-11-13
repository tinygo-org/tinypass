//go:build tinygo && !arduino_uno_r4_wifi && !arduino_nano_r4

package main

import "machine"

type reader interface{ ReadByte() (byte, error) }
type writer interface{ WriteByte(byte) error }

// ports returns the port that receives from the DUT and the port that sends
// to the host. Add a file like link_<board>.go with the board build tag when
// they are different ports.
func ports() (reader, writer) {
	return machine.DefaultUART, machine.DefaultUART
}
