# Self-hosted deployment from `main`

The repository includes a disabled-by-default GitHub Actions workflow at
`.github/workflows/deploy-selfhost.yml`. When activated, it runs on a runner
installed on the local Docker host, builds both images from the exact merge
commit, and updates the existing `docker-compose.selfhost.yml` installation.
It does not publish images or copy secrets through GitHub.

## Activation

The owner must perform these steps once:

1. Install and register a GitHub Actions self-hosted runner on the Docker host
   with labels `self-hosted`, `linux`, and `multica-selfhost`. Give the runner
   account access to Docker and only the installation directory.
2. Configure the runner workspace as the existing Compose project directory,
   check out this repository there, and keep its `origin` pointed at the fork.
   Keep `.env` and Docker volumes outside Git tracking; `.env` remains the
   source of all credentials. The workflow deliberately disables checkout
   cleaning so those files survive a run.
3. Verify the existing installation manually with
   `docker compose -f docker-compose.selfhost.yml ps` and confirm that the
   runner can run `docker compose` without `sudo`.
4. Set the repository Actions variable
   `MULTICA_SELFHOST_DEPLOY_ENABLED` to the exact value `true`.
5. Merge this change and observe the first deployment. A refresh shows the new
   frontend; the workflow never reloads browsers or discards user input.

The workflow is otherwise skipped. The runner must not be exposed to
untrusted pull requests: it is only eligible for the protected `main` push.
Keep the repository's default-branch and environment protections enabled.

## Safety and operation

Deployments are serialized. Each queued run re-checks `origin/main` after it
gets the deployment lock, so a stale merge cannot overwrite a newer merge.
The state file `.multica-deploy/current` records the actual application image
tag and the workflow prints it after each run. Image tags are full commit SHAs,
not `latest`.

Before the backend container starts, the script writes a local custom-format
PostgreSQL dump to `.multica-deploy/backups/<commit>.dump`. The existing backend
entrypoint then runs the normal migrations; `/healthz` must report ready before
the run succeeds. The existing `pgdata` and `backend_uploads` volumes and all
`.env` values are retained.

For a failed build, no `compose up` occurs. For a failed migration or readiness
check, the script leaves the failed services and backup in place and does not
automatically roll back images: a schema change may not be reversible. Inspect
the logs and backup, then use an explicitly reviewed application-only rollback
on the host if the schema is compatible:

```sh
scripts/deploy-selfhost.sh rollback <previous-40-character-commit-sha>
```

That command never restores the database. Restoring the database dump is a
separate, destructive operator action requiring a maintenance window and an
explicit compatibility decision.

Expected interruption is the time for Compose to recreate backend/frontend
containers and for migrations to complete; normally seconds to a few minutes.
