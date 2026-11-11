# UART stress test

Hardware test for the TinyGo UART driver of any board. Two boards are wired
together. The device under test (DUT) runs `dut/` and the peer runs `peer/`.
The DUT reports results to a host PC over its USB serial port.

## Setup

1. Pick a DUT with a console that reaches the host and a spare UART. `dut/`
   talks to the peer over the UART in `dut/link.go`, which is
   `machine.DefaultUART`, and to the host over `machine.Serial`. They must be
   different ports. Boards with a USB console work as they are. On a board
   whose `DefaultUART` is the console, such as the Arduino Uno R4 WiFi, add a
   `dut/link_<board>.go` with the board build tag that picks another UART
   (see `link_arduino_uno_r4_wifi.go`).
2. Pick a peer with a UART. `peer/` uses `machine.DefaultUART`. A board whose
   console is that UART works too, because the peer never prints.
3. Wire DUT TX to peer RX, DUT RX to peer TX, and ground to ground. Check
   that both boards use the same logic level or add a level shifter.
4. Flash each board.

   ```
   tinygo flash -target <peer-target> -port <peer-port> ./uartstress/peer
   tinygo flash -target <dut-target>  -port <dut-port>  ./uartstress/dut
   ```

   Some boards share the UART pins with an on-board USB bridge or bootloader.
   Disconnect the TX/RX wires while flashing those boards.
5. Run the host program.

   ```
   go run ./uartstress -port <dut-usb-port> [-timeout 5m] [-soak 60s]
   ```

   Do not open the peer's USB port while the test runs if opening it resets
   the peer. Do not open any port at 1200 baud, some boards enter their
   bootloader.

Without `-soak` the program runs every test once. With `-soak` it repeats
echo and stream rounds for that long.

Edit `bauds` in `dut/main.go` to match what the peer can do. A slow peer, such
as an 8 bit AVR, may not keep up with the highest rates.

## Output

The DUT prints these lines. The host program shows them as they arrive.

| Line | Meaning |
| --- | --- |
| `READY` | Waiting for a command from the host. Printed every second. |
| `RUN <group>` | A test group started. |
| `END <group> <n>ms` | The group finished. |
| `PASS <name> <detail>` | One check passed. |
| `FAIL <name> <detail>` | One check failed. |
| `# ...` | Progress from long tests. Not a result. The program hides `# progress` lines. |
| `DONE` | All tests finished. |

The program exits 0 if no test failed, 1 if any failed, and 2 on timeout. It
prints `# no output for 10s` if the DUT goes quiet. The last `RUN` line shows
which group was running.

## Peer protocol

The peer runs a small command server on its UART. Values are little endian.

| Command | Action |
| --- | --- |
| `P` | Reply `K`. |
| `B` baud32 | Reply `K`, then switch to the new baud rate. |
| `E` n32 | Echo the next n bytes as they arrive. |
| `G` n32 seed | Send n pattern bytes at full line rate. |
| `C` n32 | Swallow n bytes, reply with their sum and count (4 bytes each). |

A command that stalls is abandoned after about 100 ms. The DUT drains its
receive buffer and sends `P` again to resynchronize.

## What the tests verify

| Group | Checks |
| --- | --- |
| `ping` | 200 short round trips. Basic wiring and TX/RX. |
| `bauds` | Switch both sides to each rate in `bauds` and echo 2000 random bytes. Verifies baud rate divisors and `SetBaudRate`. |
| `echochunks` | Echo 5000 random bytes sent in chunks of 1 to 127 bytes. Verifies TX and RX run at the same time without loss, with the DUT's RX buffer almost full. |
| `duplex` | Send 20000 bytes one at a time while reading the echo. Verifies RX keeps up when TX and RX interleave tightly. |
| `streams` | The peer sends 30000 pattern bytes at 115200 to 500000 baud, with and without USB output from the DUT. Verifies RX interrupt latency and USB interference. |
| `bufferhold` | The peer sends 100 then 300 bytes while the DUT does not read. Verifies the ring buffer holds its data in order, drops the excess without corrupting what it holds, and still works afterward. |
| `framing` | The DUT listens at the wrong baud rate while the peer sends. Verifies framing errors are cleared and the link recovers at the right rate. |
| `overrun` | The DUT masks interrupts while the peer sends. Verifies a hardware overrun does not corrupt data (received bytes are an in order subset of what was sent) and the receiver recovers. |
| `txblocks` | The DUT writes blocks up to 32 KiB to the peer, which returns a checksum. Verifies back to back transmit and `Write` of large buffers. |
| `baudswitch` | Alternate between two rates 40 times with an echo after each. Verifies repeated reconfiguration. |
| soak | Repeated echo and stream rounds with random chunk sizes. Finds rare losses. |

## Failure modes

| Failure | Likely cause |
| --- | --- |
| No `READY` or no output | The DUT did not boot, wrong USB port, or the console is not USB. Check `-serial`. |
| `FAIL ping` or `no peer` | Wiring (TX/RX not crossed, no common ground), wrong level, peer not flashed, or the peer is at another baud rate. The DUT tries every rate in `bauds` before it gives up. |
| `FAIL baud-N switch failed` | The peer does not support rate N, or the DUT divisor is wrong at that rate. Check with a logic analyzer. |
| `baud-N` with `wrong>0` | Baud rate error between the two boards. Compare the divisor error on each side. High rates on a slow peer also cause this. |
| `missing>0`, `wrong=0` | Bytes lost. Receiver overrun or a full ring buffer. The peer may be too slow at that rate. |
| `wrong>0`, `missing=0` | Bytes changed. Baud mismatch, a framing error accepted as data, or a stop bit or parity setting that differs between the boards. |
| `stream-N` fails but `stream-N-usb` passes or the reverse | Interrupt latency. Another interrupt, often USB, delays the UART interrupt for longer than one byte time. Check interrupt priorities. |
| `buffered` is not 100 or 127..128 | The ring buffer size changed or bytes were dropped before they reached it. |
| `buffer-overflow-keeps-prefix` fails | The buffer overwrites old data when full instead of dropping new data. |
| `framing-recovers` or `overrun-recovers` fails | An error flag is never cleared, so the receiver stays stuck, or an interrupt fires in a loop. |
| `overrun-no-corruption` fails | After an overrun the driver kept a wrong byte, for example one from a shifted or half received frame. |
| `tx-block-N` fails | Transmit gaps or truncated output. `Write` returns before the last byte leaves, or TX data is overwritten. |
| `TIMEOUT` or `# no output` | The DUT hung. The last `RUN` line shows the group. A hang in `streams` or `overrun` often means an interrupt storm. Reset the board and rerun that group. |

A single failure in one run can also come from the peer. Run twice, swap the
roles of the boards if both have a UART and USB, and use a logic analyzer when
the cause is unclear.
