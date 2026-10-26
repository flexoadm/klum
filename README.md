klum - Kubernetes Lazy User Manager
========

![AI-assisted development](https://img.shields.io/badge/development-AI--assisted-blue)

This fork is developed with AI assistance. AI tools help prepare code changes,
tests, and documentation; maintainers remain responsible for reviewing and
validating contributions before merging.

klum does the following basic tasks:

* Create/Delete/Modify users
* Easily manage roles associated with users
* Issues kubeconfig files for users to use

This is a very simple controller that just create service accounts under the hood. Properly
configured this should work on any Kubernetes cluster.

## Installation

```sh
kubectl apply -f https://raw.githubusercontent.com/ibuildthecloud/klum/master/deploy.yaml
```

## Usage
 
### Create User

```yaml
kind: User
apiVersion: klum.cattle.io/v1alpha1
metadata:
  name: darren
```

### Download Kubeconfig
```shell script
kubectl get kubeconfig darren -o json | jq .spec > kubeconfig
kubectl --kubeconfig=kubeconfig get all
```
The name of the kubeconfig resource will be the same as the user name

### Delete User
```shell script
kubectl delete user darren
```

### Assign Roles
```yaml
kind: User
apiVersion: klum.cattle.io/v1alpha1
metadata:
  name: darren
spec:
  clusterRoles:
  - view
  roles:
  - namespace: default
    # you can assign cluster roles in a namespace
    clusterRole: cluster-admin
  - namespace: other
    # or assign a role specific to that namespace
    role: something-custom
```

If you don't assign a role a default role will be assigned to the user which is
configured on the controller.  The default value is cluster-admin, so change
that if you want a more secure setup.

### Disable user
```yaml
kind: User
apiVersion: klum.cattle.io/v1alpha1
metadata:
  name: darren
spec:
  enabled: false
```

When the user is reenabled a new kubeconfig with new token will be created.

## Configuration
The controller can be configured as follows.  You will need to edit the deployment and change
then environment variables:

```shell script
GLOBAL OPTIONS:
   --namespace value             Namespace to create secrets and SAs in (default: "klum") [$NAMESPACE]
   --context-name value          Context name to put in Kubeconfigs (default: "default") [$CONTEXT_NAME]
   --server value                The external server field to put in the Kubeconfigs (default: "https://localhost:6443") [$SERVER_NAME]
   --ca value                    The value of the CA data to put in the Kubeconfig [$CA]
   --default-cluster-role value  Default cluster-role to assign to users with no roles (default: "cluster-admin") [$DEFAULT_CLUSTER_ROLE]
```

## Building

Use Go **1.26.10** (minimum Go 1.26.0). Kubernetes clients and generators are
pinned to v0.36.5, with Wrangler v3.7.1. Dependencies are managed with `go.mod`
and `go.sum`; the first build requires network access to download them.

```sh
./scripts/build
# Or build the executable directly:
go build -o bin/klum .
```

`make` builds the binary directly with the local Go installation.
`make ci` builds, runs unit tests, validates Go code, and checks that generated
clients match the committed sources. It does not require Docker.

To build a local Linux container (including when running on macOS):

```sh
make package
# Optional image name and tag:
REPO=klum TAG=dev make package
```

`GOOS`, `GOARCH` and `GOARM` can be set for binary cross-compilation. The container
packaging script always builds for Linux.

## Local checks

```sh
./scripts/test
./scripts/validate
```

Unit tests cover generated user resources, CRD validation and preservation of API
fields under structural-schema pruning. Validation runs `go vet` and checks Go
formatting; it does not require golangci-lint.

To regenerate Kubernetes clients and controllers after changing API types:

```sh
go generate .
go mod tidy
```

### Kubernetes API compatibility tests

Install Docker and kind **v0.33.0**, then run:

```sh
./scripts/test-integration
```

To test a single minor version:

```sh
./scripts/test-integration 1.36
```

Without arguments, the script creates and deletes a dedicated cluster for each pinned Kubernetes
image: **1.33.12, 1.34.8, 1.35.5 and 1.36.1**. It writes kubeconfig into a temporary
directory and does not use or modify your existing cluster context. Expect image
downloads on the first run. Both amd64 and arm64 images are supported.

These tests exercise CRD creation and update, the User status subresource,
generated clients, Kubeconfig persistence, and core/RBAC resource creation against
real API servers. Unit tests use Kubernetes v0.36.5 validation libraries; the
cluster matrix checks actual API compatibility across the four minor versions.

This dependency update does **not** fix token Secret auto-generation: the original
controller still waits for a ServiceAccount token Secret, which modern Kubernetes
does not create automatically. End-to-end kubeconfig issuance and token rotation
will be addressed separately; passing these API tests does not verify that flow.

![](https://media.giphy.com/media/3o7TKGMZHi73yzCumQ/giphy.gif)


## CI and releases

GitHub Actions is configured in `.github/workflows/ci.yml`. It uses standard
GitHub-hosted `ubuntu-24.04` and `ubuntu-24.04-arm` runners. No self-hosted runner,
Drone server or Dapper installation is required.

On branch pushes, pull requests and manual runs, the workflow:

1. Builds, runs unit tests, validates code and checks generated sources on x64
   and ARM64. It also validates the workflow with actionlint.
2. Runs Kubernetes 1.33–1.36 API tests in four independent kind jobs.
3. Produces Linux `klum-amd64`, `klum-arm64`, and `klum-arm` (ARMv7) binaries.
   ARMv7 is cross-compiled on the x64 runner.
4. Builds an OCI container image for `linux/amd64`, `linux/arm64` and `linux/arm/v7`
   from those binaries, without publishing. No QEMU is needed because the image
   has no build-time commands to execute on the target platform.

Binary artifacts are retained for seven days. Pull request checks have read-only
repository permissions and do not require repository secrets. Actions are pinned
to commit SHAs.

### Publishing

A push of a tag such as `v0.1.0` runs the same checks, then publishes:

- a multi-platform image to `ghcr.io/<owner>/<repository>:v0.1.0`;
- the `latest` image tag for releases without a prerelease suffix;
- a GitHub Release containing the three Linux binaries and `SHA256SUMS`.

Tags must have the form `vMAJOR.MINOR.PATCH`, optionally followed by a prerelease
suffix (for example, `v0.1.0-rc.1`). Tags with a prerelease suffix produce GitHub
prereleases and do not update `latest`. Re-running publication replaces release
assets with matching names; immutable GitHub releases cannot be overwritten.

Only the tag publication job receives `contents: write` and `packages: write`.
It authenticates using the automatic `GITHUB_TOKEN`; Docker Hub credentials and a
separate GitHub token are not needed. The owner/repository name is taken from the
running repository, not hard-coded to the upstream project. For a newly created
GHCR package, configure package visibility/access in GitHub if anonymous pulls
are required.

Publishing is triggered by pushing the tag, not by running a local release
script. There is no automatic deployment to a Kubernetes cluster.

## Running

`./bin/klum --kubeconfig=${HOME}/.kube/config`

## License

Licensed under Apache-2.0. See [LICENSE](LICENSE).
