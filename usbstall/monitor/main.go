//go:build tinygo

// Sends every byte received on the UART back out of it. On a board with a
// USB bridge on that UART, such as an Arduino Uno R3, the bytes show up on
// the host. See link.go for boards where the two differ.
package main

import "machine"

func main() {
	in, out := ports()
	inUART, _ := in.(*machine.UART)
	if inUART != nil {
		inUART.Configure(machine.UARTConfig{BaudRate: 115200})
	}
	if outUART, ok := out.(*machine.UART); ok && outUART != inUART {
		outUART.Configure(machine.UARTConfig{BaudRate: 115200})
	}
	for {
		if b, err := in.ReadByte(); err == nil {
			out.WriteByte(b)
		}
	}
}
