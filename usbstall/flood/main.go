//go:build tinygo

// Floods the USB console with println and sends 'L' over the UART about
// every 10 ms. Pair it with ../monitor on the peer. The host sees the L bytes
// only while the main loop runs.
//
// The host controls the loop with single byte commands over USB.
//
//	'F' / 'N'   call Flush with every L / stop doing that
//	'Q' / 'U'   stop / resume the flood lines
//	'P'         print "pong"
//	'Z'         stop reading USB for 3 seconds
//	'B' n       write 100 blocks of n bytes, block j is byte(j+k) at index k
//	'K' n32     swallow n bytes and print "sum <count> <sum>"
//	'E' n32     echo the next n bytes
//	'W'         write lines from three goroutines, print "writers done"
//
// n32 is a little endian 32 bit length. Other bytes are ignored.
package main

import (
	"machine"
	"time"
)

const pattern = "0123456789abcdefghijklmnopqrstuvwxyz"

var usb = machine.Serial

func writers() {
	done := make(chan struct{})
	for _, c := range []byte("ABC") {
		go func() {
			line := make([]byte, 41)
			for i := range line {
				line[i] = c
			}
			line[40] = '\n'
			for i := 0; i < 2000; i++ {
				usb.Write(line)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 3; i++ {
		<-done
	}
	println("writers done")
}

func blocks(n int) {
	buf := make([]byte, n)
	for j := 0; j < 100; j++ {
		for k := range buf {
			buf[k] = byte(j + k)
		}
		usb.Write(buf)
	}
}

func main() {
	uart := machine.DefaultUART
	uart.Configure(machine.UARTConfig{BaudRate: 115200})
	flusher, _ := any(usb).(interface{ Flush() })

	var (
		flush, quiet bool
		deaf, lastL  time.Time
		state        int // 0 command, 1 length, 2 payload, 3 block size
		cmd          byte
		nlen         int
		length       uint32
		count, sum   uint32
		echo         []byte
	)
	for i := 0; ; i++ {
		if !quiet {
			println("flood line", i, pattern)
		}
		now := time.Now()
		if now.Sub(lastL) > 10*time.Millisecond {
			lastL = now
			if flush && flusher != nil {
				flusher.Flush()
			}
			uart.WriteByte('L')
		}
		if now.Before(deaf) {
			continue
		}
		echo = echo[:0]
		for {
			b, err := usb.ReadByte()
			if err != nil {
				break
			}
			switch state {
			case 0:
				switch b {
				case 'F':
					flush = true
				case 'N':
					flush = false
				case 'Q':
					quiet = true
				case 'U':
					quiet = false
				case 'P':
					println("pong")
				case 'Z':
					deaf = time.Now().Add(3 * time.Second)
				case 'W':
					writers()
				case 'B':
					state = 3
				case 'K', 'E':
					cmd, state, nlen, length = b, 1, 0, 0
				}
			case 3:
				blocks(int(b))
				state = 0
			case 1:
				length |= uint32(b) << (8 * nlen)
				nlen++
				if nlen == 4 {
					count, sum = 0, 0
					state = 2
					if length == 0 {
						state = 0
					}
				}
			case 2:
				count++
				sum += uint32(b)
				if cmd == 'E' {
					echo = append(echo, b)
				}
				length--
				if length == 0 {
					state = 0
					if cmd == 'K' {
						println("sum", count, sum)
					}
				}
			}
			if deaf.After(now) {
				break
			}
		}
		if len(echo) > 0 {
			usb.Write(echo)
		}
	}
}
