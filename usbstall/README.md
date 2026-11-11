# USB stall test

Checks that a board keeps running, and sends and receives clean data, when
the USB host misbehaves. The host stops reading, closes the port, toggles DTR,
changes the line coding or floods the board with data. A program that prints
to a closed or unread port must not stay blocked once the host is gone.

Two boards are used. The device under test (DUT) runs `flood/`, which prints
without end over USB. It also sends one byte over its UART every 256 lines.
The peer runs `monitor/`, which sends every byte it receives back out of its
UART. The peer must have a USB bridge on that UART, such as an Arduino Uno R3.
The host reads the peer port to see whether the DUT main loop still runs.

## Setup

1. Connect DUT TX to peer RX and DUT ground to peer ground. Only this
   direction is needed. Do not connect peer TX to a 3.3 V DUT.
2. Flash the peer. The DUT drives the shared RX line of the peer, so the peer
   bootloader fails to sync while the DUT runs. Flash the peer while the DUT
   is in its bootloader or with the DUT TX wire unplugged.

   ```
   tinygo flash -target arduino -port <peer-port> ./usbstall/monitor
   ```

3. Flash the DUT with a USB console.

   ```
   tinygo flash -target <dut-target> -port <dut-port> ./usbstall/flood
   ```

4. Run the host program.

   ```
   go run ./usbstall -dut <dut-port> -peer <peer-port> [-stall 60s] [-scenario all]
   ```

Opening the peer port resets an Arduino, so the program waits two seconds
after it opens.

## Scenarios

Some scenarios stop reading the DUT port for `-stall`. The DUT sends until the
host buffer is full and then blocks in `println`. The program prints `#` lines
that show the main loop state every 10 seconds. If the loop never stops, use a
longer `-stall`. Boards that send slowly need more time. Select one scenario
with `-scenario <name>`. The default `all` runs them in the order below.
Run the scenarios one at a time on a board you have not tested before. A
board that hangs needs a reset, and a hang stops every scenario after it.

| Scenario | Checks |
| --- | --- |
| `resume` | After the stall the host reads again. Data flows and the main loop runs. |
| `close` | After the stall the host closes the port. The main loop must run again without a host (`close-releases`). After a reopen data flows and the loop runs. |
| `flush` | Like `close`, with the DUT calling `Flush` in its loop. `Flush` must not wait for a host that is gone. |
| `cycle` | Open and close the port `-cycles` times, then toggle DTR `2*cycles` times, while the DUT floods. The DUT must keep running and the lines it sends must be intact and in order. |
| `control` | Change the baud rate and DTR `3*cycles` times while the DUT floods. The DUT must keep running and send clean lines. It never uses 1200 baud. |
| `rxflood` | Send 2 MiB to the DUT while it does not read. The write must finish, the DUT must answer afterwards and its main loop must run. It may drop data. |
| `boundary` | Transfer sizes around the USB packet size in both directions. The DUT sends 100 blocks of 1 to 255 bytes and the host checks every byte. The host sends 64 KiB in writes of 1 to 1024 bytes and the DUT reports the count and the sum. |
| `echo` | Send 256 KiB in writes of random sizes while reading the echo at the same time. The echo must match. |
| `writers` | Three goroutines on the DUT write at once. Every byte must arrive. Lines may interleave. |
| `throughput` | Measure bytes per second from the DUT. It fails only below `-minrate`. |
| `touch` | After the stall the host opens the port at 1200 baud and closes it. The DUT serial port must disappear, which means it entered its bootloader. This leaves the DUT in its bootloader. |

## Failure modes

| Failure | Meaning |
| --- | --- |
| `resume-data` | The USB transmit path does not restart after a stall. |
| `close-releases` | A write waiting for buffer space does not notice that DTR dropped. The program stays blocked until a host opens the port again. |
| `flush-releases` | `Flush` waits for a transfer that never completes after the host left. |
| `reopen-data` or `reopen-running` | The USB state is stuck after a close and reopen. |
| `cycle-*-data` with `bad>0` | Bytes were lost or reordered inside a line after a reopen or DTR change. |
| `cycle-*-running` or `control-running` | The main loop stopped after the host toggled the control lines. The USB transmit state is stuck. |
| The program stops printing during `cycle` or `control` | The device stopped answering control requests, so a call to open, close or set DTR on the host never returns. Reset the board. |
| `rxflood-write-completes` | The device never drains its receive buffer or never accepts data again, so the host write stays blocked. |
| `rxflood-answers` or `rxflood-running` | The receive path or the main loop is stuck after a receive overflow. |
| `boundary-tx-N` | The device loses or repeats bytes for blocks of N bytes. Check how the driver ends a transfer that is a multiple of the packet size. |
| `boundary-rx-N` | The device loses or changes bytes sent in writes of N bytes. |
| `echo-integrity` | Data was lost, repeated or changed while both directions were busy. The detail shows the first differing offset. |
| `writers-bytes` | Concurrent writers lose bytes. The transmit ring buffer needs locking. |
| `throughput` | The rate is below `-minrate`. |
| `touch-bootloader` | The 1200 baud touch is not handled while the main loop is blocked, or the board needs a different reset. Check how the board enters its bootloader. |
| All windows show the main loop stopped, even with the host reading | The peer does not see the DUT. Check the wire, the common ground and that the peer runs `monitor/`. |

## Commands of the DUT firmware

`flood/` reads one byte commands from the host over USB. The other scenarios
use them to keep the DUT quiet while they check data. Lengths are 32 bit
little endian.

| Command | Action |
| --- | --- |
| `F` or `N` | Call `Flush` with every liveness byte, or stop doing that. |
| `Q` or `U` | Stop or resume the flood lines. |
| `P` | Print `pong`. |
| `Z` | Stop reading USB for 3 seconds. |
| `B` n | Write 100 blocks of n bytes. Byte k of block j is `byte(j+k)`. |
| `K` len | Swallow len bytes and print `sum <count> <sum>`. |
| `E` len | Echo the next len bytes. |
| `W` | Write lines from three goroutines and print `writers done`. |
