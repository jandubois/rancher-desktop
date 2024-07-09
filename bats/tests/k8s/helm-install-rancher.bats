# Test case 11 & 12

load '../helpers/load'

local_setup_file() {
    RD_USE_RAMDISK=false
}

local_setup() {
    needs_port 443

    LOG_NAMESPACE=cattle-system
    LOG_APPNAME=rancher
}

# Check that the rancher-latest/rancher helm chart at the given version is
# supported on the current Kubernetes version (as determined by
# $RD_KUBERNETES_PREV_VERSION)
is_rancher_chart_compatible() {
    local chart_version=$1

    run helm show chart rancher-latest/rancher --version "$chart_version"
    assert_success || return

    run awk '/^kubeVersion:/ { $1 = ""; print }' <<<"$output"
    assert_success || return
    # We only support kubeVersion of form "< x.y.z"
    assert_output --regexp '^[[:space:]]*<[[:space:]]*[^[:space:]]+$' || return

    run awk '{ print $2 }' <<<"$output"
    assert_success || return

    local unsupported_version=$output
    semver_gt "$unsupported_version" "$RD_KUBERNETES_PREV_VERSION" || return
}

# Set (and save) $rancher_chart_version to $RD_RANCHER_IMAGE_TAG if it is set
# (and compatible), or otherwise the oldest chart version that supports
# $RD_KUBERNETES_PREV_VERSION.
# If no compatible chart version could be found, calls mark_k3s_version_skipped
# and fails the test.
determine_chart_version() {
    local rancher_chart_version
    if [[ -n $RD_RANCHER_IMAGE_TAG ]]; then
        # If a version is given, check that it's compatible.
        rancher_chart_version=${RD_RANCHER_IMAGE_TAG#v}
        if ! is_rancher_chart_compatible "$rancher_chart_version"; then
            mark_k3s_version_skipped
            printf "Rancher %s is not compatible with Kubernetes %s" \
                "$rancher_chart_version" "$RD_KUBERNETES_PREV_VERSION" |
                fail
            return
        fi
        save_var rancher_chart_version
        return
    fi
    local default_version
    default_version=$(rancher_image_tag)
    default_version=${default_version#v}

    run --separate-stderr helm search repo --versions rancher-latest/rancher --output json
    assert_success || return

    run jq_output 'map(.version).[]'
    assert_success || return

    run sort --version-sort <<<"$output"
    assert_success || return
    local versions=$output

    for rancher_chart_version in $versions; do
        if ! semver_is_valid "$rancher_chart_version"; then
            continue # Skip invalid / RC versions.
        fi
        if semver_lt "$rancher_chart_version" "$default_version"; then
            continue # Skip any versions older than the default version
        fi
        if is_rancher_chart_compatible "$rancher_chart_version"; then
            # Once we find a compatible version, use it (and don't look at the
            # rest of the chart versions).
            trace "$(printf "Selected rancher chart version %s for Kubernetes %s" \
                "$rancher_chart_version" "$RD_KUBERNETES_PREV_VERSION")"
            save_var rancher_chart_version
            return
        fi
    done
    mark_k3s_version_skipped
    printf "Could not find a version of rancher-latest/rancher compatible with Kubernetes %s\n" \
        "$RD_KUBERNETES_PREV_VERSION" |
        fail || return
}

assert_not_empty_list() {
    run "$@"
    assert_success || return
    run jq_output length
    assert_success || return
    refute_output 0 || return
}

assert_true() {
    run --separate-stderr "$@"
    assert_success || return
    assert_output --regexp '^([Tt]rue|1)$' || return
}

# Fetch pod log using LOG_NAMESPACE and LOG_APPNAME
get_pod_log() {
    run kubectl get pod --namespace "$LOG_NAMESPACE" --selector "app=$LOG_APPNAME" \
        --output=jsonpath='{.items[0].metadata.name}'
    assert_success || return
    assert_output || return
    local name="$output"

    run kubectl logs --namespace "$LOG_NAMESPACE" "$name"
    assert_success || return
    echo "$output"
}

# Given LOG_NAMESPACE and LOG_APPNAME, assert that a log line contains the given string.
assert_pod_log_line() {
    local expect="$*"
    run get_pod_log || return
    assert_output --partial "$expect" || return
}

capture_pod_log() {
    if [[ $status -eq 0 ]]; then
        return 0
    fi
    if capturing_logs; then
        local logfile
        logfile=$(unique_filename "${PATH_BATS_LOGS}/${BATS_SUITE_TEST_NUMBER}-${LOG_NAMESPACE}-${LOG_APPNAME}" .log)
        trace "capturing ${logfile}"
        get_pod_log >"$logfile"
    fi
    return 1
}

# Pull down the image manually first so we are less likely to time out when
# deploying rancher
pull_rancher_image() {
    local rancher_chart_version
    if ! load_var rancher_chart_version; then
        fail "Could not restore Rancher chart version"
    fi
    local CONTAINERD_NAMESPACE=k8s.io
    try ctrctl pull --quiet "rancher/rancher:v$rancher_chart_version"
}

_wait_for_rancher_pod() {
    try assert_pod_log_line 'Listening on :443' || return
    try assert_pod_log_line 'Starting catalog controller' || return
    try --max 60 --delay 10 assert_pod_log_line 'Watching metadata for rke-machine-config.cattle.io/v1' || return
    try --max 60 --delay 10 assert_pod_log_line 'Creating clusterRole for roleTemplate Cluster Owner (cluster-owner).' || return
    try assert_pod_log_line 'Rancher startup complete' || return
    try assert_pod_log_line 'Created machine for node' || return
}

wait_for_rancher_pod() {
    _wait_for_rancher_pod
    local rc=$status
    capture_pod_log
    return "$rc"
}

_wait_for_webhook_pod() {
    local LOG_APPNAME=rancher-webhook
    try assert_pod_log_line 'Rancher-webhook version' || return
    try assert_pod_log_line 'Listening on :9443' || return
    # Depending on version, this is either "cattle-webhook-tls" or "cattle-system/cattle-webhook-tls"
    try assert_pod_log_line 'Creating new TLS secret for cattle-' || return
    try assert_pod_log_line 'Active TLS secret cattle-' || return
    try assert_pod_log_line 'Sleeping for 15 seconds then applying webhook config' || return
}

wait_for_webhook_pod() {
    _wait_for_webhook_pod
    local rc=$status
    LOG_APPNAME=rancher-webhook capture_pod_log
    return "$rc"
}

dump_logs() {
    # Get k3s logs if possible before things fail
    kubectl get deployments --all-namespaces || :
    kubectl get pods --all-namespaces || :

    try get_pod_log
    assert_success || return
    echo "$output"

    local LOG_APPNAME=rancher-webhook
    try get_pod_log
    assert_success || return
    echo "$output"
}

deploy_rancher() {
    # TODO remove `skip_unless_host_ip` once `traefik_hostname` no longer needs it
    if is_windows; then
        skip_unless_host_ip
    fi

    local rancher_chart_version
    if ! load_var rancher_chart_version; then
        fail "Could not restore Rancher chart version"
    fi

    helm upgrade \
        --install cert-manager jetstack/cert-manager \
        --namespace cert-manager \
        --set crds.enabled=true \
        --set crds.keep=true \
        --set prometheus.enabled=false \
        --set "extraArgs[0]=--enable-certificate-owner-ref=true" \
        --create-namespace
    try assert_not_empty_list helm list --namespace cert-manager --deployed --output json --selector name=cert-manager
    wait_for_kube_deployment_available --namespace cert-manager cert-manager

    local host
    host=$(traefik_hostname)

    comment "Installing rancher $rancher_chart_version"
    # The helm install can take a long time, especially on CI.  Therefore we
    # avoid using --wait / --timeout, and instead check for forward progress
    # at each step.
    helm upgrade \
        --install rancher rancher-latest/rancher \
        --version "$rancher_chart_version" \
        --namespace cattle-system \
        --set hostname="$host" \
        --set replicas=1 \
        --create-namespace

    try assert_not_empty_list helm list --all --output json --namespace cattle-system --selector name=rancher
    try assert_not_empty_list helm list --deployed --output json --namespace cattle-system --selector name=rancher
    try kubectl get ingress --namespace cattle-system rancher
    try assert_not_empty_list kubectl get ingress --namespace cattle-system rancher --output jsonpath='{.status.loadBalancer.ingress}'

    try --max 60 --delay 10 kubectl get namespace fleet-local
    try --max 60 --delay 10 kubectl get namespace local
    try --max 60 --delay 10 kubectl get namespace cattle-global-data
    try --max 60 --delay 10 kubectl get namespace fleet-default

    try assert_not_empty_list kubectl get pods --namespace cattle-system --selector app=rancher --output jsonpath='{.items}'

    # Unfortunately, the Rancher pod could get restarted; this may lead to the
    # wait steps to fail and we need to start again from the top.
    try wait_for_rancher_pod

    try assert_true kubectl get APIServices v3.project.cattle.io --output=jsonpath='{.status.conditions[?(@.type=="Available")].status}'

    try kubectl get namespace cattle-fleet-system
    try kubectl get namespace cattle-system

    try --max 48 kubectl get deployment --namespace cattle-fleet-system fleet-controller
    try assert_kube_deployment_available --namespace cattle-fleet-system gitjob
    try assert_kube_deployment_available --namespace cattle-fleet-system fleet-controller

    try --max 60 --delay 10 assert_not_empty_list kubectl get pods --namespace cattle-system --selector app=rancher-webhook --output jsonpath='{.items}'

    # Unfortunately, the webhook pod might restart too :(
    try wait_for_webhook_pod

    dump_logs

    try --max 120 assert_kube_deployment_available --namespace cattle-system rancher
    try --max 120 assert_kube_deployment_available --namespace cattle-fleet-local-system fleet-agent
    try --max 60 assert_kube_deployment_available --namespace cattle-system rancher-webhook

    # The rancher pod sometimes falls over on its own; retry in a loop to
    # detect flapping.
    local i
    for i in {1..10}; do
        sleep 1
        try --max 60 --delay 10 assert_kube_deployment_available --namespace cattle-system rancher
    done
}

verify_rancher() {
    # TODO remove `skip_unless_host_ip` once `traefik_hostname` no longer needs it
    if is_windows; then
        skip_unless_host_ip
    fi

    dump_logs

    local host
    host=$(traefik_hostname)

    run try --max 9 --delay 10 curl --insecure --show-error "https://${host}/dashboard/auth/login"
    assert_success
    assert_output --partial 'href="/dashboard/'
    run try kubectl get secret --namespace cattle-system bootstrap-secret -o json
    assert_success
    assert_output --partial "bootstrapPassword"
}

@test 'add helm repo' {
    helm repo add jetstack https://charts.jetstack.io
    helm repo add rancher-latest https://releases.rancher.com/server-charts/latest
    helm repo update
}

foreach_k3s_version \
    determine_chart_version \
    factory_reset \
    start_kubernetes \
    wait_for_kubelet \
    wait_for_traefik \
    pull_rancher_image \
    deploy_rancher \
    verify_rancher
