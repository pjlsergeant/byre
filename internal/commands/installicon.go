package commands

import _ "embed"

// deliverIconPNG is the deliver app's icon — a committed PNG asset, packed
// into .icns at install time (packICNS) and written verbatim for the Linux
// hicolor theme. Replace assets/deliver-icon.png to change the art; the
// current file is a placeholder pending the designed icon.
//
//go:embed assets/deliver-icon.png
var deliverIconPNG []byte
