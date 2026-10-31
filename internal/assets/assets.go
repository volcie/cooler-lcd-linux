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
	GroupToken = `GROUP="users"`
	BinToken   = "@BIN@"
)
