# Security Policy

## Reporting a Vulnerability

SeedHammer engraves bitcoin seed phrases and wallet descriptors — the security of
the machine, its firmware, and its supply chain is critical to its users.

**Please do NOT open a public GitHub issue for security vulnerabilities.**

Instead, report vulnerabilities privately through one of these channels:

| Channel | Details |
|---------|---------|
| Email | [team@seedhammer.com](mailto:team@seedhammer.com) |
| Email (alternative) | [support@seedhammer.com](mailto:support@seedhammer.com) |
| Nostr (encrypted DM) | `npub1z072s0nvldeva7a6qek3kj358f0h5gm640kkllkk52h0qrjtnw8q38wfm4` |

When reporting, please include:

- A description of the vulnerability and its potential impact
- Steps to reproduce (a proof of concept is welcome)
- The component affected (firmware/controller, seed or descriptor parsing,
  NFC/USB input handling, build/release pipeline, PCB/hardware design)
- Any suggested remediation, if you have one

## What to Expect

- **Acknowledgement** of your report within a few working days.
- **Assessment and triage** — we will confirm the issue, assess severity, and
  keep you informed of progress.
- **Fix and disclosure** — confirmed vulnerabilities are remediated as a
  priority, and reporters are credited (with permission) once a fix is released.

We ask that you practise responsible disclosure: give us reasonable time to fix
the issue before any public disclosure, and do not access or exfiltrate user
secrets beyond what is necessary to demonstrate the vulnerability.

## Scope

Of particular interest:

- Anything that could leak, weaken, or alter engraved secrets (seed words,
  passphrases, descriptors) — including via NFC, USB, or the display
- Weaknesses in entropy generation (dice roll / camera / on-device randomness)
- Parsing bugs in untrusted input (QR codes, NFC payloads, descriptors, PSBTs)
- Compromise of the firmware build, signing, or reproducible-build process
- Ways to make the machine engrave data that differs from what was verified
  on screen

## Supported Versions

Security fixes are applied to the `main` branch and shipped in the next
firmware release. Users should run the
[latest release](https://github.com/seedhammer/seedhammer/releases/latest).
