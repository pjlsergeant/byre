package commands

import _ "embed"

// deliverIconPNG is the deliver app's icon art -- a committed PNG asset,
// rendered at every icon size by iconletters (with the install's icon
// letters, if any) and packed into .icns (packICNS) or written to the
// Linux hicolor theme. It must be at least the largest rendered size.
// Replace assets/deliver-icon.png to change the art; the current file is
// a placeholder pending the designed icon.
//
//go:embed assets/deliver-icon.png
var deliverIconPNG []byte

// deliverIconSSHPNG is the remote-delivery install's icon art -- same
// rendering and packing as deliverIconPNG (an ssh install is labeled, so
// it carries icon letters whenever its label yields any). Replace
// assets/deliver-icon-ssh.png to change the art; local installs (named or
// not) keep deliverIconPNG as their base.
//
//go:embed assets/deliver-icon-ssh.png
var deliverIconSSHPNG []byte
