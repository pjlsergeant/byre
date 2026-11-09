package commands

import _ "embed"

// deliverIconPNG is the deliver app's icon — a committed PNG asset, packed
// into .icns at install time (packICNS) and written to the Linux hicolor
// theme. The unnamed local install uses it verbatim; a labeled local install
// gets iconletters.Render over it. Replace assets/deliver-icon.png to change
// the art; the current file is a placeholder pending the designed icon.
//
//go:embed assets/deliver-icon.png
var deliverIconPNG []byte

// deliverIconSSHPNG is the remote-delivery install's icon — same packing
// rules as deliverIconPNG, drawn with iconletters.Render (an ssh
// install is labeled, so it carries icon letters whenever its label yields
// any). Replace assets/deliver-icon-ssh.png to change the
// art; local installs (named or not) keep deliverIconPNG as their base.
//
//go:embed assets/deliver-icon-ssh.png
var deliverIconSSHPNG []byte
