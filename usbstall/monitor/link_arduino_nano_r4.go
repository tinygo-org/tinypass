//go:build tinygo && arduino_nano_r4

package main

import "machine"

type reader interface{ ReadByte() (byte, error) }
type writer interface{ WriteByte(byte) error }

// DefaultUART is on the D0 and D1 pins. The USB console goes to the host.
func ports() (reader, writer) {
	return machine.DefaultUART, machine.Serial
}
