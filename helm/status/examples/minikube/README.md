# Minikube example

This example installs the status app and its optional single-instance PostgreSQL StatefulSet into a dedicated `status-local` namespace. It uses the locally built `localhost/status:dev` image. PostgreSQL data is stored in a Minikube PVC.

From the repository root, export the image built by Podman and load the archive into Minikube. This works with Minikube's default `containerd` runtime:

```sh
podman save --format docker-archive -o /tmp/status-dev.tar localhost/status:dev
minikube image load /tmp/status-dev.tar
kubectl create namespace status-local
```

Create the app Secret. Replace the demo password with a local password of at least 12 characters:

```sh
kubectl -n status-local create secret generic status-secrets \
  --from-literal=SESSION_SECRET="$(openssl rand -hex 32)" \
  --from-literal=BOOTSTRAP_ADMIN_PASSWORD='local-demo-password'
```

Create the local database password Secret:

```sh
kubectl -n status-local create secret generic status-db \
  --from-literal=DATABASE_PASSWORD='local-demo-db-password'
```

Install the release:

```sh
helm upgrade --install status ./helm/status \
  --namespace status-local \
  -f ./helm/status/examples/minikube/values.yaml
```

Check the pod and forward the service port:

```sh
kubectl -n status-local get pods
kubectl -n status-local port-forward service/status 8080:80
```

Open `http://localhost:8080`. The admin username is `admin`; use the password stored in the `status-secrets` Secret. The example disables the Secure cookie flag for local HTTP port-forwarding. Do not use these values for a public deployment.
