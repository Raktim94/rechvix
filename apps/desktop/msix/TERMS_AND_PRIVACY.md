# Rechvix Desktop — Terms of Use & Privacy Policy

> **DRAFT — review with qualified legal counsel before submitting to Microsoft
> Partner Center. Not legal advice.**

*Applies to: the "Rechvix" app distributed via the Microsoft Store
(Partner Center Store ID `9NMPSP7CR5RW`, Package Family Name
`NODEDRINFOTECHLIMITED.Rechvix_wsh4jzg5a6682`).*

Last updated: 2026-09-19

## 1. What this app is

Rechvix (desktop) is a thin client shell built with Tauri. It does not
contain any billing, inventory, accounting, or GST logic of its own. On
first launch it asks you for the URL of a rechvix server — a self-hosted
instance that you or your organization runs and controls — and from then
on it simply opens that URL in a native application window, the same way
a web browser would. All actual functionality (invoicing, inventory,
accounting, GST filings, user accounts, etc.) is provided entirely by
that server, not by this app.

This document covers only your use of the Rechvix Windows app as
distributed through the Microsoft Store. It is not a services agreement,
a hosting agreement, or a support contract for any particular rechvix
server — see Section 6.

## 2. Who operates what

- **The publisher** — NODEDR INFOTECH LIMITED ("we", "us", "the
  publisher") — builds and distributes this desktop app through the
  Microsoft Store.
- **The server** — the rechvix instance you configure the app to connect
  to — is operated by you, your employer, or whoever set it up
  ("you", "the operator"). The publisher does not operate, host, manage,
  or have access to any operator's server or the data on it, unless you
  separately and explicitly engage us to do so outside of this Store
  listing.

Because of this split, most of what a typical Store app's privacy policy
covers (accounts, business records, uploaded files, analytics on your
usage of the product) simply is not something the publisher touches when
you use this desktop shell. It's covered by whatever policy the operator
of your specific server has adopted, not by this document.

## 3. What data the app itself handles

- **Server URL.** The one piece of information the app stores is the
  server address you type in on first run (or later change via
  "Change Server…"). It is saved locally on your device, in the app's
  own per-package storage, so you don't have to re-enter it every time.
  It is not sent to the publisher. Uninstalling the app deletes it along
  with everything else Windows cleans up on uninstall.
- **Everything else** — your business data, invoices, customer records,
  inventory, GST filings, login credentials, and so on — lives on and is
  transmitted to/from your own server. It passes through the app's
  embedded web view exactly as it would through a browser tab pointed at
  the same URL; the app does not intercept, log, copy, or forward it
  anywhere else.
- **No telemetry.** This desktop shell does not collect analytics,
  crash reports, usage metrics, or any other telemetry about you or your
  use of the app, and does not phone home to the publisher.
- **No advertising or third-party trackers.** There are none in this
  app. There is nothing here for the app to share with advertisers,
  because it collects nothing to share.

Standard Windows/Microsoft Store platform telemetry (e.g. install and
crash diagnostics collected by Windows itself) may still apply — that is
governed by Microsoft's own privacy terms for the Store and Windows, not
by the publisher.

## 4. GST e-Invoice / e-Way Bill submissions

If your server has GST e-Invoice or e-Way Bill features enabled, those
submissions are made directly from your own server to the relevant
Indian government (GSTN/e-Way Bill) endpoints, using credentials and
configuration that live on your server. The desktop app is not in that
data path at all — it only displays the web pages your server serves.
The publisher is not a party to, and has no visibility into, those
government filings or the data they contain.

## 5. Acceptable use

You agree not to use the Rechvix desktop app to:

- access a server you are not authorized to access;
- attempt to reverse engineer, tamper with, or circumvent the app's
  packaging or code signing in a way that violates the Microsoft Store
  terms; or
- use the app for any unlawful purpose.

Everything else about how you use your rechvix server — user
permissions, data retention, filing accuracy, business records — is
between you and (if different) the operator of that server; it is not
something this Store app enforces or is responsible for.

## 6. Relationship to the AGPLv3 license

The underlying rechvix source code (the server, the web UI, and this
desktop shell) is free software licensed under the GNU Affero General
Public License v3.0 (AGPLv3) — see the `LICENSE` file in the source
repository. If you self-host rechvix, your rights and obligations around
running, modifying, and redistributing the software are governed by the
AGPLv3 itself and by whatever separate deployment/support documentation
applies to your installation — not by this document.

This Terms of Use & Privacy Policy is specifically and only about
obtaining, installing, and running the packaged desktop app through the
Microsoft Store. It does not restrict, expand, or substitute for any
right you already have under the AGPLv3.

## 7. No warranty

THE APP IS PROVIDED "AS IS" AND "AS AVAILABLE", WITHOUT WARRANTY OF ANY
KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE, TITLE, AND
NON-INFRINGEMENT. The publisher does not warrant that the app will be
uninterrupted, error-free, or compatible with any particular server
version, nor does it warrant the accuracy, completeness, or legal
sufficiency of any GST filing, invoice, or other output your server
produces — that responsibility rests with the operator of the server you
connect to.

## 8. Limitation of liability

TO THE MAXIMUM EXTENT PERMITTED BY APPLICABLE LAW, THE PUBLISHER SHALL
NOT BE LIABLE FOR ANY INDIRECT, INCIDENTAL, SPECIAL, CONSEQUENTIAL, OR
PUNITIVE DAMAGES, OR ANY LOSS OF DATA, REVENUE, OR PROFITS, ARISING OUT
OF OR IN CONNECTION WITH YOUR USE OF (OR INABILITY TO USE) THE APP, EVEN
IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGES. Because the app is free
software distributed at no charge, the publisher's total aggregate
liability arising out of or relating to the app is, to the extent
permitted by law, limited to zero (INR 0 / USD 0).

Nothing in this section limits liability that cannot be limited under
applicable law.

## 9. Changes to this document

We may update this Terms of Use & Privacy Policy from time to time,
primarily to keep it accurate as the app changes (e.g. if telemetry or
new capabilities are ever added — which would also require updating the
`Capabilities` declared in the app's package manifest). The version
distributed with a given Store listing reflects the version in effect
for that release.

## 10. Contact

Questions about this document, the desktop app, or the Microsoft Store
listing can be sent to:

**ranjitraktim5@gmail.com**

This address is for the Store app and this document specifically — for
issues with a particular self-hosted rechvix server (data, filings,
support), contact whoever operates that server.
