# Status service

Small manual status page for Schulcloud instances. One Go process serves the public German page, admin UI, and JSON API. Status changes are written to PostgreSQL and do not depend on uptime checks or heartbeat pings.

## Why a small custom service

The requirement is a manually maintained status page: operators set a component state and message, while the public page and API show current status and incident history. Existing platforms such as Cachet provide a broader set of monitoring, subscriber, notification, and extension features. Those features are useful in other deployments but would add dependencies and operational work here without being required; automated uptime checks are not part of this design.

This service keeps the runtime to one static Go binary, PostgreSQL, and embedded HTML/CSS. There is no Node build step, background worker, or separate monitoring service. The application has two direct Go dependencies: `pgx` for PostgreSQL and `x/crypto` for bcrypt. Testcontainers and its dependencies are isolated in `integration-tests/go.mod` and are not included in the application image. Production uses the platform's separately managed PostgreSQL service, avoiding a second database lifecycle inside each status deployment.

## Build and run

Requirements: Go 1.27 or newer and PostgreSQL 14 or newer.

```sh
make deps
export DATABASE_URL='postgres://status:password@localhost:5432/status?sslmode=disable'
export SESSION_SECRET="$(openssl rand -base64 48)"
export COOKIE_SECURE=false # Local HTTP only; leave secure cookies enabled behind HTTPS.
export BOOTSTRAP_ADMIN_USER=admin
export BOOTSTRAP_ADMIN_PASSWORD='set-a-long-random-password'
go run ./cmd/statusd
```

The first startup creates the schema, inserts the bootstrap administrator if that account does not exist, and seeds page/component configuration. Page branding and component labels/order are reconciled from `STATUS_INITIAL_CONFIG` at startup; current component statuses and messages are preserved. Passwords are not overwritten on restart. Provide `STATUS_INITIAL_CONFIG` as JSON with this shape:

```json
{
  "pageTitle": "Schul-Cloud Brandenburg System Status",
  "logoUrl": "https://example.org/status-logo.svg",
  "intro": "Hier finden Sie den aktuellen Status unserer Systeme.",
  "supportEmail": "support@example.org",
  "privacyUrl": "https://example.org/privacy",
  "imprintUrl": "https://example.org/imprint",
  "groups": [{"name":"Schul-Cloud Brandenburg"}],
  "components": [{"id":"server","name":"Server","componentGroup":"Schul-Cloud Brandenburg","description":"","link":""}]
}
```

The admin page is at `/login`. Admins can set component statuses, create operators, and change page branding through Helm values. Operators can also change component statuses. Status messages are multiline and remain editable from the admin page. Users can change their own passwords after signing in. There are no API tokens.

## HTTP API

Public read endpoints:

- `GET /api/v1/status` returns page settings, overall status, component groups, component states, and recent status-change history in `incidents`.
- `GET /api/v1/components` returns the same read-only status data.
- `GET /api/v1/ping` returns a Cachet-compatible ping response for simple existing health clients.

The public API is read-only. Status changes are made by signed-in operators in the admin screen. Supported states are `operational`, `degraded`, `partial_outage`, `major_outage`, and `maintenance`.

Health endpoints: `/healthz` checks process liveness and `/readyz` checks PostgreSQL connectivity.

## Tests

```sh
make test
make test-integration
```

Unit tests use only the Go test runner. Integration tests use Testcontainers to start PostgreSQL and require Docker (or a compatible container runtime configured for Testcontainers).

## Container

```sh
docker build -t localhost/status:dev .
```

The image contains a static Go binary on a distroless non-root base. The runtime does not need a writable filesystem.

## Release

GitHub Actions runs the unit and PostgreSQL integration tests and validates the Helm chart. To release a version, set `version` and `appVersion` in `helm/status/Chart.yaml` to the same new `X.Y.Z` value and merge the change to `main`. Actions builds and publishes `ghcr.io/hpi-schul-cloud/status:X.Y.Z`, then publishes the `status` chart to this repository's GitHub Pages Helm repository at `https://hpi-schul-cloud.github.io/status/`. Install it as `hpi-schul-cloud/status`. The action creates a GitHub Release and maintains the Helm index; the repository's Pages source must be configured to serve the `gh-pages` branch once.

## Configuration and deployment

`helm/status` deploys the service and exposes one public HTTPS ingress. Set the logo URL in `page.logoUrl` in the Helm values file (for example, `helm/status/examples/minikube/values.yaml`). Production values use a separately managed PostgreSQL instance and a Kubernetes Secret containing `DATABASE_URL`, `SESSION_SECRET`, and `BOOTSTRAP_ADMIN_PASSWORD`. The optional single-instance PostgreSQL StatefulSet uses the Docker Official Image and is for local/test deployments; it is disabled by default. Do not put database or session secrets in Helm values.

The chart seeds page branding and the component catalog once. Runtime component status is durable in PostgreSQL and shared by all application replicas. Back up PostgreSQL using the existing platform database backup process.
