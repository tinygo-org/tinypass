// Command usbstall checks that a board keeps running when the USB host stops
// reading or closes the port. It talks to two serial ports. The device under
// test runs flood/ and prints without end. The peer runs monitor/ and shows
// on its own USB port whether the device main loop still runs.
//
//	go run ./usbstall -dut <dut-port> -peer <peer-port> [-stall 60s]
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"go.bug.st/serial"
)

var (
	dutName  = flag.String("dut", "", "USB serial port of the device under test")
	peerName = flag.String("peer", "", "USB serial port of the peer")
	stall    = flag.Duration("stall", time.Minute, "how long the host stops reading")
	which    = flag.String("scenario", "all", "all, resume, close or touch")
	peer     serial.Port
	fails    int
)

func open(name string, baud int) serial.Port {
	p, err := serial.Open(name, &serial.Mode{BaudRate: baud})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	p.SetReadTimeout(50 * time.Millisecond)
	return p
}

// count reads from p for d and returns the bytes seen and how many were c.
func count(p serial.Port, d time.Duration, c byte) (total, match int) {
	buf := make([]byte, 65536)
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		n, _ := p.Read(buf)
		total += n
		for _, b := range buf[:n] {
			if b == c {
				match++
			}
		}
	}
	return
}

// alive reports whether the main loop of the device ran during d.
func alive(d time.Duration) bool {
	peer.ResetInputBuffer()
	_, l := count(peer, d, 'L')
	return l > 0
}

func result(name string, ok bool, detail string) {
	s := "PASS"
	if !ok {
		s = "FAIL"
		fails++
	}
	fmt.Println(s, name, detail)
}

// stallFor leaves the port unread until the device blocks or the time is up.
// It reports whether the device blocked.
func stallFor() bool {
	blocked := false
	for t := time.Duration(0); t < *stall; t += 10 * time.Second {
		blocked = !alive(10 * time.Second)
		state := "running"
		if blocked {
			state = "stopped"
		}
		fmt.Printf("# not reading %v, main loop %s\n", t+10*time.Second, state)
	}
	return blocked
}

func resume() {
	dut := open(*dutName, 115200)
	defer dut.Close()
	count(dut, 2*time.Second, 0)
	blocked := stallFor()
	n, _ := count(dut, 4*time.Second, 0)
	result("resume-data", n > 0, fmt.Sprintf("bytes=%d", n))
	result("resume-running", alive(3*time.Second), "")
	if !blocked {
		fmt.Println("# stall not reached, use a longer -stall")
	}
}

func closePort() {
	dut := open(*dutName, 115200)
	blocked := stallFor()
	dut.Close()
	time.Sleep(2 * time.Second)
	result("close-releases", alive(3*time.Second), "")
	dut = open(*dutName, 115200)
	defer dut.Close()
	n, _ := count(dut, 4*time.Second, 0)
	result("reopen-data", n > 0, fmt.Sprintf("bytes=%d", n))
	result("reopen-running", alive(3*time.Second), "")
	if !blocked {
		fmt.Println("# stall not reached, use a longer -stall")
	}
}

// touch opens the port at 1200 baud and closes it. The device should leave
// its serial port, which means it entered its bootloader. This leaves the
// device in the bootloader.
func touch() {
	dut := open(*dutName, 115200)
	stallFor()
	dut.Close()
	t := open(*dutName, 1200)
	time.Sleep(300 * time.Millisecond)
	t.Close()
	gone := false
	for i := 0; i < 20 && !gone; i++ {
		time.Sleep(500 * time.Millisecond)
		_, err := os.Stat(*dutName)
		gone = err != nil
	}
	result("touch-bootloader", gone, "")
}

func main() {
	flag.Parse()
	if *dutName == "" || *peerName == "" {
		flag.Usage()
		os.Exit(2)
	}
	peer = open(*peerName, 115200)
	defer peer.Close()
	time.Sleep(2 * time.Second) // an Arduino resets when the port opens

	switch *which {
	case "all":
		resume()
		closePort()
		touch()
	case "resume":
		resume()
	case "close":
		closePort()
	case "touch":
		touch()
	default:
		flag.Usage()
		os.Exit(2)
	}
	if fails > 0 {
		os.Exit(1)
	}
}
