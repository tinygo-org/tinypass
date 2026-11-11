//go:build tinygo

// Floods the USB console with println and sends 'L' over the UART every 256
// lines. Pair it with ../monitor on the peer. The host sees the L bytes only
// while the main loop runs.
package main

import "machine"

func main() {
	uart := machine.DefaultUART
	uart.Configure(machine.UARTConfig{BaudRate: 115200})
	for i := 0; ; i++ {
		println("flood line", i, "0123456789abcdefghijklmnopqrstuvwxyz")
		if i%256 == 0 {
			uart.WriteByte('L')
		}
	}
}
