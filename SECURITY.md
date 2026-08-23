# Security policy

Security fixes target the latest stable Rivune release. Older releases, prereleases, and development builds receive no guaranteed backports.

## Report privately

Use GitHub's private [vulnerability report](https://github.com/moodiness/rivune/security/advisories/new). If unavailable, open a public issue containing only `Private security contact requested`; include no technical details.

A useful private report includes:

- affected release/commit, deployment mode, and client platform;
- the crossed security boundary and realistic impact;
- minimal reproduction against systems and accounts you control;
- sanitized evidence, prerequisites, and known mitigations.

Never send production credentials, tokens, encryption/signing keys, private URLs, personal media, or an unredacted database. Revoke any secret exposed during research. Report upstream vulnerabilities to the upstream project unless Rivune uses the dependency unsafely.

Keep details private until a fix/advisory is available or a disclosure date is agreed. Good-faith research must use authorized systems, minimize access and disruption, stop after proving impact, avoid social engineering or denial of service, and follow applicable law.
