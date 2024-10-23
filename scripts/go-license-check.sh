#!/usr/bin/env bash

set -o errexit -o nounset -o pipefail

go install github.com/google/go-licenses@v1.6.0

# We just copy the allowed licenses from the CNCF list:
# https://github.com/cncf/foundation/blob/main/allowed-third-party-license-policy.md#approved-licenses-for-allowlist
ALLOWED="Apache-2.0,BSD-2-Clause,BSD-2-Clause-FreeBSD,BSD-3-Clause,MIT,ISC,Python-2.0,PostgreSQL,X11,Zlib"
IGNORE=(
    # skipping our own modules because we only have a LICENSE file in the root of the repo
    "--ignore" "github.com/rancher-sandbox/rancher-desktop"
    # CNCF has approved several exceptions for MPL-2 licensed modules, including those below
    # https://github.com/cncf/foundation/blob/1e80c35/license-exceptions/cncf-exceptions-2019-11-01.spdx
    "--ignore" "github.com/hashicorp/hcl"
    "--ignore" "github.com/hashicorp/errwrap"
    "--ignore" "github.com/hashicorp/go-multierror"
)

find src/go -type d -depth 1 | while IFS= read -r DIR; do
    pushd "$DIR"
    "$(go env GOPATH)/bin/go-licenses" check --include_tests ./... --allowed_licenses "${ALLOWED}" "${IGNORE[@]}"
    popd >/dev/null
done
