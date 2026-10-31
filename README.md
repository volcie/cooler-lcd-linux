# cooler-lcd-linux

Linux driver for the segmented display on CPU coolers built around the HID
`5131:2007` controller — UPSIREN Walrus (Glacier 120 and siblings), Vevor/HT
coolers, Eagle Warrior Frostbite, and rebrands running the same firmware.

The cooler ships with Windows-only software. This replaces it. The panel's
firmware draws its own digits and the host only streams sensor values over HID;
no framebuffer is transferred.

Tested against a UPSIREN Walrus Glacier 120.

## Why the display is dark

Nothing is broken. The firmware renders nothing on its own — it draws digits
only when the host sends a frame, every 200 ms. With no Windows app running,
there are no frames, so the panel stays blank on Linux.

## Requirements

- Linux with `hidraw`
- To build: Go 1.27+ (pinned in `mise.toml`) and `libudev` headers

The binary links against cgo/`libudev` through `go-hid`, so a fully static build
is not possible and `CGO_ENABLED=1` is required.

## Install

Download a release binary, then let it install itself:

```bash
sudo ./cooler-lcd-linux-amd64 install
```

That copies itself to `/usr/local/bin/cooler-lcd`, creates the `cooler-lcd`
group, installs the udev rule granting the panel to that group, enables a
**system** service, and writes a default `/etc/cooler-lcd/config.toml`.

It is a system service, not a user one, because the panel belongs to the machine:
it should come up at boot and keep running at the login screen. A user service
would stop at logout, and its `uaccess` ACL would disappear with the session.
The service runs unprivileged via `DynamicUser=yes` with
`SupplementaryGroups=cooler-lcd`, so only it can open the device — not every
logged-in user.

```
--no-service   skip the systemd user service
--no-start     install the service but leave it stopped
```

Re-running is a no-op for anything already current. To undo it:

```bash
cooler-lcd uninstall          # asks before deleting your config, defaulting to no
```

`uninstall` removes the udev rule, service and binary, then asks whether to
delete `/etc/cooler-lcd/config.toml`. Only real removals are reported; if
nothing was installed it says `nothing to remove` rather than claiming success.

The config prompt defaults to **keep**, so a reinstall keeps your settings.
`--keep-config` skips the question and `--keep-binary` leaves the binary in
place. When stdin is not a terminal the prompt is skipped and the config is kept.

From a source checkout, `make install` builds and then runs the same self-install.

If the panel is not writable immediately after install, unplug and replug the
cooler's USB header. udev does not retroactively re-apply `GROUP` to a node that
already exists.

## Everyday use

```bash
cooler-lcd status     # running? panel visible and writable? last log lines
sudo cooler-lcd restart    # after a config change
sudo cooler-lcd stop
```

Writes need root because these are system units; `status` does not.

These are shorthands for `cooler-lcd service <action>`; no `systemctl` needed.
`status` folds together the three questions worth asking — is the unit up, can
the device be opened, and what did it last say — so a broken install does not
require chasing `journalctl`.

Running `cooler-lcd` with no subcommand streams to the panel in the foreground
and blanks it on Ctrl-C, which is useful for a one-off test.

## Configuration

The service runs `cooler-lcd` with no arguments, so its behaviour comes from
`/etc/cooler-lcd/config.toml`:

```bash
sudo cooler-lcd config init   # write a commented default file
cooler-lcd config             # show what is loaded
sudo cooler-lcd restart       # apply changes
```

`install` writes this file already. As root there is only one config; as a normal
user, `~/.config/cooler-lcd/config.toml` is used when it exists, so `cooler-lcd
print` and a foreground run work without sudo.

```toml
unit    = "c"        # or "f"
show    = "temp"     # "temp", "usage" or "cycle"
cycle   = "2s"       # seconds per mode when show = "cycle"
only    = "both"     # "both", "cpu" or "gpu"
freq    = "max"      # or "avg"
refresh = "200ms"
```

Command-line flags win over the file: `cooler-lcd --unit f print` loads the config
and then overrides that one setting. Unknown keys are rejected by name rather than
ignored, since a typo like `shwo = "usage"` would otherwise look like it worked.

The file is read at startup and not watched, hence the `restart`.

## Commands

| command | purpose |
|---|---|
| *(none)* | stream frames until interrupted, blanking the panel on exit |
| `once` | send a single frame and leave values on screen |
| `print` | show resolved sensors and the encoded payload; no device I/O |
| `list` | list matching HID devices |
| `status` | is the service running; is the panel visible and writable |
| `start` / `stop` / `restart` | control the background service |
| `config` | show, create or validate the configuration file |
| `install` / `uninstall` | set up or remove udev rule and service |

## Flags

What the panel shows:

```
--unit c|f                temperature scale (default c)
--show temp|usage|cycle   what to render (default temp)
--cycle DURATION          time per mode when --show=cycle (default 2s)
--only both|cpu|gpu       which halves to populate (default both)
--freq max|avg            CPU clock to report (default max)
```

Runtime:

```
--device PATH         hidraw node (default: autodetect by USB ID)
--refresh DURATION    frame interval (default 200ms, matching the vendor app)
--config PATH         config file (default /etc/cooler-lcd/config.toml)
--verbose             log every frame
--log-json            structured JSON logs
```

Sensor overrides:

```
--cpu-temp PATH     CPU temperature sensor
--gpu-hwmon DIR     amdgpu hwmon directory
--fan-path PATH     fan tachometer
--pump-path PATH    pump tachometer (none detected by default)
--rapl PATH         CPU power energy counter
```

An explicitly set sensor path that does not exist is an error at startup rather
than a silent `0` on the display.

All flags are persistent, so position does not matter:
`cooler-lcd --unit f once` and `cooler-lcd once --unit f` are equivalent.

## Sensors

Autodetected, then overridable: `k10temp`/`zenpower`/`coretemp` for CPU
temperature, the package RAPL counter for CPU power, `amdgpu` for GPU telemetry,
and the first spinning fan for RPM. `print` shows what resolved and where from.

The CPU fan tachometer is usually **not** in hwmon on consumer boards. It needs a
SuperIO driver (`nct6775`, `it87`) loaded with `acpi_enforce_resources=lax`.
Without it the fan slot reports the GPU fan instead.

## Building from source

```bash
make build          # host architecture → dist/cooler-lcd-linux-$arch
make linux-amd64
make linux-arm64
make test
```

Building both locally takes more than a Go toolchain, because `go-hid` is
cgo-only:

| target | needs |
|---|---|
| `linux-amd64` | host C compiler, host `libudev` headers |
| `linux-arm64` | `aarch64-linux-gnu-gcc` **and** an arm64 sysroot with `libudev` |

`make all` builds each target in turn and stops at the first one whose toolchain
is missing. That is why CI is the supported path for arm64: it builds natively on
an arm64 runner, so no cross sysroot is involved. Tagged releases carry
`cooler-lcd-linux-amd64`, `cooler-lcd-linux-arm64` and a `SHA256SUMS`.

## Compatibility

The vendor's own application matches the device on USB ID alone
(`PID == 8199 && VID == 20785`) and contains no model names, so the frame format
appears to be shared across the family rather than being per-cooler. The layout
was recovered from that application — see [docs/PROTOCOL.md](docs/PROTOCOL.md),
including the two places this implementation deliberately differs from it.

Slots 33–35 are not telemetry; they select what the panel displays, and are the
most likely point of divergence on another model.

## Layout

```
cmd/cooler-lcd/       entry point and runtime tuning
internal/app/         cobra commands, streaming loop, config, logging
internal/panel/       wire format and hidapi transport
internal/telemetry/   sysfs/procfs sensor discovery and sampling
internal/assets/      embedded udev rule and systemd unit
internal/version/     build metadata
docs/PROTOCOL.md      recovered wire protocol and its provenance
```

## Licence

MIT — see [LICENSE](LICENSE). Not affiliated with UPSIREN or any cooler vendor;
no vendor code is included or redistributed.

[docs/PROTOCOL.md](docs/PROTOCOL.md) was derived by disassembling a publicly
downloadable vendor installer. It documents an on-the-wire format rather than
quoting vendor code, and the installer itself is not redistributed here.
