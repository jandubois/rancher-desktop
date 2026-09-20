# release-checklist

`yarn release` drives a Rancher Desktop 1.x release. It works out which
release is in progress, checks each step it ships against the system that
would show it done, and shows one line per step with its state. It will
replace the release checklist the team follows by hand.

Run it from a clone of the repository:

    yarn release            open the dashboard
    yarn release --status   print the checklist and exit
    yarn release --run 4    run one step's automation, by its number

## The dashboard

`yarn release` opens the checklist full screen: the release it found at the
top, every step and its state below it, and a pane describing the step under
the cursor.

| Key | What it does |
| --- | --- |
| `↑` `↓` | Move to another step. `k` and `j` work too. |
| `enter` | Run the step's automation. The dashboard gives up the terminal, so the action can show its commands and ask before it runs them. |
| `m` | Mark the step done when only your judgment can settle it, such as the release notes. Press `m` again to take the mark off. |
| `f` | Write out the facts the step's manual work is done from, such as how the bundled utilities moved since the previous release. |
| `i` | Read the step's instructions, filled in with this release's values. |
| `r` | Read every check again. |
| `q` | Quit. |

`--status` prints the same checklist as plain text, for a terminal the
dashboard cannot draw in and for pasting into a report.

## Which release it drives

The tool asks the repository every time it runs, so it picks up a release
somebody started without it.

1. The highest `release-X.Y` branch is where the work happens. Lines are
   compared as numbers, so `release-1.24` beats `release-1.9`.
2. The target is the newest tag on that branch, counting only the tags of its
   own line. A new minor branch has no tag yet, and the nearest tag in its
   history belongs to the previous line; taking that one would drive a patch
   of the release before it. With no tag of its own, the target is `X.Y.0`.
3. A tag whose GitHub release is a draft, or that has no release at all, is a
   release still in progress, so it stays the target. Commits past such a tag
   only raise a warning: they go into the next patch unless the tag is burned.
4. A published release stays the target until its tag reaches `main`, because
   its post-publish steps are still open. Merging the release back into `main`
   is what marks it done.
5. Commits past a finished release mean the next patch, `X.Y.(Z+1)`.
6. Otherwise the target is the next minor, `X.(Y+1).0`, from `main`.

A burned version is never a target. Burning a bad tag deletes it from GitHub
and renames its draft to `burned-vX.Y.Z`, and the tool passes over every
version that has such a draft.

Set `VERSION` to drive a release the rule would not pick, such as a patch on
an older line:

    VERSION=1.23.2 yarn release

## What the states mean

| State | Meaning |
| --- | --- |
| `done` | The check found the work in place. |
| `available` | Every precondition holds, so the step can be run. |
| `waiting` | A process outside the tool has to finish first. |
| `blocked` | A precondition failed. The line beside it says how to fix it. |
| `skipped` | The step is for the other kind of release, or the profile leaves out the system it touches. |
| `unknown` | The check could not decide: nothing answered, or the answer could not be read. |

Every check reads the system that would show the step done, on every run, so
the tool notices work done by hand or out of order, and a step somebody undid
goes back to `available`. It keeps one piece of state of its own, the mark on
a step that only judgment settles. The release notes are done when you say
they are, and editing them afterwards puts the step back to `available`.

## Profiles

A profile names every system outside this machine that a release touches: the
repositories, the documentation site, the OBS projects, the screenshot bucket
and the upgrade responder. Step code never spells these out, so one switch
points a whole release at a fork.

The production profile is built into the binary, and plain `yarn release` uses
it. The tool reads any other profile from the user's config directory, at
`<config>/rancher-desktop-release/<name>/profile.yaml`, and
`yarn release --profile <name>` selects it. `<config>` is
`~/Library/Application Support` on macOS, `~/.config` on Linux and
`%AppData%` on Windows.

Two rules keep a rehearsal away from the real release:

- A profile other than production must name resources of its own. The tool
  compares every resource field with production's at startup and refuses to
  run when one matches, naming the field. A fork profile that forgot to change
  its OBS project cannot publish into the real one.
- A section left out turns its steps off. A step whose system the profile
  does not name is `skipped`, so a profile with no `obs` section runs the
  release without the OBS steps.

## Credentials

The tool stores no credentials. Everything that needs authentication runs
through a command line tool that keeps its own: `gh` for GitHub, and `git` for
the refs and the worktrees. Each step declares which systems it reaches, the
tool probes each one once per run, and a step whose tool is missing is
`blocked`, with the address to install it from.

## What it keeps on this machine

Everything is keyed by profile, so a rehearsal on a fork never reads or writes
what a real release left behind.

| Path | Holds |
| --- | --- |
| `<config>/rancher-desktop-release/<profile>/` | a profile other than production |
| `<config>/rancher-desktop-release/<profile>/confirmations.yaml` | the steps you have marked done |
| `<cache>/rancher-desktop-release/<profile>/<version>/` | the worktrees an action checks out, the release assets it downloads, and the facts `f` writes |

`<cache>` is `~/Library/Caches` on macOS, `~/.cache` on Linux and
`%LocalAppData%` on Windows. Your own clone never changes branch: a step that
needs a checkout makes a worktree under the cache directory, so a failed step
cannot leave you on a bump branch. The clone is still the worktree's parent,
and the fetch that feeds it writes to the clone's object store.

Nothing removes a worktree or a download afterwards. A worktree that needs its
own `yarn install` then pays for it once instead of once per attempt, and a
release asset runs to hundreds of megabytes, so the directory grows. Delete a
finished release's cache directory when you want the space back, and the next
checkout prunes the registration it leaves behind.

## What it changes outside this machine

Nothing without a confirmation. `--run` prints every operation the step would
perform, filled in with this release's values, and runs them only after you
answer yes. It stops at the first failure, because the operations after it
would build on work that did not happen.

Five of the steps below have automation. The version bump pushes a branch and
opens a pull request against the release branch; that branch goes to your fork
of the release repository, or to the release repository itself when you have no
fork of it. The release notes step shows how release-notes.md differs from the
notes of `vX.Y.Z`, then puts the file into the release. The tag step pushes the
release branch head to `refs/tags/vX.Y.Z` in the release repository, which
starts the build every release asset comes from. The package build step reruns
the jobs of that build that failed. The Linux assets step takes that build's
Linux zip, gives it the name the release uses, writes its checksum and uploads
what the release does not have. Each step names the system it reaches.

## Step reference

Placeholders stand for the release's own values: `{version}` for `1.25.0`,
`{tag}` for `v1.25.0`, `{branch}` for `release-1.25`, `{line}` for `1.25`, and
`{repo}` for the profile's repository. The step numbers come from the hand
checklist, so the steps below are not consecutive.

<!-- The reference below is generated from the step definitions. -->

### 1. Release branch

- **Applies to:** Minor releases. A patch is cut from the branch its line already has.
- **Done when:** {repo} has the branch {branch}.
- **Waits for:** gh can push to {repo}.
- **Reaches:** GitHub repo.
- **Runs:** nothing. Follow the instructions.
- **Gathers:** nothing. The instructions are all the step needs.

Push the head of main to the new branch:

    git push <url of {repo}> <head of main>:refs/heads/{branch}

Check the head commit's subject, date and checks first. It is what the release ships. The push starts the package workflow, which uploads the branch's Linux zip to the OBS bucket.

### 4. Version bump

- **Applies to:** Every release.
- **Done when:** package.json on {branch} says {version}.
- **Waits for:** gh can push to {repo}, and the release branch step is done or does not apply.
- **Reaches:** GitHub repo.
- **Runs:** Open a pull request bumping package.json to {version}.
- **Gathers:** nothing. The instructions are all the step needs.

Set the `version` field of package.json on {branch} to {version}, commit it with a sign-off, push the commit to a branch of your own, and open a pull request against {branch} titled "Bump version to {version}". A reviewer approves and merges it.

### 5. Draft release

- **Applies to:** Every release.
- **Done when:** A release named {tag} exists in {repo}, as a draft or published.
- **Waits for:** gh can push to {repo}.
- **Reaches:** GitHub repo.
- **Runs:** nothing. Follow the instructions.
- **Gathers:** nothing. The instructions are all the step needs.

Create the draft, with no --target:

    gh release create {tag} --repo {repo} --draft --title "<title>" --notes-file <file>

The title is "Rancher Desktop X.Y" for a minor release and "Rancher Desktop X.Y.Z" for a patch. Drafts are visible only to users who can push, so nobody sees the notes before the release.

### 6. Release notes

- **Applies to:** Every release.
- **Done when:** {tag} has notes, they are the ones in release-notes.md when this clone has that file, and you have marked them done. Editing them afterwards puts the step back to available.
- **Waits for:** gh can push to {repo}, and the draft release exists.
- **Reaches:** GitHub repo.
- **Runs:** Put release-notes.md into the notes of {tag}.
- **Gathers:** The bundled utilities that moved, the first-time contributors, and the changelog links, written to release-notes-facts.md under the release's cache directory.

Write the notes in release-notes.md at the top of your clone, then put them into the release:

    gh release edit {tag} --repo {repo} --notes-file release-notes.md

Start from the previous release's notes, which `gh release view <previous tag> --repo {repo}` prints, and keep the same sections: the installer links for {version}, the new contributors, what changed for the user, the bundled utilities that moved, and the compare link. Write what a user sees and leave internal work out.

Press `f` to write this release's facts out as sections to paste. nerdctl and containerd are not among them, because they ship in the guest ISO rather than in dependencies.yaml.

The file is untracked and nothing ignores it, so keep it out of your commits. Press `m` once the notes on the release are the ones to ship.

### 7a. Docs: bundled utilities

- **Applies to:** Minor releases. A patch ships the documentation its line already has, unless a bundled utility moved.
- **Done when:** The release branch of your fork of {docsRepo}, or {docsRepo}'s own main branch once the documentation is merged, has bundled-utilities-version-info/v{version}.md, the reference page imports it, and the file lists the versions {version} bundles.
- **Waits for:** gh can push to {docsRepo}, and the release branch exists.
- **Reaches:** GitHub repo, GitHub docs repo.
- **Runs:** nothing. Follow the instructions.
- **Gathers:** nothing. The instructions are all the step needs.

Write the bundled utility versions for {version} into the documentation:

1. Add docs/bundled-utilities-version-info/v{version}.md, one utility per line, in the form `name: version <br/>`.
2. Delete the oldest file in that directory, so the page lists three releases.
3. In docs/references/bundled-utilities.md, import the new file and add its row at the top of the table, and take out the deleted one.

Every version but nerdctl's comes from dependencies.yaml on {branch}. nerdctl ships in the guest images instead. The Lima image's Makefile and the WSL distribution's versions.env each set NERDCTL_VERSION, in the image release that dependencies.yaml names.

Commit it to a release-{line} branch of your fork of {docsRepo} with a sign-off. The rdctl reference and the versioning snapshot go on the same branch, and one pull request carries all three.

### 9. Tag

- **Applies to:** Every release.
- **Done when:** {tag} names a commit of {repo} whose package.json says {version}.
- **Waits for:** gh can push to {repo}, the version bump and the draft release are done, {branch} has a commit main does not, and the package run for the head of {branch} succeeded.
- **Reaches:** GitHub repo.
- **Runs:** Push {tag} to {repo}.
- **Gathers:** nothing. The instructions are all the step needs.

Tag the head of {branch}:

    git push <url of {repo}> <head of {branch}>:refs/tags/{tag}

Name the release repository by URL. In most clones `origin` is your own fork, so a tag pushed there never reaches {repo}. The push starts the package workflow for the tag, and that run builds the assets the release ships. Nothing moves a tag once it is pushed, so a tag on the wrong commit costs the version: it has to be burned, and the release goes out as the next patch.

### 10. Package build

- **Applies to:** Every release.
- **Done when:** The package workflow run for {tag} succeeded.
- **Waits for:** gh can push to {repo}, and {tag} is pushed. Nothing starts this run by hand; the tag push does.
- **Reaches:** GitHub repo.
- **Runs:** Rerun the failed jobs of the package run for {tag}.
- **Gathers:** nothing. The instructions are all the step needs.

Wait for the package run the tag push started, at

    https://github.com/{repo}/actions/workflows/package.yaml

Every release asset comes from that run. Rerun the jobs that failed if it does not pass.

### 11. Linux assets

- **Applies to:** Every release.
- **Done when:** {tag} has rancher-desktop-linux-{tag}.zip and its .sha512sum.
- **Waits for:** gh can push to {repo}, the draft release exists, {tag} is pushed, and the package run for {tag} has built its Linux zip.
- **Reaches:** GitHub repo.
- **Runs:** Upload the Linux zip and its checksum to {tag}.
- **Gathers:** nothing. The instructions are all the step needs.

Take the Linux build from the package run for {tag}:

    gh run download <run id> --repo {repo} --name "Rancher Desktop-linux.zip" --dir <dir>

gh unpacks the artifact, so the zip arrives under the name the build stamped it with. Rename it to rancher-desktop-linux-{tag}.zip. The workflow that copies the release's Linux zip to the OBS bucket builds its download URL from that name, so the release cannot keep the build's. Write the checksum beside it:

    sha512sum rancher-desktop-linux-{tag}.zip > rancher-desktop-linux-{tag}.zip.sha512sum

Leave -b off, so the line reads the way every released Linux checksum does. macOS has no sha512sum, and shasum -a 512 prints the same line. Upload both files:

    gh release upload {tag} --repo {repo} <the zip> <the checksum>

### 14. Windows assets

- **Applies to:** Every release.
- **Done when:** {tag} has Rancher.Desktop.Setup.{version}.msi and its .sha512sum.
- **Waits for:** gh can push to {repo}, the draft release exists, {tag} is pushed, and the package run for {tag} has built its Windows zip.
- **Reaches:** GitHub repo.
- **Runs:** nothing. Follow the instructions.
- **Gathers:** What the Windows signer needs, written to windows-signing.md under the release's cache directory.

The Windows signing key is a fob, so the key holder builds and signs the installer on their own machine. Press `f` for the message to send them, which names the package run the build is in.

They take the "Rancher Desktop-win.zip" artifact of that run and sign it with the SUSE code-signing certificate, as docs/development/signing.md describes. yarn sign writes the installer and its checksum under the names the release uses, so nothing has to be renamed or hashed by hand.

They can upload the two files or send them to you.
