package commands

import _ "embed"

// deliverIconPNG is the deliver app's icon — a committed PNG asset, packed
// into .icns at install time (packICNS) and written verbatim for the Linux
// hicolor theme. Replace assets/deliver-icon.png to change the art; the
// current file is a placeholder pending the designed icon.
//
//go:embed assets/deliver-icon.png
var deliverIconPNG []byte

// deliverIconSSHPNG is the remote-delivery install's icon — same packing
// rules as deliverIconPNG. Replace assets/deliver-icon-ssh.png to change
// the art; local installs (named or not) keep deliverIconPNG.
//
//go:embed assets/deliver-icon-ssh.png
var deliverIconSSHPNG []byte
