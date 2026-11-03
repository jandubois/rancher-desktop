# Probe: reach a published port from the host's WSL adapter address while a
# container network inside the VM uses that address as its gateway.

load '../helpers/load'

@test 'factory reset' {
    factory_reset
}

@test 'start container engine' {
    start_container_engine
    wait_for_container_engine
}

@test 'run nginx on a network whose gateway is HOST_IP' {
    skip_unless_host_ip
    ctrctl network create --subnet "${HOST_IP%.*}.0/24" --gateway "${HOST_IP}" host-ip-bridge
    ctrctl run --detach --name web --network host-ip-bridge --publish 8000:80 "${IMAGE_NGINX}"
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
