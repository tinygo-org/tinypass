//go:build tinygo && arduino_uno_r4_wifi

package main

import "machine"

type reader interface{ ReadByte() (byte, error) }
type writer interface{ WriteByte(byte) error }

// UART2 is on the D0 and D1 pins. DefaultUART is bridged to USB.
func ports() (reader, writer) {
	return machine.UART2, machine.DefaultUART
}
