# Rehearsing a release

A rehearsal runs the release checklist against repositories of its own, so
every step with automation runs for real and the real release never sees it.
A step's own branch goes to your fork, and everything else goes to the
upstream repository, as in a real release. A profile that named your fork as
the release repository would send everything to the fork, because you cannot
fork your own fork. So a rehearsal needs upstream repositories of its own.

A rehearsal is always of version 1.99.0, on the branch `release-1.99`.

## What it covers

Steps 1, 4, 5, 6, 7a, 7b, 9, 10 and 11 run their automation. Step 14 runs
only its check and cannot finish, because only the key holder can sign the
Windows installer.

OBS and S3 are left out for now. Their steps are not automated yet, and the
package workflow on main still names the production bucket and OBS project.
So the rehearsal repository must not have the secrets `AWS_ACCESS_KEY_ID`,
`AWS_SECRET_ACCESS_KEY` or `OBS_WEBHOOK_TOKEN`. Without them the workflow skips
its OBS step. With them it tries to upload to the production bucket and to
trigger `isv:Rancher:dev`.

## What you need

- gh, signed in as the account whose forks the rehearsal pushes to.
- git, and [yq](https://github.com/mikefarah/yq) for the reset script.
- Rancher Desktop running on this machine, with no snapshots and no
  extensions, for step 7b. It need not be version 1.99.0; the step says which
  build the page's output comes from.
- A clone of rancher-desktop to run `yarn release` from. Your usual clone
  works. The tool finds a repository's remote by its URL and pushes to its
  public HTTPS URL when the clone has none, so git needs credentials for
  github.com over HTTPS, which `gh auth setup-git` gives it.

## Setting it up once

Pick an organization for the rehearsal, called `<org>` below, and names for
its two repositories that no repository in your own account has. The tool
looks for your fork as `<your login>/<upstream name>`, and when a repository
of yours by that name is not a fork of the upstream, it sends your branches to
the upstream instead. The names below are examples.

1. Create the organization on the free plan, and two empty public
   repositories in it, `<org>/rancher-desktop-rehearsal` and
   `<org>/docs-rehearsal`. Workflows in public repositories run free.

2. The copy pushes every branch, and GitHub runs the push workflows for each
   branch a push creates. Turn Actions off in both:

       gh api --method PUT repos/<org>/rancher-desktop-rehearsal/actions/permissions -F enabled=false
       gh api --method PUT repos/<org>/docs-rehearsal/actions/permissions -F enabled=false

3. Copy both repositories, with every branch and tag. A bare clone has only
   those; a mirror clone also fetches the pull request refs, which GitHub
   refuses on the push.

       git clone --bare https://github.com/rancher-sandbox/rancher-desktop.git
       git -C rancher-desktop.git push --mirror https://github.com/<org>/rancher-desktop-rehearsal.git
       git clone --bare https://github.com/rancher-sandbox/docs.rancherdesktop.io.git
       git -C docs.rancherdesktop.io.git push --mirror https://github.com/<org>/docs-rehearsal.git

4. Turn Actions back on in the code repository only. Every push to
   `release-1.99` and step 9's tag start the package workflow, and steps 9, 10
   and 11 wait for its runs.

       gh api --method PUT repos/<org>/rancher-desktop-rehearsal/actions/permissions -F enabled=true

5. Fork both into your account:

       gh repo fork <org>/rancher-desktop-rehearsal --clone=false
       gh repo fork <org>/docs-rehearsal --clone=false

6. Clone the documentation rehearsal repository into a directory of its own,
   not your usual documentation clone:

       gh repo clone <org>/docs-rehearsal <dir>

7. Write the profile to
   `<config>/rancher-desktop-release/rehearsal/profile.yaml`, where `<config>`
   is the directory the README's Profiles section names:

   ```yaml
   name: rehearsal
   github:
     repo: <org>/rancher-desktop-rehearsal
     docsRepo: <org>/docs-rehearsal
   ```

   It leaves out the documentation site, OBS, the screenshot bucket and the
   upgrade responder, which a rehearsal does not reach.

8. Write `<config>/rancher-desktop-release/rehearsal/settings.yaml`, naming
   the clone from step 6 by its absolute path:

   ```yaml
   docsClone: <dir>
   ```

## Running it

The rehearsal repository has the real release tags but none of their GitHub
releases, so the tool would take the newest of them for a release still in
progress. The first run names the version instead:

    VERSION=1.99.0 yarn release --profile rehearsal

Once step 1 has pushed `release-1.99`, `yarn release --profile rehearsal`
finds it without the version. Run the steps in order, as the step reference
in the README describes. Two of them differ in a rehearsal. Step 4 opens a
pull request against `release-1.99` in the rehearsal repository, and you merge
it yourself. And the `release-notes.md` that step 6 puts into the draft can
hold any text.

## Starting again

    src/go/release-checklist/reset-rehearsal.sh rehearsal

The script lists what the rehearsal left behind and deletes it once you
agree. On the rehearsal repository it closes the pull requests into
`release-1.99`, since GitHub cannot delete a pull request, and deletes the
releases and tags of 1.99 and `release-1.99` itself. It also deletes your
fork's bump branches and `release-1.99` of your documentation fork, and closes
any pull request from that branch. On this machine it deletes the 1.99 tags in
both clones, the rehearsal's cache directory with its worktrees, and the steps
you marked done. The organization, the repositories, your forks, the clones,
the profile and its settings stay. It refuses the production profile and any
repository under rancher-sandbox.
