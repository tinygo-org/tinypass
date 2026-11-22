//go:build tinygo

package main

import (
	"machine"
	"runtime"
	"runtime/volatile"
	"time"
)

// streamIdle is stream with a caller supplied idle step and a hook that runs
// after each byte. It also returns the largest gap between received bytes.
func streamIdle(n uint32, seed byte, idle func(), each func(got int)) (got, bad int, maxGap time.Duration) {
	drain()
	cmd := []byte{'G', 0, 0, 0, 0, seed}
	put32(cmd[1:], n)
	uart.Write(cmd)
	last := time.Now()
	for uint32(got) < n && time.Since(last) < 300*time.Millisecond {
		if uart.Buffered() == 0 {
			idle()
			continue
		}
		b, _ := uart.ReadByte()
		now := time.Now()
		if g := now.Sub(last); got > 0 && g > maxGap {
			maxGap = g
		}
		last = now
		if b != pattern(uint32(got), seed) {
			bad++
		}
		got++
		if each != nil {
			each(got)
		}
	}
	return
}

func spinIdle() {}

func itoaU(v uint32) string { return itoa(int(v)) }

// switchPeer tells the peer to change rate and waits for its reply.
func switchPeer(baud uint32) bool {
	drain()
	cmd := []byte{'B', 0, 0, 0, 0}
	put32(cmd[1:], baud)
	uart.Write(cmd)
	var b [1]byte
	if readN(b[:], 200*time.Millisecond) != 1 || b[0] != 'K' {
		return false
	}
	time.Sleep(10 * time.Millisecond)
	return true
}

// testSleepPoll receives while the program polls with different idle steps.
func testSleepPoll() {
	type mode struct {
		name    string
		idle    func()
		maxBaud uint32
	}
	modes := []mode{
		{"spin", spinIdle, 0},
		{"gosched", runtime.Gosched, 0},
		{"sleep100us", func() { time.Sleep(100 * time.Microsecond) }, 0},
		{"sleep1ms", func() { time.Sleep(time.Millisecond) }, 0},
		{"sleep5ms", func() { time.Sleep(5 * time.Millisecond) }, 115200},
	}
	for _, baud := range []uint32{9600, 115200, 250000} {
		if !setBaud(baud) {
			result("sleeppoll-"+itoaU(baud), false, "switch failed")
			recoverPeer()
			continue
		}
		n := baud / 50
		if n > 2000 {
			n = 2000
		}
		for _, m := range modes {
			if m.maxBaud != 0 && baud > m.maxBaud {
				continue
			}
			got, bad, _ := streamIdle(n, byte(baud), m.idle, nil)
			result("sleeppoll-"+m.name+"-"+itoaU(baud), uint32(got) == n && bad == 0,
				"got="+itoa(got)+"/"+itoaU(n)+" bad="+itoa(bad))
		}
	}
	recoverPeer()
}

// testReconfigure calls Configure repeatedly and with new rates.
func testReconfigure() {
	bad := 0
	for i := 0; i < 20; i++ {
		uart.Configure(machine.UARTConfig{BaudRate: 115200})
		if !ping() {
			bad++
			recoverPeer()
			continue
		}
		if m, w := echo(64, 16, uint32(i)+1); m != 0 || w != 0 {
			bad++
		}
	}
	result("reconfigure-same", bad == 0, "bad="+itoa(bad))

	for _, b := range []uint32{57600, 250000, 9600, 115200} {
		name := "reconfigure-" + itoaU(b)
		if !switchPeer(b) {
			result(name, false, "peer switch failed")
			recoverPeer()
			continue
		}
		uart.Configure(machine.UARTConfig{BaudRate: b})
		time.Sleep(10 * time.Millisecond)
		ok := ping()
		m, w := 0, 0
		if ok {
			m, w = echo(500, 32, b)
		}
		result(name, ok && m == 0 && w == 0, "missing="+itoa(m)+" wrong="+itoa(w))
	}
	recoverPeer()
}

// testBaudStress calls SetBaudRate in tight loops and during reception.
func testBaudStress() {
	for i := 0; i < 1000; i++ {
		uart.SetBaudRate(115200)
	}
	drain()
	result("baudstress-same", ping(), "")

	for i := 0; i < 200; i++ {
		uart.SetBaudRate(9600)
		uart.SetBaudRate(115200)
	}
	drain()
	result("baudstress-alternate", ping(), "")

	uart.Write([]byte{'G', 244, 1, 0, 0, 21}) // 500 bytes
	time.Sleep(5 * time.Millisecond)
	for i := 0; i < 10; i++ {
		uart.SetBaudRate(115200)
		time.Sleep(time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	drain()
	result("baudstress-midstream", ping(), "")
	got, bad, _ := streamIdle(2000, 23, runtime.Gosched, nil)
	result("baudstress-stream-after", got == 2000 && bad == 0, "got="+itoa(got)+" bad="+itoa(bad))
}

// extraBauds are rates not in bauds. Edit it to match what the peer can do.
var extraBauds = []uint32{1200, 2400, 19200, 38400, 230400, 460800, 921600}

func testOddBauds() {
	for _, b := range extraBauds {
		name := "oddbaud-" + itoaU(b)
		if !setBaud(b) {
			result(name, false, "switch failed")
			recoverPeer()
			continue
		}
		n := 200
		if b >= 115200 {
			n = 2000
		}
		missing, wrong := echo(n, 32, b)
		result(name, missing == 0 && wrong == 0, "missing="+itoa(missing)+" wrong="+itoa(wrong))
	}
	result("oddbaud-restore", recoverPeer(), "")
}

var (
	stopAlloc volatile.Register8
	sink      []byte
)

func allocLoop() {
	for i := 0; stopAlloc.Get() == 0; i++ {
		sink = make([]byte, 48)
		if i%50 == 0 {
			runtime.GC()
		}
		runtime.Gosched()
	}
}

// testGCLoad receives while the heap is allocated and collected.
func testGCLoad() {
	stopAlloc.Set(0)
	go allocLoop()
	each := func(got int) {
		sink = make([]byte, 32)
		if got%64 == 0 {
			runtime.Gosched()
		}
	}
	for _, b := range []uint32{115200, 250000} {
		if !setBaud(b) {
			result("gcload-"+itoaU(b), false, "switch failed")
			recoverPeer()
			continue
		}
		got, bad, _ := streamIdle(20000, byte(b), runtime.Gosched, each)
		result("gcload-stream-"+itoaU(b), got == 20000 && bad == 0, "got="+itoa(got)+" bad="+itoa(bad))
		missing, wrong := echo(2000, 32, b)
		result("gcload-echo-"+itoaU(b), missing == 0 && wrong == 0, "missing="+itoa(missing)+" wrong="+itoa(wrong))
	}
	stopAlloc.Set(1)
	time.Sleep(50 * time.Millisecond)
	recoverPeer()
}

// drainUSB reads a few bytes from the host so its OUT transfers complete.
func drainUSB() {
	for i := 0; i < 64; i++ {
		if _, err := usb.ReadByte(); err != nil {
			return
		}
	}
}

// testUSBLoad receives while the host sends data to the USB OUT endpoint.
// The host program starts and stops the load on the "# load" lines.
func testUSBLoad() {
	for _, b := range []uint32{115200, 250000, 500000} {
		name := "usbload-" + itoaU(b)
		if !setBaud(b) {
			result(name, false, "switch failed")
			recoverPeer()
			continue
		}
		println("# load start")
		time.Sleep(300 * time.Millisecond)
		got, bad, _ := streamIdle(30000, byte(b), runtime.Gosched, func(got int) {
			if got%64 == 0 {
				drainUSB()
			}
			if got%512 == 0 {
				println("# progress " + itoa(got))
			}
		})
		println("# load stop")
		time.Sleep(200 * time.Millisecond)
		for i := 0; i < 100; i++ {
			drainUSB()
		}
		result(name, got == 30000 && bad == 0, "got="+itoa(got)+" bad="+itoa(bad))
	}
	recoverPeer()
}

// testLatency reports the largest gap between received bytes while spinning.
func testLatency() {
	for _, b := range []uint32{115200, 250000, 500000} {
		name := "latency-" + itoaU(b)
		if !setBaud(b) {
			result(name, false, "switch failed")
			recoverPeer()
			continue
		}
		got, bad, gap := streamIdle(20000, byte(b), spinIdle, nil)
		result(name, got == 20000 && bad == 0,
			"got="+itoa(got)+" bad="+itoa(bad)+" maxgap="+itoa(int(gap/time.Microsecond))+"us")
	}
	recoverPeer()
}
