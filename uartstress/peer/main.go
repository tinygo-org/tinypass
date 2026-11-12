//go:build tinygo

// Peer firmware for the UART stress test. Runs on any board with a spare UART,
// wired to the board under test (TX/RX crossed, common ground). It uses
// machine.DefaultUART, so any board with a spare UART can be the peer.
//
// Commands, all multi-byte values little endian:
//
//	'P'                  reply 'K'
//	'B' baud32           reply 'K' at the old rate, then switch baud rate
//	'E' n32              echo the next n bytes as they arrive
//	'G' n32 seed         send n pattern bytes at full rate
//	'C' n32              swallow n bytes, reply with sum32 and count32
//
// A command that stalls for a while is abandoned.
package main

import (
	"machine"
	"time"
)

// Spin count before a stalled command is abandoned, roughly 100 ms.
const idleSpins = 40000

var uart = machine.DefaultUART

func pattern(i uint32, seed byte) byte {
	return byte(i*167+(i>>8)*13) + seed
}

func readByte() (byte, bool) {
	for i := 0; i < idleSpins; i++ {
		if b, err := uart.ReadByte(); err == nil {
			return b, true
		}
	}
	return 0, false
}

func readU32() (uint32, bool) {
	var v uint32
	for i := 0; i < 4; i++ {
		b, ok := readByte()
		if !ok {
			return 0, false
		}
		v |= uint32(b) << (8 * i)
	}
	return v, true
}

func main() {
	uart.Configure(machine.UARTConfig{BaudRate: 115200})
	for {
		cmd, err := uart.ReadByte()
		if err != nil {
			continue
		}
		switch cmd {
		case 'P':
			uart.WriteByte('K')
		case 'B':
			baud, ok := readU32()
			if !ok || baud == 0 {
				break
			}
			uart.WriteByte('K')
			time.Sleep(2 * time.Millisecond)
			uart.Configure(machine.UARTConfig{BaudRate: baud})
		case 'E':
			n, ok := readU32()
			if !ok {
				break
			}
			for i := uint32(0); i < n; i++ {
				b, ok := readByte()
				if !ok {
					break
				}
				uart.WriteByte(b)
			}
		case 'C':
			n, ok := readU32()
			if !ok {
				break
			}
			var sum, got uint32
			for got < n {
				b, ok := readByte()
				if !ok {
					break
				}
				sum += uint32(b)
				got++
			}
			for _, v := range [2]uint32{sum, got} {
				for i := 0; i < 4; i++ {
					uart.WriteByte(byte(v >> (8 * i)))
				}
			}
		case 'G':
			n, ok := readU32()
			if !ok {
				break
			}
			seed, ok := readByte()
			if !ok {
				break
			}
			for i := uint32(0); i < n; i++ {
				uart.WriteByte(pattern(i, seed))
			}
		}
	}
}
