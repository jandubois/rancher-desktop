#!/usr/bin/env bash

# Reset a rehearsal of the release checklist, so the next run starts again
# from step 1. It lists what a rehearsal left on its repositories and on this
# machine, asks, and deletes it. REHEARSAL.md describes the setup.
#
# Usage: reset-rehearsal.sh <profile>

set -o errexit -o nounset -o pipefail

# line is the release line every rehearsal uses.
line=1.99

die() {
    echo "$*" >&2
    exit 1
}

lowercase() {
    tr '[:upper:]' '[:lower:]' <<<"$1"
}

# find_store sets the directories the tool keeps a profile's files in, which
# Go's os.UserConfigDir and os.UserCacheDir name.
find_store() {
    case $(uname -s) in # BSD uname doesn't support long option `--kernel-name`
        Darwin)
            config_dir="$HOME/Library/Application Support/rancher-desktop-release/$profile"
            cache_dir="$HOME/Library/Caches/rancher-desktop-release/$profile"
            ;;
        Linux)
            config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/rancher-desktop-release/$profile"
            cache_dir="${XDG_CACHE_HOME:-$HOME/.cache}/rancher-desktop-release/$profile"
            ;;
        *) die "reset-rehearsal.sh runs on macOS and Linux only." ;;
    esac
}

# read_profile sets the repositories and the documentation clone the profile
# names, and refuses any repository of the real release.
read_profile() {
    local file="$config_dir/profile.yaml" settings="$config_dir/settings.yaml" named
    [[ -f $file ]] || die "There is no profile at $file."

    repo=$(yq '.github.repo // ""' "$file")
    docs_repo=$(yq '.github.docsRepo // ""' "$file")
    [[ -n $repo ]] || die "$file names no github.repo."

    for named in "$repo" "$docs_repo"; do
        case $(lowercase "$named") in
            rancher-sandbox/*) die "$file names $named, a repository of the real release." ;;
        esac
    done

    docs_clone=""
    if [[ -f $settings ]]; then
        docs_clone=$(yq '.docsClone // ""' "$settings")
    fi
}

# fork_of prints where the tool pushes a step's own branch, which is the user's
# fork of a repository, or the repository itself when the user has none. A
# repository of the user's with the same name that is not a fork of it is
# never touched.
fork_of() {
    local candidate="$login/${1#*/}" parent
    if ! parent=$(gh api "repos/$candidate" --jq .parent.full_name 2>&1); then
        [[ $parent == *"HTTP 404"* ]] || die "Looking for your fork of $1: $parent"
        echo "$1"
        return
    fi
    if [[ $(lowercase "$parent") == $(lowercase "$1") ]]; then
        echo "$candidate"
    else
        echo "$1"
    fi
}

# refs_with_prefix prints the refs of a repository whose names start with a
# prefix, such as refs/tags/v1.99.
refs_with_prefix() {
    gh api "repos/$1/git/matching-refs/${2#refs/}" --jq '.[].ref'
}

# ref_named prints a ref of a repository if it exists. GitHub matches refs by
# prefix, so asking for release-1.99 also finds release-1.990.
ref_named() {
    gh api "repos/$1/git/matching-refs/${2#refs/}" --jq ".[].ref | select(. == \"$2\")"
}

# act lists what it would do, or does it once the user has agreed.
act() {
    local description=$1
    shift
    if [[ $mode == list ]]; then
        [[ $found == yes ]] || echo "Resetting the $profile rehearsal will:"
        echo "  $description"
    else
        echo "$description"
        "$@"
    fi
    found=yes
}

# reset walks everything a rehearsal leaves behind, listing it or deleting it
# as the mode says. GitHub cannot delete a pull request, so it closes them.
reset() {
    mode=$1
    found=no

    local prs releases refs ref tags tag number clone
    prs=$(gh pr list --repo "$repo" --state open --base "release-$line" --json number --jq '.[].number')
    for number in $prs; do
        act "close pull request #$number in $repo" gh pr close "$number" --repo "$repo"
    done
    if [[ -n $docs_repo ]]; then
        prs=$(gh pr list --repo "$docs_repo" --state open --head "release-$line" --json number --jq '.[].number')
        for number in $prs; do
            act "close pull request #$number in $docs_repo" gh pr close "$number" --repo "$docs_repo"
        done
    fi

    releases=$(gh release list --repo "$repo" --limit 1000 --json tagName \
        --jq ".[].tagName | select(startswith(\"v$line.\") or startswith(\"burned-v$line.\"))")
    for tag in $releases; do
        act "delete the release $tag in $repo" gh release delete "$tag" --repo "$repo" --yes
    done

    refs=$(refs_with_prefix "$repo" "refs/tags/v$line.")
    refs+=$'\n'$(ref_named "$repo" "refs/heads/release-$line")
    for ref in $refs; do
        act "delete $ref in $repo" gh api --method DELETE "repos/$repo/git/$ref" --silent
    done
    refs=$(refs_with_prefix "$fork" "refs/heads/bump-to-$line.")
    for ref in $refs; do
        act "delete $ref in $fork" gh api --method DELETE "repos/$fork/git/$ref" --silent
    done
    if [[ -n $docs_repo ]]; then
        refs=$(ref_named "$docs_fork" "refs/heads/release-$line")
        for ref in $refs; do
            act "delete $ref in $docs_fork" gh api --method DELETE "repos/$docs_fork/git/$ref" --silent
        done
    fi

    for clone in "${clones[@]}"; do
        tags=$(git -C "$clone" tag --list "v$line.*")
        for tag in $tags; do
            act "delete the tag $tag in $clone" git -C "$clone" tag --delete "$tag"
        done
    done
    if [[ -d $cache_dir ]]; then
        act "delete $cache_dir, with the worktrees and downloads in it" rm -rf "$cache_dir"
        for clone in "${clones[@]}"; do
            act "prune the worktrees $clone no longer has" git -C "$clone" worktree prune
        done
    fi
    if [[ -f $config_dir/confirmations.yaml ]]; then
        act "delete the steps marked done, in $config_dir/confirmations.yaml" \
            rm -f "$config_dir/confirmations.yaml"
    fi
}

[[ $# -eq 1 ]] || die "Usage: $0 <profile>"
profile=$1
if [[ $(lowercase "$profile") == production ]]; then
    die "The production profile is the real release; reset-rehearsal.sh resets rehearsals only."
fi

find_store
read_profile
clones=("$(git -C "$(dirname "$0")" rev-parse --show-toplevel)")
if [[ -n $docs_clone ]]; then
    clones+=("$docs_clone")
fi
login=$(gh api user --jq .login)
fork=$(fork_of "$repo")
docs_fork=""
if [[ -n $docs_repo ]]; then
    docs_fork=$(fork_of "$docs_repo")
fi

reset list
if [[ $found == no ]]; then
    echo "The $profile rehearsal has nothing to reset."
    exit
fi
read -r -p "Go ahead? [y/N] " answer || answer=""
[[ $answer == [yY] ]] || die "Nothing was deleted."
reset delete
