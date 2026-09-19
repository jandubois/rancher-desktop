# release-checklist

`yarn release` drives a Rancher Desktop 1.x release. It works out which
release is in progress, checks each step it ships against the system that
would show it done, and prints one line per step with its state. It will
replace the release checklist the team follows by hand; this version ships
two of that checklist's steps.

Run it from a clone of the repository:

    yarn release

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

Every check reads the system that would show the step done, on every run. The
tool keeps no record of what it has done, so it notices work done by hand or
out of order, and a step somebody undid goes back to `available`.

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
the refs, which the tool reads before the checklist prints. Each step declares
which systems it reaches, the tool probes each
one once per run, and a step whose tool is missing is `blocked`, with the
address to install it from.

## What it changes outside this machine

Nothing yet. Every step in this version only reads. Each step below names the
system it reaches.

## Step reference

Placeholders stand for the release's own values: `{version}` for `1.25.0`,
`{tag}` for `v1.25.0`, `{branch}` for `release-1.25`, `{line}` for `1.25`, and
`{repo}` for the profile's repository. The step numbers come from the hand
checklist, so the two steps below are not consecutive.

<!-- The reference below is generated from the step definitions. -->

### 1. Release branch

- **Applies to:** Minor releases. A patch is cut from the branch its line already has.
- **Done when:** {repo} has the branch {branch}.
- **Waits for:** gh can push to {repo}.
- **Reaches:** GitHub repo.

Push the head of main to the new branch:

    git push <url of {repo}> <head of main>:refs/heads/{branch}

Check the head commit's subject, date and checks first. It is what the release ships. The push starts the package workflow, which uploads the branch's Linux zip to the OBS bucket.

### 5. Draft release

- **Applies to:** Every release.
- **Done when:** A release named {tag} exists in {repo}, as a draft or published.
- **Waits for:** gh can push to {repo}.
- **Reaches:** GitHub repo.

Create the draft, with no --target:

    gh release create {tag} --repo {repo} --draft --title "<title>" --notes-file <file>

The title is "Rancher Desktop X.Y" for a minor release and "Rancher Desktop X.Y.Z" for a patch. Drafts are visible only to users who can push, so nobody sees the notes before the release.
