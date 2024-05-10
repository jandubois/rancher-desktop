load '../helpers/load'

@test 'start k8s' {
    factory_reset
    start_kubernetes
    wait_for_kubelet
}

@test 'deploy sample app' {
    kubectl apply --filename - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: webapp-configmap
data:
  index: "Hello World!"
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: webapp
spec:
  replicas: 1
  selector:
    matchLabels:
      app: webapp
  template:
    metadata:
      labels:
        app: webapp
    spec:
      volumes:
      - name: webapp-config-volume
        configMap:
          name: webapp-configmap
          items:
          - key: index
            path: index.html
      containers:
      - name: webapp
        image: nginx
        volumeMounts:
        - name: webapp-config-volume
          mountPath: /usr/share/nginx/html
EOF
}

@test 'deploy ingress' {
    kubectl apply --filename - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: webapp-1
spec:
  type: ClusterIP
  selector:
    app: webapp
  ports:
  - port: 80
---
apiVersion: v1
kind: Service
metadata:
  name: webapp-2
spec:
  type: ClusterIP
  selector:
    app: webapp
  ports:
  - name: http
    port: 80
EOF
}

@test 'fail to connect to the service on localhost without port forwarding' {
    run try --max 5 curl --silent --fail "http://localhost:8080"
    assert_failure
}

@test 'forward service by port number' {
    rdctl api port_forwarding --method POST --input - <<<'{
        "namespace": "default",
        "service":   "webapp-1",
        "k8sPort":   80,
        "hostPort":  8080
    }'
    run try curl --silent --fail  "http://localhost:8080"
    assert_success
    assert_output "Hello World!"
}

@test 'forward service by port name' {
    rdctl api port_forwarding --method POST --input - <<<'{
        "namespace": "default",
        "service":   "webapp-2",
        "k8sPort":   "http",
        "hostPort":  8088
    }'
    run try curl --silent --fail  "http://localhost:8088"
    assert_success
    assert_output "Hello World!"
}

@test 'fail to connect to the service after removing port number forwarding' {
    rdctl api -X DELETE "port_forwarding?namespace=default&service=webapp-1&k8sPort=80"
    run try --max 5 curl --silent --fail "http://localhost:8080"
    assert_failure
}

@test 'fail to connect to the service after removing port name forwarding' {
    rdctl api -X DELETE "port_forwarding?namespace=default&service=webapp-2&k8sPort=http"
    run try --max 5 curl --silent --fail "http://localhost:8088"
    assert_failure
}
