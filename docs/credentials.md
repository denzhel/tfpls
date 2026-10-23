# Credentials: one IAM role

Every job (plan, apply, drift, locks, unlock) uses the **same** credentials: one IAM role that can plan and apply. There is nothing else to configure.

## Setup

OIDC (preferred, no stored keys): create the role (below) and set one repository variable.

```bash
gh variable set TFPLS_ROLE_ARN --body arn:aws:iam::123456789012:role/tfpls
```

Or one secret holding a JSON map of environment variables (long-lived keys, other clouds, backend settings):

```bash
gh secret set TFPLS_ENV_JSON < env.json      # {"AWS_ACCESS_KEY_ID":"...","AWS_SECRET_ACCESS_KEY":"...","AWS_REGION":"us-east-1"}
```

Both can be used together: the role's short-lived keys are written after the map, so they win, and the map then only needs non-secret settings (region, endpoint URL). The caller passes secrets with `secrets: inherit` (the one `onboard` writes does) and needs `id-token: write` for OIDC.

The role comes from the variable `TFPLS_ROLE_ARN` (an Environment variable of the same name wins inside jobs bound to that environment, see below). A custom STS endpoint (localstack, VPC endpoints) goes into the JSON secret as `AWS_ENDPOINT_URL_STS`; the token audience is `sts.amazonaws.com`.

Every job logs `credentials: env | oidc | none` and writes it to the job summary. Secret values are masked in logs; labels (`TFPLS_*`), `*_REGION` keys and values shorter than 6 characters are not.

## Self-hosted runners with their own AWS identity (not yet tested)

If the runner already has AWS credentials, you can skip OIDC: set neither `TFPLS_ROLE_ARN` nor `TFPLS_ENV_JSON`. The job logs `credentials: none` and `tofu` uses the standard AWS credential chain, so whatever the runner provides is used: an instance profile, or on EKS a Kubernetes service account annotated with an IAM role (IRSA, or EKS Pod Identity). That role needs what is described below, and the same warning applies: the plan runs the code of the pull request, so it runs with that identity.

**Not verified.** tfpls has only been run against real AWS through GitHub OIDC on GitHub-hosted runners. This path rests on how the AWS SDK finds credentials; nothing in this repository has run it on a real runner yet. Try it on a throwaway workspace first, and please report what you see.

## What the role needs

Whatever your configuration needs to plan and apply: access to the S3 state and its lock object, and the permissions of the resources you manage. tfpls does not narrow it. For a read-only plan role see [Resource read is more than `Describe*`](#resource-read-is-more-than-describe) below.

## The trust policy

In AWS, create the GitHub OIDC provider (`token.actions.githubusercontent.com`, audience `sts.amazonaws.com`) and give the role a trust policy on the token's **subject** (`sub`). The workflow only asks for the role; the trust policy is what keeps everyone else out. Restrict it to this repository.

What GitHub sends (read from real tokens in a test repository):

| Job | `sub` |
|---|---|
| plan on a pull request (`plan.yml`) | `repo:OWNER@OWNERID/REPO@REPOID:pull_request` |
| `/tofu plan`, `/tofu apply`, `/tofu unlock` (comment) | `repo:OWNER@OWNERID/REPO@REPOID:ref:refs/heads/main` |
| merge apply, drift, locks | `repo:OWNER@OWNERID/REPO@REPOID:ref:refs/heads/main` |
| the same jobs when `apply_environment` is set (apply, unlock, drift remediation, the `locks` task) | `repo:OWNER@OWNERID/REPO@REPOID:environment:NAME` |

The subject carries the numeric owner and repository IDs for repositories created after GitHub's change; older ones still get `repo:OWNER/REPO:...`. Copy the `sub` your repository really gets (decode the token payload) rather than guessing.

```json
{ "Effect": "Allow",
  "Principal": { "Federated": "arn:aws:iam::123456789012:oidc-provider/token.actions.githubusercontent.com" },
  "Action": "sts:AssumeRoleWithWebIdentity",
  "Condition": { "StringEquals": { "token.actions.githubusercontent.com:aud": "sts.amazonaws.com" },
                 "StringLike":   { "token.actions.githubusercontent.com:sub": ["<sub of the pull_request row>", "<sub of the main row>"] } } }
```

## What this means for pull requests

A pull request plan runs **code written in the pull request** (provider plugins it pins, `data "external"` and other data sources, module sources), and it runs with this role. So anyone who can open a pull request **in this repository** can run code with the role's permissions. What stands in the way:

- pull requests from forks never run plans (the fork guard),
- who may push branches or open pull requests here is your repository's access control,
- changes to workflows and providers should need review (branch protection, CODEOWNERS),
- `/tofu apply` needs an approving review of the current commit by someone other than the author ([approvals.md](approvals.md)),
- the role's trust policy limits it to this repository.

If that is not acceptable for a repository, do not use it for untrusted contributors.

## Optional: a required-reviewers gate on applies (`apply_environment`)

```yaml
jobs:
  tofu:
    uses: denzhel/tfpls/.github/workflows/tofu.yml@v0
    with: { apply_environment: tofu-apply }
    secrets: inherit
```

Jobs that can apply (`/tofu apply`, `/tofu unlock`, merge apply, drift remediation, the `locks` task) then declare that GitHub Environment and wait until its required reviewers approve. Plan jobs never use an environment, so the role and the secret must be repository-level (an environment secret would not reach plans). Availability (GitHub's rules): environments and deployment branches of private repositories need GitHub Pro or Team; the approval gate itself (**required reviewers**, wait timers) needs GitHub Enterprise on private repositories, and is free on public ones. On other plans the rules are ignored. The gate is GitHub's feature, not tfpls': it has been exercised only on a public repository.

## Optional: approvals that cannot be bypassed (a read-only plan role)

With one role, the approval rules are advisory: the plan of a pull request runs the pull request's code with a role that can also apply. To close that gap without any new setting, use two roles and the GitHub Environment of `apply_environment`:

1. A **read-only** role (state read, resource read, no writes) as the repository variable `TFPLS_ROLE_ARN`. Its trust policy allows the `pull_request` subject **and** `ref:refs/heads/<default branch>` (drift checks, `/tofu plan` and the `locks` report run from the default branch), each in both subject forms (see the trust-policy section). Branch workflows of other branches are denied. Give it `s3:PutObject` on `*.tflock` objects only, so plans can take the state lock when `plan_lock: true`.
2. An **apply** role as a variable with the same name, `TFPLS_ROLE_ARN`, on the Environment named by `apply_environment` (Settings, Environments, Variables). Inside a job bound to an environment its variable wins over the repository one. Its trust policy allows only the `environment:NAME` subject, so no pull request job can assume it.

Plan jobs have no environment, so they get the read-only role; applies, unlocks, remediation and the `locks` task declare the environment and get the apply role. A read-only plan role also cannot take the state lock in S3 if you use `use_lockfile`: give it `s3:PutObject` on the `*.tflock` objects only.

This is written from GitHub's rules and the way the workflows read the variable; it has **not** been run here (environments of private repositories need GitHub Pro or Team, and reviewer gates need Enterprise). Try it on a scratch workspace first.

### Resource read is more than `Describe*`

A plan refreshes every resource in the state. For some resources the refresh calls a `Get*` or `List*` action that a `Describe*` wildcard does not cover, so a role with only `ec2:Describe*` plans fine until such a resource is in the state, and from then on **every plan of that workspace fails**. Example: a managed prefix list (`aws_ec2_managed_prefix_list`) needs `ec2:GetManagedPrefixListEntries`.

What you see: the plan comment says `plan failed`, and the run log has a line like `Error: reading EC2 Managed Prefix List (pl-...) Entries: ... StatusCode: 403`. The fix is to add the action named in the error to the read-only role (read actions only). The apply role is not affected, because it has the write actions and usually the read ones too. See [the runbook](runbooks.md#every-plan-of-a-workspace-fails-with-a-403-on-refresh).

This is written from the failure seen in the sandbox of this repository (a read-only role with `ec2:Describe*` and state access, one prefix list in the state).

## Rotating keys

Update the secret (`TFPLS_ENV_JSON`) or the role, nothing else; the next job picks it up. With OIDC there is nothing to rotate.

## Verified

The token exchange (token request, STS call, masking, errors) against fake GitHub and STS servers in `internal/credentials/oidc_test.go`, and **against real AWS**: in a test repository (GitHub-hosted runners, account in `eu-north-1`) the variable `TFPLS_ROLE_ARN` pointed at a role whose trust policy names only that repository (the subject with owner and repository IDs, plus the plain `repo:OWNER/REPO:*` form) and has `AdministratorAccess`. Every job logged `credentials: oidc`: PR plans of three workspaces, `/tofu apply` by comment, merge applies, drift, the `locks` task and the dashboard all assumed the role through GitHub's token and used an S3 state bucket with native locking (`use_lockfile`). Creating and resizing a VPC, a NAT gateway and an EC2 instance, and removing a NAT gateway, worked end to end through the pipeline; the final destroy was run locally with an IAM user profile. Not verified: trust-policy conditions for other branches or environments (including the split above), and an RCP/SCP-restricted account beyond the region limits described in [runner-minutes.md](runner-minutes.md#real-aws-on-github-hosted-runners).
