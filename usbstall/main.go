// Command usbstall checks that a board keeps running and sends and receives
// clean data when the USB host misbehaves. It talks to two serial ports. The
// device under test runs flood/ and prints without end. The peer runs
// monitor/ and shows on its own USB port whether the device main loop runs.
//
//	go run ./usbstall -dut <dut-port> -peer <peer-port> [-stall 1m]
package main

import (
	"bytes"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"
)

var (
	dutName  = flag.String("dut", "", "USB serial port of the device under test")
	peerName = flag.String("peer", "", "USB serial port of the peer")
	stall    = flag.Duration("stall", time.Minute, "how long the host stops reading")
	which    = flag.String("scenario", "all", "all, "+strings.Join(names(), ", "))
	cycles   = flag.Int("cycles", 100, "port open/close and DTR toggle cycles")
	minRate  = flag.Int("minrate", 0, "fail the throughput check below this many bytes per second")
	peer     serial.Port
	fails    int
)

var scenarios = []struct {
	name string
	run  func()
}{
	{"resume", resume},
	{"close", closePort},
	{"flush", flush},
	{"cycle", cycle},
	{"control", control},
	{"rxflood", rxFlood},
	{"boundary", boundary},
	{"echo", echo},
	{"writers", writers},
	{"throughput", throughput},
	{"touch", touch},
}

func names() (n []string) {
	for _, s := range scenarios {
		n = append(n, s.name)
	}
	return
}

func open(name string, baud int) serial.Port {
	p, err := serial.Open(name, &serial.Mode{BaudRate: baud})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	p.SetReadTimeout(50 * time.Millisecond)
	return p
}

func result(name string, ok bool, detail string) {
	s := "PASS"
	if !ok {
		s = "FAIL"
		fails++
	}
	fmt.Println(s, name, detail)
}

// read collects what p sends for d.
func read(p serial.Port, d time.Duration) []byte {
	var data []byte
	buf := make([]byte, 65536)
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		n, _ := p.Read(buf)
		data = append(data, buf[:n]...)
	}
	return data
}

// readLine reads until a line that starts with prefix is complete. It returns
// the bytes before the prefix and the line.
func readLine(p serial.Port, prefix string, d time.Duration) (before []byte, line string, ok bool) {
	var data []byte
	buf := make([]byte, 65536)
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		n, _ := p.Read(buf)
		data = append(data, buf[:n]...)
		i := bytes.Index(data, []byte(prefix))
		if i < 0 {
			continue
		}
		if j := bytes.IndexByte(data[i:], '\n'); j >= 0 {
			return data[:i], strings.TrimSpace(string(data[i : i+j])), true
		}
	}
	return nil, "", false
}

// alive reports whether the main loop of the device ran during d.
func alive(d time.Duration) bool {
	peer.ResetInputBuffer()
	return bytes.IndexByte(read(peer, d), 'L') >= 0
}

// quiet stops the flood lines and waits until everything before that is read.
func quiet(p serial.Port) bool {
	p.Write([]byte{'Q', 'P'})
	_, _, ok := readLine(p, "pong", 10*time.Second)
	return ok
}

func unquiet(p serial.Port) {
	p.Write([]byte{'U'})
}

func le32(n int) []byte {
	return []byte{byte(n), byte(n >> 8), byte(n >> 16), byte(n >> 24)}
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

func note(blocked bool) {
	if !blocked {
		fmt.Println("# stall not reached, use a longer -stall")
	}
}

func resume() {
	dut := open(*dutName, 115200)
	defer dut.Close()
	read(dut, 2*time.Second)
	blocked := stallFor()
	result("resume-data", len(read(dut, 4*time.Second)) > 0, "")
	result("resume-running", alive(3*time.Second), "")
	note(blocked)
}

func closePort() {
	dut := open(*dutName, 115200)
	blocked := stallFor()
	dut.Close()
	time.Sleep(2 * time.Second)
	result("close-releases", alive(3*time.Second), "")
	dut = open(*dutName, 115200)
	defer dut.Close()
	result("reopen-data", len(read(dut, 4*time.Second)) > 0, "")
	result("reopen-running", alive(3*time.Second), "")
	note(blocked)
}

// flush asks the device to call Flush in its loop, then closes the port during
// a stall. Flush must not wait for a host that is gone.
func flush() {
	dut := open(*dutName, 115200)
	dut.Write([]byte{'F'})
	blocked := stallFor()
	dut.Close()
	time.Sleep(2 * time.Second)
	result("flush-releases", alive(3*time.Second), "")
	dut = open(*dutName, 115200)
	dut.Write([]byte{'N'})
	dut.Close()
	note(blocked)
}

var flood = regexp.MustCompile(`^flood line (\d+) 0123456789abcdefghijklmnopqrstuvwxyz$`)

// floodLines checks that the complete lines in data are intact and that their
// numbers increase. The first line may be cut short.
func floodLines(data []byte) (n, bad int) {
	prev := -1
	all := strings.Split(string(data), "\n")
	for i, line := range all {
		if i == 0 || i == len(all)-1 {
			continue
		}
		m := flood.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if m == nil {
			bad++
			continue
		}
		v, _ := strconv.Atoi(m[1])
		if v <= prev {
			bad++
		}
		prev = v
		n++
	}
	return
}

func lines(p serial.Port, d time.Duration) (n, bad int) {
	return floodLines(read(p, d))
}

// cycle opens and closes the port and toggles DTR many times while the
// device floods, then checks that the device still runs and sends clean data.
func cycle() {
	for i := 0; i < *cycles; i++ {
		if i%25 == 0 {
			fmt.Printf("# open/close %d/%d\n", i, *cycles)
		}
		dut := open(*dutName, 115200)
		time.Sleep(time.Duration(i%7) * 10 * time.Millisecond)
		dut.Close()
	}
	result("cycle-open-close-running", alive(2*time.Second), "")
	dut := open(*dutName, 115200)
	defer dut.Close()
	n, bad := lines(dut, 2*time.Second)
	result("cycle-open-close-data", n > 0 && bad == 0, fmt.Sprintf("lines=%d bad=%d", n, bad))
	for i := 0; i < *cycles*2; i++ {
		if i%50 == 0 {
			fmt.Printf("# dtr toggle %d/%d\n", i, *cycles*2)
		}
		dut.SetDTR(i%2 == 1)
		time.Sleep(time.Duration(i%5) * 5 * time.Millisecond)
	}
	dut.SetDTR(true)
	n, bad = lines(dut, 2*time.Second)
	result("cycle-dtr-data", n > 0 && bad == 0, fmt.Sprintf("lines=%d bad=%d", n, bad))
	result("cycle-dtr-running", alive(2*time.Second), "")
}

// control changes the line coding and DTR many times while the device floods.
// A baud rate of 1200 with DTR low would reset some boards, so it is not used.
func control() {
	dut := open(*dutName, 115200)
	defer dut.Close()
	rates := []int{9600, 19200, 57600, 115200, 230400, 460800, 921600}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < *cycles*3; i++ {
		if i%50 == 0 {
			fmt.Printf("# control %d/%d\n", i, *cycles*3)
		}
		dut.SetMode(&serial.Mode{BaudRate: rates[rng.Intn(len(rates))]})
		if i%3 == 0 {
			dut.SetDTR(rng.Intn(2) == 0)
		}
		time.Sleep(time.Duration(rng.Intn(10)) * time.Millisecond)
	}
	dut.SetMode(&serial.Mode{BaudRate: 115200})
	dut.SetDTR(true)
	result("control-running", alive(2*time.Second), "")
	n, bad := lines(dut, 2*time.Second)
	result("control-data", n > 0 && bad == 0, fmt.Sprintf("lines=%d bad=%d", n, bad))
}

// rxFlood sends megabytes to the device while it does not read. The device
// may drop data but must keep running and answer afterwards.
func rxFlood() {
	dut := open(*dutName, 115200)
	defer dut.Close()
	if !quiet(dut) {
		result("rxflood", false, "no answer from the device")
		return
	}
	defer unquiet(dut)
	dut.Write([]byte{'Z'})
	junk := bytes.Repeat([]byte{'z'}, 4096)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 512; i++ {
			dut.Write(junk)
		}
		close(done)
	}()
	var finished bool
	select {
	case <-done:
		finished = true
	case <-time.After(30 * time.Second):
	}
	result("rxflood-write-completes", finished, "2 MiB")
	answered := false
	for i := 0; i < 20 && !answered; i++ {
		dut.Write([]byte{'P'})
		_, _, answered = readLine(dut, "pong", 500*time.Millisecond)
	}
	result("rxflood-answers", answered, "")
	result("rxflood-running", alive(2*time.Second), "")
}

// boundary checks transfers of sizes around the USB packet size in both
// directions.
func boundary() {
	dut := open(*dutName, 115200)
	defer dut.Close()
	if !quiet(dut) {
		result("boundary", false, "no answer from the device")
		return
	}
	defer unquiet(dut)
	rng := rand.New(rand.NewSource(2))
	for _, n := range []int{1, 63, 64, 65, 127, 128, 129, 191, 192, 193, 255} {
		dut.Write([]byte{'B', byte(n), 'P'})
		before, _, ok := readLine(dut, "pong", 10*time.Second)
		want := make([]byte, 0, 100*n)
		for j := 0; j < 100; j++ {
			for k := 0; k < n; k++ {
				want = append(want, byte(j+k))
			}
		}
		result(fmt.Sprintf("boundary-tx-%d", n), ok && bytes.Equal(before, want), fmt.Sprintf("got=%d want=%d", len(before), len(want)))
	}
	for _, n := range []int{1, 63, 64, 65, 127, 128, 129, 1024} {
		payload := make([]byte, 64*1024)
		var sum int
		for i := range payload {
			payload[i] = byte(rng.Intn(256))
			sum += int(payload[i])
		}
		dut.Write(append([]byte{'K'}, le32(len(payload))...))
		for off := 0; off < len(payload); off += n {
			dut.Write(payload[off:min(off+n, len(payload))])
		}
		_, line, ok := readLine(dut, "sum", 10*time.Second)
		result(fmt.Sprintf("boundary-rx-%d", n), ok && line == fmt.Sprintf("sum %d %d", len(payload), sum), line)
	}
}

// echo sends random data in odd sized writes while reading the echo.
func echo() {
	dut := open(*dutName, 115200)
	defer dut.Close()
	if !quiet(dut) {
		result("echo", false, "no answer from the device")
		return
	}
	defer unquiet(dut)
	const total = 256 * 1024
	rng := rand.New(rand.NewSource(3))
	data := make([]byte, total)
	rng.Read(data)
	got := make(chan []byte)
	go func() {
		var all []byte
		buf := make([]byte, 65536)
		end := time.Now().Add(60 * time.Second)
		for len(all) < total && time.Now().Before(end) {
			n, _ := dut.Read(buf)
			all = append(all, buf[:n]...)
		}
		got <- all
	}()
	dut.Write(append([]byte{'E'}, le32(total)...))
	for off := 0; off < total; {
		n := 1 + rng.Intn(300)
		n = min(n, total-off)
		dut.Write(data[off : off+n])
		off += n
	}
	all := <-got
	first := 0
	for first < len(all) && first < total && all[first] == data[first] {
		first++
	}
	result("echo-integrity", bytes.Equal(all, data), fmt.Sprintf("got=%d want=%d first difference=%d", len(all), total, first))
}

// writers has three goroutines on the device write at once. Every byte must
// arrive, although lines may interleave.
func writers() {
	dut := open(*dutName, 115200)
	defer dut.Close()
	if !quiet(dut) {
		result("writers", false, "no answer from the device")
		return
	}
	defer unquiet(dut)
	dut.Write([]byte{'W'})
	before, _, ok := readLine(dut, "writers done", 60*time.Second)
	counts := [3]int{}
	for _, c := range before {
		if c >= 'A' && c <= 'C' {
			counts[c-'A']++
		}
	}
	want := 2000 * 40
	result("writers-bytes", ok && counts == [3]int{want, want, want}, fmt.Sprintf("A=%d B=%d C=%d want=%d", counts[0], counts[1], counts[2], want))
}

// throughput measures how fast the device sends. It only fails below -minrate.
func throughput() {
	dut := open(*dutName, 115200)
	defer dut.Close()
	read(dut, time.Second)
	rate := len(read(dut, 5*time.Second)) / 5
	result("throughput", rate > 0 && rate >= *minRate, fmt.Sprintf("bytes/s=%d", rate))
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

	ran := false
	for _, s := range scenarios {
		if *which == "all" || *which == s.name {
			fmt.Println("RUN", s.name)
			start := time.Now()
			s.run()
			fmt.Printf("END %s %v\n", s.name, time.Since(start).Round(time.Millisecond))
			ran = true
		}
	}
	if !ran {
		flag.Usage()
		os.Exit(2)
	}
	if fails > 0 {
		os.Exit(1)
	}
}
