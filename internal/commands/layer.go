package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pjlsergeant/byre/internal/builtins"
	"github.com/pjlsergeant/byre/internal/config"
	"github.com/pjlsergeant/byre/internal/packages"
	"github.com/pjlsergeant/byre/internal/project"
)

// layerCatalog loads home + catalog, the context every layer verb needs
// (the catalog carries the reserved bare names).
func layerCatalog(s Streams) (string, *packages.Catalog, error) {
	home, err := project.Home()
	if err != nil {
		return "", nil, err
	}
	if err := builtins.EnsureStoreOut(home, s.Err); err != nil {
		return "", nil, err
	}
	cat, err := builtins.LoadCatalogRaw(home)
	if err != nil {
		return "", nil, err
	}
	return home, cat, nil
}

// LayerNew scaffolds a named layer at ~/.byre/layers/<name>/layer.config.
// Layers are plain files, not packages: no [package] table, no version, no
// install verbs — distribution is sending someone the file.
func LayerNew(s Streams, name string) error {
	if err := config.ValidateLayerName(name); err != nil {
		return err
	}
	home, cat, err := layerCatalog(s)
	if err != nil {
		return err
	}
	if reason := config.ReservedLayerName(cat, name); reason != "" {
		return fmt.Errorf("layer name %q is reserved (%s); pick another name", name, reason)
	}
	path := config.LayerPath(home, name)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	stub := fmt.Sprintf(`# Named layer %q: a user-authored cascade layer. Every project (or layer)
# that carries 'extends = %q' resolves it live at each develop — edit it
# here once and every extending project picks the change up.
#
# Full config vocabulary EXCEPT 'template' (shape selection belongs to the
# project config). Chain onto a parent with: extends = "<layer>"

# apt = []
# skills = []
# egress = []
# [env]
`, name, name)
	if err := config.AtomicWrite(path, stub); err != nil {
		return err
	}
	fmt.Fprintf(s.Err, "byre: created %s\n", path)
	fmt.Fprintf(s.Err, "extend it from a project: extends = %q in its byre.config (byre config, EXTENDS)\n", name)
	return nil
}

// LayerList prints every dir under ~/.byre/layers with its parent pointer,
// flagging broken ones (parse errors, dangling extends, reserved-name
// squatters) with the reason they are never loaded.
func LayerList(s Streams) error {
	home, cat, err := layerCatalog(s)
	if err != nil {
		return err
	}
	infos, err := config.ListLayers(home, cat)
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		fmt.Fprintf(s.Err, "byre: no layers (create one: byre layer new <name>)\n")
		return nil
	}
	for _, li := range infos {
		// Layer names are directory names the user (or anything) dropped on
		// disk: escape them like every other listing surface.
		name := packages.EscapeTerminal(li.Name)
		switch {
		case li.Reason != "":
			fmt.Fprintf(s.Out, "%-28s  BROKEN  %s\n", name, packages.EscapeTerminal(li.Reason))
		case li.Extends != "":
			fmt.Fprintf(s.Out, "%-28s  extends %s\n", name, packages.EscapeTerminal(li.Extends))
		default:
			fmt.Fprintln(s.Out, name)
		}
	}
	return nil
}

// LayerValidate parses a layer (ban list included) and walks its chain;
// with no name it validates every layer dir. Mirrors template validate.
func LayerValidate(s Streams, name string) error {
	home, cat, err := layerCatalog(s)
	if err != nil {
		return err
	}
	if name == "" {
		infos, err := config.ListLayers(home, cat)
		if err != nil {
			return err
		}
		var bad int
		for _, li := range infos {
			if li.Reason != "" {
				fmt.Fprintf(s.Err, "byre: layer %s: %s\n", packages.EscapeTerminal(li.Name), packages.EscapeTerminal(li.Reason))
				bad++
			}
		}
		if bad > 0 {
			return fmt.Errorf("%d broken layer(s)", bad)
		}
		fmt.Fprintf(s.Err, "byre: %d layer(s) ok\n", len(infos))
		return nil
	}
	if err := config.ValidateLayerName(name); err != nil {
		return err
	}
	// LoadExtendsChain on the layer's own name parses it (ban list included),
	// checks the reserved-name gate, and walks the chain above it — cycles
	// and dangling parents fail here with their own messages. Those messages
	// can quote hostile file bytes (a layer someone sent you, an unknown-key
	// name), so escape them at this print boundary like list does its rows.
	chain, err := config.LoadExtendsChain(home, cat, name)
	if err != nil {
		return fmt.Errorf("%s", escapeMultiline(err.Error()))
	}
	if names := config.ChainNames(chain); len(names) > 1 {
		fmt.Fprintf(s.Err, "byre: layer %s ok (chain: %s)\n", name, strings.Join(names, " -> "))
	} else {
		fmt.Fprintf(s.Err, "byre: layer %s ok\n", name)
	}
	return nil
}
