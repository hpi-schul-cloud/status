# Status app Helm chart

Deploys the Go status application. By default, PostgreSQL is managed separately and the chart reads its URL from `database.existingSecret`. For local or small test installs, `postgresql.enabled: true` creates a single PostgreSQL StatefulSet with a PVC from the Docker Official Image. It is pinned to PostgreSQL 18.6 and follows the official PostgreSQL 18 data-directory layout. This is not a production backup or HA solution.

## Required configuration

- With the default external database mode, `database.existingSecret` and `database.secretKey` reference a Kubernetes Secret containing the PostgreSQL connection URL.
- With the local database enabled, `postgresql.auth.existingSecret` must reference a Secret containing `postgresql.auth.passwordKey`.
- `secrets.existingSecret` references a Secret containing `SESSION_SECRET` and `BOOTSTRAP_ADMIN_PASSWORD` (or the configured key names).
- Configure `page.title`, `page.logoUrl`, links, `page.groups`, and `page.components` for the instance.
- Enable `ingress` and configure TLS to expose the public status page and API.
- The public API is read-only; it includes current component state and recent status-change history. Status edits happen in the authenticated admin UI, where multiline messages can be revised later.

See [the application README](../../README.md) for build, API, and test instructions. The [Minikube example](examples/minikube/README.md) enables the local database.
