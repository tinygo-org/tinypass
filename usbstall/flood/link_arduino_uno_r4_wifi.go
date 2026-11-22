//go:build tinygo && arduino_uno_r4_wifi

package main

import "machine"

// DefaultUART is bridged to USB. UART2 is on the D0 and D1 pins.
var link = machine.UART2
