# Documentation preview dispatch

Documentation remains canonical in `docs/cli`. Three workflows support the
website integration:

- `docs-validate.yml` checks `SUMMARY.md`, page coverage, headings, and local
  links for every pull request and `main` update.
- `docs-preview-dispatch.yml` sends the exact documentation commit and source
  repository to the secret-free site build in `Margin-Lab/marginlab`.
- `docs-release-dispatch.yml` emits the distinct `docs-release` event only for
  a trusted `Margin-Lab/evals` `main` revision. Pull-request documentation can
  never enter this production-eligible path.

The preview dispatch workflow uses `pull_request_target`, checks out the exact
frozen base-branch revision from the event, and never reads or executes
pull-request content. Preview
dispatch is limited to non-draft branches in `Margin-Lab/evals`; fork pull
requests still receive secret-free documentation validation but cannot consume
the deployment credential or publish arbitrary content to the preview domain.

The release workflow is separate and runs only after documentation reaches
`main`. It has no manual trigger because it handles a GitHub App credential.
Its payload includes both `source_ref: refs/heads/main` and a `source_sha`
matching the requested documentation SHA. The website repository independently
resolves and verifies `Margin-Lab/evals` `main` before it labels a build as
production eligible.

## Required GitHub App

Create a dedicated GitHub App for preview dispatches and install it only on
`Margin-Lab/marginlab`.

- Repository permission: **Contents — Read and write**
- Repository selection: **Only select repositories → `marginlab`**
- No organization, administration, pull-request, Actions, or deployment
  permissions are required.

GitHub's repository-dispatch endpoint requires Contents write permission. The
App token does not receive Firebase credentials and cannot deploy a site
directly. The trusted workflow uses its broader Contents permission only to
send the repository-dispatch event, so the private key must remain protected.

Configure these values in `Margin-Lab/evals`:

| Kind | Name |
| --- | --- |
| Repository variable | `MARGINLAB_PREVIEW_APP_ID` |
| Repository secret | `MARGINLAB_PREVIEW_APP_PRIVATE_KEY` |

The workflows exchange the private key for a short-lived installation token
and send only the exact docs SHA, canonical dispatcher, docs source repository,
release intent, docs PR number when applicable, and preview channel ID. The
later privileged Firebase workflow consumes the successful static artifact
without executing it. Do not replace this with a broadly scoped personal
access token.

The receiving workflow and the rest of the required configuration are
documented in `Margin-Lab/marginlab/.github/PREVIEW_AUTOMATION.md`.
