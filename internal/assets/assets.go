// Package assets holds the files the binary needs to install itself.
package assets

import _ "embed"

// UdevRule grants the panel's hidraw node to a group. The GROUP= value is
// substituted at runtime with the user's primary group; see the placeholder
// below.
//
//go:embed 70-cooler-lcd.rules
var UdevRule string

// ServiceUnit is the systemd user unit. ExecStart is substituted at runtime.
//
//go:embed cooler-lcd.service.in
var ServiceUnit string

// Placeholders replaced when rendering the templates above.
const (
	GroupToken = "@GROUP@"
	BinToken   = "@BIN@"
)

// ServiceGroup is the dedicated group that owns the panel device and that the
// system service runs with. A fixed name is required: the service runs with
// DynamicUser=yes and cannot use a login user's group.
const ServiceGroup = "cooler-lcd"

// SystemConfigPath is where a system service reads its configuration.
const SystemConfigPath = "/etc/cooler-lcd/config.toml"
