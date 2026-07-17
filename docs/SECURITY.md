# Security

byre's security model -- the threat model, the boxed/not-boxed contract,
and the specific facts worth knowing (daemon access is root-equivalent,
a skill is trusted code, `env` bakes into the image, the firewall's IP
snapshot, `--self-edit`, shared volumes) -- is published at
<https://getbyre.com/docs/security-model/>. Its source lives in this
repo at `site/content/docs/security-model.md`; when you change behavior
it describes, update it in the same unit of work.

## Reporting

byre is a young single-maintainer project. Report security issues via
GitHub security advisories on `pjlsergeant/byre` (preferred) or a plain
issue if the report is not sensitive.
