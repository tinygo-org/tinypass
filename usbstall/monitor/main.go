//go:build tinygo

// Sends every byte received on the UART back out of it. On a board with a
// USB bridge on that UART, such as an Arduino Uno R3, the bytes show up on
// the host.
package main

import "machine"

func main() {
	uart := machine.DefaultUART
	uart.Configure(machine.UARTConfig{BaudRate: 115200})
	for {
		if b, err := uart.ReadByte(); err == nil {
			uart.WriteByte(b)
		}
	}
}
