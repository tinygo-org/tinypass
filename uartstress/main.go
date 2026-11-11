// Command uartstress is the host side of the UART stress test. It reads
// results from the USB serial port of the device under test.
//
//	go run ./uartstress -port <dut-usb-port> [-timeout 5m] [-soak 60s]
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.bug.st/serial"
)

func main() {
	portName := flag.String("port", "", "USB serial port of the device under test")
	timeout := flag.Duration("timeout", 5*time.Minute, "give up after this long")
	soak := flag.Duration("soak", 0, "run a soak of this length instead of the full suite")
	flag.Parse()
	if *portName == "" {
		flag.Usage()
		os.Exit(2)
	}

	port, err := serial.Open(*portName, &serial.Mode{BaudRate: 115200})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer port.Close()
	port.ResetInputBuffer()

	cmd := []byte{'g'}
	if *soak > 0 {
		n := int(*soak / (5 * time.Second))
		cmd = []byte{'s', byte(min(max(n, 1), 255))}
	}
	if _, err := port.Write(cmd); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	lines := make(chan string)
	go func() {
		sc := bufio.NewScanner(port)
		for sc.Scan() {
			lines <- strings.TrimSpace(sc.Text())
		}
		close(lines)
	}()

	deadline := time.After(*timeout)
	quiet := time.NewTimer(10 * time.Second)
	fails := 0
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				fmt.Println("port closed")
				os.Exit(2)
			}
			quiet.Reset(10 * time.Second)
			if l == "" || strings.HasPrefix(l, "# progress") {
				continue
			}
			fmt.Println(l)
			if strings.HasPrefix(l, "FAIL") {
				fails++
			}
			if l == "DONE" {
				if fails > 0 {
					os.Exit(1)
				}
				return
			}
		case <-quiet.C:
			fmt.Println("# no output for 10s")
			quiet.Reset(10 * time.Second)
		case <-deadline:
			fmt.Println("TIMEOUT")
			os.Exit(2)
		}
	}
}
