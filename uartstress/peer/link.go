//go:build tinygo && !arduino_uno_r4_wifi

package main

import "machine"

// The UART that connects to the DUT. Add a file like link_<board>.go with
// the board build tag for boards where machine.DefaultUART is not wired out.
var link = machine.DefaultUART
