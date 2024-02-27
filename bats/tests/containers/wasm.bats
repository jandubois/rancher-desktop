# shellcheck disable=SC2030,SC2031
# See https://github.com/koalaman/shellcheck/issues/2431
# https://www.shellcheck.net/wiki/SC2030 -- Modification of output is local (to subshell caused by @bats test)
# https://www.shellcheck.net/wiki/SC2031 -- output was modified in a subshell. That change might be lost

load '../helpers/load'

local_setup() {
    if using_containerd; then
        skip "this test only works on moby right now"
    fi
}

@test 'factory reset' {
    factory_reset
    rm -rf "$PATH_CONTAINERD_SHIMS"
}

hello() {
    # `hello spin rust v2 8080`
    local shim=$1
    local version=$2
    local lang=$3
    local port=$4
    local internal_port=${5:-80}

    ctrctl run -d \
        --name "${shim}-demo-${port}" \
        --runtime "io.containerd.${shim}.${version}" \
        --platform wasi/wasm \
        -p "${port}:${internal_port}" \
        "ghcr.io/deislabs/containerd-wasm-shims/examples/${shim}-${lang}-hello:v0.10.0" /
}

@test 'start engine without wasm support' {
    start_container_engine
    wait_for_container_engine
}

@test 'verify that wasm is not supported' {
    run hello spin v2 rust 8080
    assert_failure
    assert_output --partial "operating system is not supported"
}

@test 'enable wasm support' {
    pid=$(get_service_pid "$CONTAINER_ENGINE_SERVICE")
    rdctl set --experimental.container-engine.web-assembly.enabled
    try --max 15 --delay 5 refute_service_pid "$CONTAINER_ENGINE_SERVICE" "$pid"
    wait_for_container_engine
}

shim_version() {
    # `shim_version spin-v2`
    local shim=$1
    run rdctl shell "containerd-shim-${shim}" -v
    assert_success || return
    semver "$output"
}

@test 'check spin shim version >= 0.11.1' {
    run shim_version spin-v2
    assert_success
    semver_gte "$output" 0.11.1
}

@test 'deploy sample wasm app' {
    hello spin v2 rust 8080
}

check_container_logs() {
    run ctrctl logs spin-demo-8080
    assert_success || return
    assert_output --partial "Available Routes"
}

@test 'check wasm container logs' {
    try --max 5 --delay 2 check_container_logs
}

@test 'verify wasm container is running' {
    run curl --silent --fail http://localhost:8080/hello
    assert_success
    assert_output --partial "Hello world from Spin!"

    run curl --silent --fail http://localhost:8080/go-hello
    assert_success
    assert_output --partial "Hello Spin Shim!"
}

download_shim() {
    # `download_shim v2-spin`
    local shim=$1
    base_url=https://github.com/deislabs/containerd-wasm-shims/releases/download/v0.10.0
    filename="containerd-wasm-shims-${shim}-linux-${ARCH}.tar.gz"

    mkdir -p "$PATH_CONTAINERD_SHIMS"
    curl --silent --location --remote-name --output-dir "$PATH_CONTAINERD_SHIMS" "${base_url}/${filename}"
    tar xfz "${PATH_CONTAINERD_SHIMS}/${filename}" -C "$PATH_CONTAINERD_SHIMS"
    rm "${PATH_CONTAINERD_SHIMS}/${filename}"
}

@test 'install user-managed shims' {
    download_shim v2-spin
    download_shim v1-wws

    rdctl shutdown
    rdctl start
    wait_for_container_engine
}

verify_shim() {
    local shim=$1
    local version=$2
    local lang=$3
    local port=$4
    local external_port=${5:-80}

    run shim_version "${shim}-${version}"
    assert_success
    semver_eq "$output" 0.10.0

    hello "$shim" "$version" "$lang" "$port" "$external_port"
    try --max 5 --delay 2 curl --silent --fail "http://localhost:${port}/hello"
}

@test 'verify spin shim' {
    verify_shim spin v2 rust 8181
    assert_output --partial "Hello world from Spin!"
}

@test 'verify wws shim' {
    verify_shim wws v1 js 8282 3000
    assert_output --partial "Hello from Wasm Workers Server"
}
