# State backends

**Supported: S3 state with native lockfile locking** (`use_lockfile = true`), nothing else promised. There is no DynamoDB lock table support to maintain, and other backends (PostgreSQL, GCS, Azure, Consul, HTTP, HCP Terraform) are **not a goal**. S3-compatible services such as Cloudflare R2 are meant to be supported too; they come last and are not verified yet (see below).

tfpls never talks to the backend itself. It runs `tofu init`, and the backend is whatever the stack's `terraform { backend "s3" ... }` block says; credentials reach it through `TFPLS_ENV_JSON` or the OIDC role like any provider credentials. What matters is **locking**: `/tofu unlock`, the lock options and the concurrency behavior rely on OpenTofu's lock error ("Error acquiring the state lock" with ID, holder, operation and creation time).

## What has been verified

A scenario runs on **GitHub-hosted `ubuntu-latest`** against each backend, in a test repository that no longer exists: install tools with the shared `setup` action, `apply`, a second plan with no changes, hold the state lock with a slow apply, check that a second run gets a parsable lock error, kill the holder hard (what a cancelled or timed-out job does), then use the shared `unlock` action and the shared `tofu` action.

| Backend | How it was tested | Result | After a hard-killed job |
|---|---|---|---|
| S3 API, native lockfile (`use_lockfile`) | LocalStack 3.8 in a container | all steps pass | **lock is left behind**; `/tofu unlock` releases it (ID and holder shown) |
| S3 API, native lockfile | SeaweedFS (independent S3 implementation) | all steps pass | **lock is left behind**; `/tofu unlock` releases it |
| PostgreSQL (`backend "pg"`) | `postgres:17-alpine` in a container | all steps pass | **no stuck lock**: advisory locks end with the client connection; `/tofu unlock` answers "not locked" |

**Verified on real AWS:** S3 with `use_lockfile` and OIDC on GitHub-hosted runners (plans, applies, drift, locks, the `locks` task). **Not verified:** **Cloudflare R2**. The PostgreSQL row above is informational only: it shows the pipelines do not depend on the S3 lock mechanism, but PostgreSQL is not a supported backend. MinIO was also planned, but its public container images could not be pulled (Docker Hub: access denied; quay.io: 401), so SeaweedFS was used as the second S3 implementation.

## Configuring S3-compatible storage (AWS S3, Cloudflare R2, ...)

```hcl
terraform {
  backend "s3" {
    bucket       = "my-tofu-state"
    key          = "terraform/prod.tfstate"   # with tofu workspaces the key is prefixed per workspace
    region       = "us-east-1"                # R2: "auto"
    use_lockfile = true                       # native locking with a .tflock object (needs conditional writes)
    # S3-compatible services (R2, SeaweedFS, ...) also need:
    # endpoints                   = { s3 = "https://<account>.r2.cloudflarestorage.com" }
    # skip_credentials_validation = true
    # skip_requesting_account_id  = true
    # skip_metadata_api_check     = true
    # skip_region_validation      = true
    # use_path_style              = true      # when the service does not support virtual-host style
  }
}
```

Put `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` (for R2: an R2 API token pair) into `TFPLS_ENV_JSON` ([credentials](credentials.md)); the credentials must be able to write the lock object (`<key>.tflock`) next to the state.

Things to check for a service that is not in the table above:

- **Native locking needs conditional writes.** `use_lockfile` relies on the service honoring `If-None-Match` on object creation; a service that ignores it would let two runs lock at once. Check the provider's documentation, and run the scenario (below) against it before trusting it.
- **Do not use a DynamoDB lock table.** Native lockfile locking is the supported mechanism.
- A stuck lock after a killed job is a property of the backend: object-based locks stay until released (`/tofu unlock`, or the `locks` task, which shows the holder and releases a lock by name); connection-based locks (`pg`) disappear.

## Checking another backend

The scenario above is not part of this repository. To check a backend yourself, run the same sequence by hand in a throwaway repository: apply, a second plan with no changes, a slow apply holding the lock while a second run starts (it must fail with the lock error), a hard-killed holder, then `/tofu unlock`.
