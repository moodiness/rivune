# Security policy

## Supported versions

Security fixes target the latest stable Rivune release. Older releases, prereleases, development images, and source snapshots may be used to confirm a report but do not receive guaranteed backports.

| Release | Security fixes |
|---|---|
| Latest stable release | Supported |
| Older stable releases | Not supported |
| Prereleases and development builds | Not supported |

Before reporting, confirm the behavior still exists on the latest release or current `main` when doing so is safe and does not risk data loss.

## Report a vulnerability privately

Do not disclose a suspected vulnerability, exploit, secret, private URL, or user data in a public issue, discussion, pull request, log, or screenshot.

Use GitHub's private [Report a vulnerability](https://github.com/moodiness/rivune/security/advisories/new) form. If that form is unavailable, open a public issue containing only the words `Private security contact requested` and no technical details; the maintainer will arrange a private channel.

Include:

- the affected release, commit, deployment mode, and client platform;
- the security boundary crossed and realistic impact;
- minimal reproduction steps or a proof of concept against an instance and accounts you control;
- sanitized request, response, log, or crash evidence;
- known prerequisites, mitigations, and whether exploitation has been observed publicly.

Never send production credentials, session tokens, encryption keys, signing material, provider headers, personal media data, or an unredacted database. Revoke and rotate any secret that was exposed while investigating.

Reports should cover Rivune code or release artifacts. Vulnerabilities in an upstream operating system, browser, media codec, package, reverse proxy, PostgreSQL, or device platform should be reported to that project unless Rivune uses it unsafely or fails to apply an available fix.

## Handling and disclosure

The maintainer will acknowledge a complete report, assess severity and affected versions, coordinate a fix and advisory when warranted, and credit the reporter if requested. Response and release timing depend on severity, exploitability, and upstream dependencies; the private advisory is the source of current status.

Keep details private until a fixed release and advisory are available or a disclosure date is agreed. Rivune will not request indefinite secrecy. If a report is out of scope or not reproducible, the maintainer will explain that decision privately.

## Good-faith research

Research is welcome when it:

- uses systems, accounts, media, and credentials you own or have explicit permission to test;
- minimizes access, collection, persistence, and service disruption;
- stops after demonstrating the security impact;
- does not use social engineering, denial of service, destructive actions, public exploitation, or third-party data;
- follows applicable law and this coordinated disclosure process.
