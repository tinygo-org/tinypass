# USB stall test

Checks that a board keeps running when the USB host stops reading or closes
the serial port. A program that prints to a closed or unread port must not
stay blocked forever once the host is gone.

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

The host stops reading the DUT port for `-stall`. The DUT sends until the
host buffer is full and then blocks in `println`. The program prints `#` lines
that show the main loop state every 10 seconds. If the loop never stops, use a
longer `-stall`. Boards that send slowly need more time.

| Scenario | Checks |
| --- | --- |
| `resume` | After the stall the host reads again. Data flows and the main loop runs. |
| `close` | After the stall the host closes the port. The main loop must run again without a host (`close-releases`). After a reopen data flows and the loop runs. |
| `touch` | After the stall the host opens the port at 1200 baud and closes it. The DUT serial port must disappear, which means it entered its bootloader. This leaves the DUT in its bootloader. |

`all` runs the three in that order.

## Failure modes

| Failure | Meaning |
| --- | --- |
| `resume-data` | The USB transmit path does not restart after a stall. |
| `close-releases` | A write waiting for buffer space does not notice that DTR dropped. The program stays blocked until a host opens the port again. |
| `reopen-data` or `reopen-running` | The USB state is stuck after a close and reopen. |
| `touch-bootloader` | The 1200 baud touch is not handled while the main loop is blocked, or the board needs a different reset. Check how the board enters its bootloader. |
| All windows show the main loop stopped, even with the host reading | The peer does not see the DUT. Check the wire, the common ground and that the peer runs `monitor/`. |
