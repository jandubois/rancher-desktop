# Probe: reach a published port from the host's WSL adapter address while a
# container network inside the VM contains that address.

load '../helpers/load'

ip_to_int() { # <dotted-quad>
    local IFS=.
    local octets
    read -r -a octets <<<"$1"
    echo $(((octets[0] << 24) | (octets[1] << 16) | (octets[2] << 8) | octets[3]))
}

ip_in_subnet() { # <ip> <cidr>
    local ip
    ip=$(ip_to_int "$1")
    local net
    net=$(ip_to_int "${2%/*}")
    local mask=$(((0xffffffff << (32 - ${2#*/})) & 0xffffffff))
    (((ip & mask) == (net & mask)))
}

@test 'factory reset' {
    factory_reset
}

@test 'start container engine' {
    start_container_engine
    wait_for_container_engine
}

@test 'run nginx on a network that contains HOST_IP' {
    skip_unless_host_ip
    run -0 ctrctl network inspect bridge --format '{{(index .IPAM.Config 0).Subnet}}'
    if ip_in_subnet "${HOST_IP}" "${output}"; then
        # moby refuses a subnet that overlaps docker0, but docker0 already
        # contains HOST_IP, and that is all this test needs.
        network=bridge
    else
        network=host-ip-bridge
        ctrctl network create --subnet "${HOST_IP%.*}.0/24" --gateway "${HOST_IP}" "${network}"
    fi
    trace "HOST_IP ${HOST_IP}, docker0 ${output}, nginx on ${network}"
    ctrctl run --detach --name web --network "${network}" --publish 8000:80 "${IMAGE_NGINX}"
}

@test 'reach the port via localhost' {
    try --max 9 --delay 10 curl --silent --show-error http://localhost:8000
    assert_success
    assert_output --partial "Welcome to nginx!"
}

@test 'reach the port via HOST_IP' {
    skip_unless_host_ip
    try --max 9 --delay 10 curl --silent --show-error "http://${HOST_IP}:8000"
    assert_success
    assert_output --partial "Welcome to nginx!"
}
