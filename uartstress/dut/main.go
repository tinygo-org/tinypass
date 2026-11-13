//go:build tinygo

// Device under test for the UART stress test. Reports over USB serial
// and talks to peer/ over the UART in link.go. Send 'g' over USB to run all
// tests, or 's' plus one byte N for a soak of N*5 seconds.
package main

import (
	"machine"
	"runtime"
	"runtime/interrupt"
	"runtime/volatile"
	"time"
)

// uart is the link to the peer, see link.go. usb is the host control channel.
// Output from println goes to the console, which must not be uart.
var (
	uart            = link
	usb  byteReader = machine.Serial
)

type byteReader interface {
	ReadByte() (byte, error)
}

func pattern(i uint32, seed byte) byte {
	return byte(i*167+(i>>8)*13) + seed
}

func put32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

func drain() {
	time.Sleep(20 * time.Millisecond)
	for {
		if _, err := uart.ReadByte(); err != nil {
			return
		}
	}
}

// readN reads n bytes into buf or gives up after timeout without progress.
func readN(buf []byte, timeout time.Duration) int {
	got := 0
	last := time.Now()
	for got < len(buf) {
		b, err := uart.ReadByte()
		if err != nil {
			if time.Since(last) > timeout {
				break
			}
			continue
		}
		buf[got] = b
		got++
		last = time.Now()
	}
	return got
}

func ping() bool {
	drain()
	uart.WriteByte('P')
	var b [1]byte
	return readN(b[:], 200*time.Millisecond) == 1 && b[0] == 'K'
}

func setBaud(baud uint32) bool {
	drain()
	cmd := []byte{'B', 0, 0, 0, 0}
	put32(cmd[1:], baud)
	uart.Write(cmd)
	var b [1]byte
	if readN(b[:], 200*time.Millisecond) != 1 || b[0] != 'K' {
		return false
	}
	time.Sleep(10 * time.Millisecond)
	uart.SetBaudRate(baud)
	time.Sleep(10 * time.Millisecond)
	return ping()
}

func result(name string, ok bool, detail string) bool {
	s := "PASS "
	if !ok {
		s = "FAIL "
	}
	println(s + name + " " + detail)
	return ok
}

var spin volatile.Register32

const spinCount = 1_000_000

func rnd(s *uint32) byte {
	x := *s
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*s = x
	return byte(x >> 11)
}

// recoverBauds also lists rates a peer may be left at after a failure.
// recoverPeer tries extraBauds as well.
var (
	bauds        = []uint32{9600, 57600, 115200, 250000, 500000}
	recoverBauds = []uint32{115200, 9600, 57600, 250000, 500000, 222222, 230400}
	altBaud      = uint32(250000)
)

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [12]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// recover finds the peer at whatever baud rate it is at and returns to 115200.
func recoverPeer() bool {
	for _, list := range [][]uint32{recoverBauds, extraBauds} {
		for _, b := range list {
			uart.SetBaudRate(b)
			time.Sleep(5 * time.Millisecond)
			if ping() {
				return setBaud(115200)
			}
		}
	}
	return false
}

func echoCmd(n uint32) {
	cmd := []byte{'E', 0, 0, 0, 0}
	put32(cmd[1:], n)
	uart.Write(cmd)
}

// echo sends total random bytes in chunks and checks the echo. Returns
// bytes missing and bytes wrong.
func echo(total, chunk int, seed uint32) (missing, wrong int) {
	drain()
	echoCmd(uint32(total))
	tx := make([]byte, chunk)
	rx := make([]byte, chunk)
	s := seed
	for sent := 0; sent < total; sent += chunk {
		n := chunk
		if total-sent < n {
			n = total - sent
		}
		for i := 0; i < n; i++ {
			tx[i] = rnd(&s)
		}
		uart.Write(tx[:n])
		got := readN(rx[:n], 200*time.Millisecond)
		missing += n - got
		for i := 0; i < got; i++ {
			if rx[i] != tx[i] {
				wrong++
			}
		}
		if got < n {
			return missing + total - sent - n, wrong
		}
	}
	return
}

func testPing() {
	ok := 0
	for i := 0; i < 200; i++ {
		if ping() {
			ok++
		}
	}
	result("ping-200", ok == 200, itoa(ok)+"/200")
}

func testBauds() {
	for _, b := range bauds {
		name := "baud-" + itoa(int(b))
		if !setBaud(b) {
			result(name, false, "switch failed")
			recoverPeer()
			continue
		}
		missing, wrong := echo(2000, 32, b)
		result(name, missing == 0 && wrong == 0, "missing="+itoa(missing)+" wrong="+itoa(wrong))
	}
	result("baud-restore", recoverPeer(), "")
}

func testEchoChunks() {
	for _, c := range []int{1, 7, 32, 100, 127} {
		missing, wrong := echo(5000, c, uint32(c)+1)
		result("echo-chunk-"+itoa(c), missing == 0 && wrong == 0, "missing="+itoa(missing)+" wrong="+itoa(wrong))
	}
}

func testDuplex() {
	drain()
	const total = 20000
	echoCmd(total)
	var s, sr uint32 = 99, 99
	got, wrong := 0, 0
	for i := 0; i < total; i++ {
		uart.WriteByte(rnd(&s))
		for {
			b, err := uart.ReadByte()
			if err != nil {
				break
			}
			if b != rnd(&sr) {
				wrong++
			}
			got++
		}
	}
	last := time.Now()
	for got < total && time.Since(last) < 200*time.Millisecond {
		b, err := uart.ReadByte()
		if err != nil {
			continue
		}
		last = time.Now()
		if b != rnd(&sr) {
			wrong++
		}
		got++
	}
	result("duplex-bytewise", got == total && wrong == 0, "got="+itoa(got)+" wrong="+itoa(wrong))
}

// stream asks the peer for n pattern bytes and verifies them.
func stream(n uint32, seed byte, noise bool) (got, bad int) {
	drain()
	cmd := []byte{'G', 0, 0, 0, 0, seed}
	put32(cmd[1:], n)
	uart.Write(cmd)
	last := time.Now()
	for uint32(got) < n && time.Since(last) < 300*time.Millisecond {
		b, err := uart.ReadByte()
		if err != nil {
			runtime.Gosched()
			continue
		}
		last = time.Now()
		if b != pattern(uint32(got), seed) {
			bad++
		}
		got++
		if noise && got%512 == 0 {
			println("# progress " + itoa(got))
		}
	}
	return
}

func testStreams() {
	for _, b := range []uint32{115200, 250000, 500000} {
		if !setBaud(b) {
			result("stream-"+itoa(int(b)), false, "switch failed")
			recoverPeer()
			continue
		}
		for _, noise := range []bool{false, true} {
			name := "stream-" + itoa(int(b))
			if noise {
				name += "-usb"
			}
			got, bad := stream(30000, byte(b), noise)
			result(name, got == 30000 && bad == 0, "got="+itoa(got)+" bad="+itoa(bad))
		}
	}
	recoverPeer()
}

func testBufferHold() {
	drain()
	uart.Write([]byte{'G', 100, 0, 0, 0, 7})
	time.Sleep(300 * time.Millisecond)
	n := uart.Buffered()
	ok := n == 100
	for i := 0; i < n; i++ {
		b, _ := uart.ReadByte()
		if b != pattern(uint32(i), 7) {
			ok = false
		}
	}
	result("buffer-100", ok, "buffered="+itoa(n))

	drain()
	uart.Write([]byte{'G', 44, 1, 0, 0, 9}) // 300 bytes
	time.Sleep(800 * time.Millisecond)
	n = uart.Buffered()
	ok = n >= 127 && n <= 128
	for i := 0; i < n; i++ {
		b, _ := uart.ReadByte()
		if b != pattern(uint32(i), 9) {
			ok = false
		}
	}
	result("buffer-overflow-keeps-prefix", ok, "buffered="+itoa(n))
	result("buffer-overflow-recovers", ping(), "")
}

func testFraming() {
	drain()
	uart.Write([]byte{'G', 200, 0, 0, 0, 3})
	uart.SetBaudRate(9600)
	time.Sleep(400 * time.Millisecond)
	uart.SetBaudRate(115200)
	time.Sleep(50 * time.Millisecond)
	drain()
	result("framing-recovers", ping(), "")
	got, bad := stream(2000, 5, false)
	result("framing-stream-after", got == 2000 && bad == 0, "got="+itoa(got)+" bad="+itoa(bad))
}

func testOverrun() {
	drain()
	uart.Write([]byte{'G', 100, 0, 0, 0, 11})
	// Timers need interrupts, so spin. Tune spinCount to cover about 100 ms.
	st := interrupt.Disable()
	for i := uint32(0); i < spinCount; i++ {
		spin.Set(i)
	}
	interrupt.Restore(st)
	time.Sleep(20 * time.Millisecond)
	n := uart.Buffered()
	prev := -1
	ok := true
	for i := 0; i < n; i++ {
		b, _ := uart.ReadByte()
		idx := -1
		for j := 0; j < 100; j++ {
			if pattern(uint32(j), 11) == b {
				idx = j
				break
			}
		}
		if idx <= prev {
			ok = false
		}
		prev = idx
	}
	result("overrun-no-corruption", ok, "buffered="+itoa(n))
	result("overrun-recovers", ping(), "")
	got, bad := stream(2000, 13, false)
	result("overrun-stream-after", got == 2000 && bad == 0, "got="+itoa(got)+" bad="+itoa(bad))
}

func testTxBlocks() {
	buf := make([]byte, 1024)
	for _, size := range []int{1, 64, 1000, 8192, 32768} {
		drain()
		cmd := []byte{'C', 0, 0, 0, 0}
		put32(cmd[1:], uint32(size))
		uart.Write(cmd)
		var sum uint32
		s := uint32(size)
		for sent := 0; sent < size; sent += len(buf) {
			n := len(buf)
			if size-sent < n {
				n = size - sent
			}
			for i := 0; i < n; i++ {
				buf[i] = rnd(&s)
				sum += uint32(buf[i])
			}
			uart.Write(buf[:n])
		}
		var r [8]byte
		got := readN(r[:], 500*time.Millisecond)
		rs := uint32(r[0]) | uint32(r[1])<<8 | uint32(r[2])<<16 | uint32(r[3])<<24
		rc := uint32(r[4]) | uint32(r[5])<<8 | uint32(r[6])<<16 | uint32(r[7])<<24
		result("tx-block-"+itoa(size), got == 8 && rs == sum && rc == uint32(size), "count="+itoa(int(rc)))
	}
}

func testBaudSwitch() {
	bad := 0
	for i := 0; i < 40; i++ {
		b := uint32(115200)
		if i%2 == 1 {
			b = altBaud
		}
		if !setBaud(b) {
			bad++
			recoverPeer()
			continue
		}
		if m, w := echo(32, 32, uint32(i)+1); m != 0 || w != 0 {
			bad++
		}
	}
	setBaud(115200)
	result("baud-switch-40", bad == 0, "bad="+itoa(bad))
}

func step(name string, f func()) {
	println("RUN " + name)
	t := time.Now()
	f()
	println("END " + name + " " + itoa(int(time.Since(t)/time.Millisecond)) + "ms")
}

func runAll() {
	if !ping() && !recoverPeer() {
		result("ping", false, "no peer")
		return
	}
	step("ping", testPing)
	step("bauds", testBauds)
	step("echochunks", testEchoChunks)
	step("duplex", testDuplex)
	step("streams", testStreams)
	step("bufferhold", testBufferHold)
	step("framing", testFraming)
	step("overrun", testOverrun)
	step("txblocks", testTxBlocks)
	step("baudswitch", testBaudSwitch)
	step("sleeppoll", testSleepPoll)
	step("reconfigure", testReconfigure)
	step("baudstress", testBaudStress)
	step("oddbauds", testOddBauds)
	step("gcload", testGCLoad)
	step("usbload", testUSBLoad)
	step("latency", testLatency)
}

func soak(seconds int) {
	if !ping() && !recoverPeer() {
		result("soak", false, "no peer")
		return
	}
	end := time.Now().Add(time.Duration(seconds) * time.Second)
	rounds, missing, wrong := 0, 0, 0
	seed := uint32(1)
	for time.Now().Before(end) {
		m, w := echo(2000, 1+int(seed%127), seed)
		if m != 0 || w != 0 {
			recoverPeer()
		}
		missing += m
		wrong += w
		g, b := stream(4000, byte(seed), seed%2 == 0)
		missing += 4000 - g
		wrong += b
		rounds++
		seed++
		if rounds%10 == 0 {
			println("# soak rounds=" + itoa(rounds) + " missing=" + itoa(missing) + " wrong=" + itoa(wrong))
		}
	}
	result("soak", missing == 0 && wrong == 0, "rounds="+itoa(rounds)+" missing="+itoa(missing)+" wrong="+itoa(wrong))
}

func main() {
	uart.Configure(machine.UARTConfig{BaudRate: 115200})
	for {
		println("READY")
		for i := 0; i < 100; i++ {
			if b, err := usb.ReadByte(); err == nil {
				handle(b)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func handle(b byte) {
	switch b {
	case 'g':
		runAll()
		println("DONE")
	case 's':
		var n byte
		for n == 0 {
			n, _ = usb.ReadByte()
		}
		soak(int(n) * 5)
		println("DONE")
	}
}
