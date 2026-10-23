# Security policy

tilegate sits in the sign-in path, so security reports are very welcome.

## Reporting a vulnerability

Please **don't open a public issue**. Report it privately through
[GitHub security advisories](https://github.com/chillincool/tilegate/security/advisories/new).
Include the version, the auth mode, and steps to reproduce.

You should get a reply within a week. Once a fix is released, the advisory is
published and you're credited unless you'd rather not be.

## Supported versions

Only the latest release gets security fixes.

## Scope

In scope: anything that lets a user see apps their roles don't allow, forge or
replay a session, bypass token verification, or inject content into the page.

Out of scope: access to the apps themselves. tilegate only decides which tiles
are shown, and each app must enforce its own authentication.
