package main

import (
	"strings"
	"testing"
)

// ADR 0059 edits five standing records, a principle's boundary statement, the
// glossary and the security-model page, because each said something the backup
// and restore verbs contradict if left alone -- and an amendment note over a
// BODY that still states the pre-amendment rule is worse than no note. So each
// edit gets a presence arm here, pinning the one clause that would have to go
// for the amendment to be lost and never a whole sentence.

// docFragments fails naming every fragment the file does not hold, rather
// than stopping at the first, so one run says which edits went missing.
func docFragments(t *testing.T, path string, fragments ...string) {
	t.Helper()
	body := readFileT(t, path)
	for _, f := range fragments {
		if !strings.Contains(body, f) {
			t.Errorf("%s no longer contains %q -- an ADR 0059 doc edit was lost or reworded", path, f)
		}
	}
}

func TestBackupDocsADR0007ScopesTheBanToByreInitiatedSeeding(t *testing.T) {
	docFragments(t, "../../docs/adr/0007-no-credential-seeding.md",
		"**Amended by ADR 0059**",
		"byre-INITIATED copy-semantics for rotating tokens",
		"outside the ban entirely",
	)
}

func TestBackupDocsADR0008ScopesRootToASession(t *testing.T) {
	docFragments(t, "../../docs/adr/0008-build-time-uid-bake.md",
		"**Amended by ADR 0059**",
		"no root at runtime in a session",
		"nothing runs as root after PID 1 in a session",
	)
}

func TestBackupDocsADR0029NamesRestoreInTheOneAcquisitionFlow(t *testing.T) {
	docFragments(t, "../../docs/adr/0029-skills-are-packages.md",
		"**Amended by ADR 0059**",
		"is the SECOND entry to",
		"since ADR 0059, `byre restore`",
	)
}

func TestBackupDocsADR0046ScopesTheWriteBanToContextDelivery(t *testing.T) {
	docFragments(t, "../../docs/adr/0046-context-injection-buries-context-target.md",
		"**Amended by ADR 0059**",
		"about CONTEXT DELIVERY",
	)
}

func TestBackupDocsADR0053SaysSessionContainer(t *testing.T) {
	docFragments(t, "../../docs/adr/0053-launch-record-content-addressed.md",
		"**Amended by ADR 0059**",
		"Every session container byre creates gets a",
	)
}

func TestBackupDocsPrinciplesCarriesTheCiphertextClause(t *testing.T) {
	docFragments(t, "../../docs/PRINCIPLES.md",
		"a backup carries the config file as it is,",
	)
}

func TestBackupDocsGlossaryDefinesBackup(t *testing.T) {
	docFragments(t, "../../docs/GLOSSARY.md",
		"\n**Backup**:\n",
		"Both the file and the verb pair.",
		"(the retired v5 design's words",
	)
}

func TestBackupDocsSecurityModelCarriesTheResiduals(t *testing.T) {
	docFragments(t, "../../site/content/docs/security-model.md",
		"**A backup is an unencrypted file of your project's state.**",
	)
}

func TestBackupDocsDoctrineIndexHasALineFor0059(t *testing.T) {
	docFragments(t, "../../docs/adr/README.md",
		"- 0059: `byre backup` writes ONE gzip tar",
	)
}

// The two verbs are reachable from the published CLI reference, under
// "Lifecycle & recovery". TestCommandsPagePinsSiteFile pins the whole page to
// the tree's output; this names the two rows, so a command dropped from
// commandsPageAreas cannot be regenerated away quietly.
func TestBackupDocsCommandsPageCarriesBothVerbs(t *testing.T) {
	docFragments(t, "../../site/content/docs/commands.md",
		"| `byre backup [DIR]` |",
		"| `byre restore FILE [DIR]` |",
	)
}
