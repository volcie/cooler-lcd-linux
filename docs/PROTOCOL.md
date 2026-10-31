# UPSIREN Walrus Glacier 120 — HID protocol

Reconstructed by disassembling the vendor installer
`Walrus Glacier 120.msi`
(sha256 `760fd664f6d26dc3f3db1a1f40e78e8f56e7ec8f69efb12207e1662dc6e91743`,
MSI title "UPSIREN PC Monitor"). The managed code lives in
`_3FF2A8E55F3D4A4DB78D9E977E568CB3`: .NET Framework 4.7.2, WinForms, using the
CyUSB HID library.

`cooler-lcd`'s encoder is a direct transcription of the two routines named
below. Where this document says "vendor does X", X was read from that assembly's
IL, not inferred.

## Device

- VID `0x5131` (20785), PID `0x2007` (8199). `Get_Devices` compares against
  these integers literally; there is no model or firmware check.
- The HID descriptor declares a single unnumbered 64-byte output report.
- **On the wire the vendor always sends 65 bytes.** `send_usb_data` writes
  `DataBuf[0] = Outputs.ID` (0 for an unnumbered report) and then copies the
  64-byte payload to `DataBuf[1..64]`. hidapi requires the same shape: pass the
  report ID as the first byte, so the buffer is one byte longer than the report.
  Getting this wrong does not raise an error — hidapi reinterprets `payload[0]`
  as the report ID and transmits 63 bytes, shifting every field by one slot.
- Cadence: `Thread_Send` calls `SendData2` then `Thread.Sleep(200)`.

## Frame (`SendData2`)

```
payload[0]      = 0     constant
payload[1]      = 1     constant (refresh)
payload[2]      = 2     constant
payload[3..62]  = SendValueArray[0..59], each narrowed to a byte
payload[63]     = 0
```

`SendValueArray` is `new int[61]`, but `GetPCParam` assigns only slots 0..35, so
the last 24 value bytes are always zero.

The three leading constants are not a handshake, opcode sequence or checksum.
`SendData2` writes them unconditionally on every frame.

## Value slots

Every slot is a single byte. Multi-byte fields are decimal-split so the
firmware can render each digit independently.

The vendor narrows each `int` with a C# `(byte)` cast, which **wraps** on
overflow rather than saturating. `cooler-lcd` clamps instead (0..255): a value
out of range means a broken sensor reading, and showing 255 is more honest than
wrapping 256 → 0. This differs from the vendor only for out-of-range input.

| slot | field | encoding |
|----:|---|---|
| 0 | CPU temp | `floor(T)` |
| 1 | CPU temp | `(T - floor(T)) * 100` |
| 2 | CPU unit | 0 = °C, 1 = °F — a **label only**, see below |
| 3 | CPU usage | `floor(%)` |
| 4 | CPU power | `floor(W) % 100` |
| 5 | CPU power | `(W - floor(W)) * 100` |
| 6 | CPU freq | `floor(MHz) / 100` |
| 7 | CPU freq | `floor(MHz) % 100` |
| 8 | CPU volts | `floor(V)` |
| 9 | CPU volts | `floor(V) * 100` — vendor bug, see below |
| 10 | GPU temp | `floor(T)` |
| 11 | GPU temp | `(T - floor(T)) * 100` |
| 12 | GPU unit | 0 = °C, 1 = °F — label only |
| 13 | GPU usage | `floor(%)` |
| 14 | GPU power | `floor(W) % 100` |
| 15 | GPU power | `(W - floor(W)) * 100` |
| 16 | GPU freq | `floor(MHz) / 100` |
| 17 | GPU freq | `floor(MHz) % 100` |
| 18 | Fan RPM | `floor(RPM) / 100` |
| 19 | Fan RPM | `floor(RPM) % 100` |
| 20 | Pump RPM | `floor(RPM) / 100` |
| 21 | Pump RPM | `floor(RPM) % 100` |
| 22 | Year | first two digits (`2026` → 20) |
| 23 | Year | last two digits (`2026` → 26) |
| 24 | Month | 1–12 |
| 25 | Day | 1–31 |
| 26 | Hour | 0–23 |
| 27 | Minute | 0–59 |
| 28 | Second | 0–59 |
| 29 | Weekday | .NET `DayOfWeek`, Sunday = 0 |
| 30 | RAM / mainboard usage | percent |
| 31 | CPU power | `floor(W) / 100` |
| 32 | GPU power | `floor(W) / 100` |
| 33 | `ShowMode` | 0 = temps, 1 = usages, 2 = cycle |
| 34 | `AutoChangeModeTime` | seconds per mode when cycling |
| 35 | `CPU_GPU_Flag` | 0 = both, 1 = CPU only, 2 = GPU only |
| 36..59 | unused | zero |

The firmware reconstructs the multi-byte values:

```
CPU_temp  = s[0]  + s[1]/100
CPU_power = s[31]*100 + s[4] + s[5]/100
CPU_freq  = s[6]*100  + s[7]
GPU_power = s[32]*100 + s[14] + s[15]/100
```

Note that `CPU_power` above 255 W needs slot 31; slot 4 alone is the remainder
modulo 100.

## Two vendor quirks

**CPU voltage loses its fraction.** `GetPCParam` computes
`floor(V)` for slot 8, then multiplies that *integer* by 100 for slot 9. The
fractional part is discarded, which looks like copy-paste from the temperature
code. Slot 9 therefore always ends in `00`. `cooler-lcd` reproduces it, and
reports 0 for both slots since no board here exposes CPU voltage.

**The unit slot does not convert.** `Showcentigrade_Flag` selects which of two
formatted strings is written to the *label* (`℃` or `℉`), and separately sets
slot 2/12 to 0 or 1. What is stored into `SendValue_CPUTemp` / `SendValue_GPUTemp`
is the raw sensor `Value` in both branches — `GetF` (`c * 1.8 + 32`) is applied
only to the on-screen text, never to the transmitted value.

This means the firmware is told "this number is °F" while being handed °C. Either
the firmware converts on its own, or the vendor app's own display is wrong.

`cooler-lcd` does **not** reproduce this. It applies `c * 1.8 + 32` to the value
when the unit is °F, which is self-consistent regardless of which side converts:
a panel that trusts the number shows the right temperature, and one that converts
per the label double-converts. This is a deliberate deviation, not a transcription
of vendor behaviour — the one place the encoder departs from the IL.

## Shutdown frame

`frmMain_FormClosing` allocates a fresh zeroed `byte[64]`, sets `array[1] = 15`,
and sends it. On the wire: `00 00 0F 00 …` — report ID, then 15 in the payload's
second byte. It is unrelated to the refresh path and carries no other data.

## Not in the protocol

There is no handshake, no acknowledgement, no read path and no error reporting
from the device. The host writes frames and the panel either renders them or
does not. Nothing in the 65 bytes is read back.

## Confidence

High for slots 0..32 and the frame layout: those were read directly from IL and
cross-checked against two independent third-party reimplementations of the same
controller (`coldwelderx/cooler-lcd-linux` and `ScreenLEDFrostbite`), which agree
on the layout including its split-byte encoding.

Lower for slots 33..35. Their meaning comes from `TimerAdd` and the UI handlers
in the vendor app, and the values are plausible but were verified only by
inspecting the panel with each setting, not by a protocol trace.

Untested: whether any other cooler sharing this VID/PID uses different slots. The
vendor app contains no model names, which suggests the firmware is shared, but
that is an argument from absence.
