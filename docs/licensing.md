# Licensing and distribution

Original project code is licensed under the [GNU Affero General Public License v3.0 only](../LICENSE).
The SPDX identifier is `AGPL-3.0-only`.
Copyright © 2026 traktuner.

AGPLv3 permits copying, modification and redistribution under its conditions.
It requires corresponding source availability in the circumstances described by the license,
including its provision for remote interaction with modified versions.
The project adds no restriction on commercial use or AI use.
The web interface links to the corresponding source and displays the license and warranty notices.

Third-party code retains its own licenses and attribution.
The [jlesage MIT notice](../LICENSES/jlesage-docker-crashplan-pro-MIT.txt) remains in the source and image.
CrashPlan and its bundled runtime are separate third-party software.
This project's license does not relicense them.

## Vendor files

The source repository does not contain CrashPlan binaries, assets or decompiled implementations.
The image build downloads a pinned vendor archive and verifies its SHA-256 before processing it.
The container starts from the built image without downloading the engine at startup.
The vendor runtime's legal notices remain in the image.

Image-build downloads do not themselves establish redistribution permission.
Distribution of vendor software remains subject to its terms.
The Java integration directly references and instruments vendor classes;
the project does not claim that AGPLv3 alone resolves that licensing boundary.
No additional linking exception is included.

## Trademarks

CrashPlan Headless is an independent, unofficial project.
It is not affiliated with, endorsed by, or sponsored by CrashPlan.
CrashPlan and related product names are trademarks of their respective owners.
The same notice appears on the web login screen and in the application.
