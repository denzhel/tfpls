# Policy library

Enable ready-made policies per workspace in `.tfpls.yml`; no Rego needed in your repo:

```yaml
defaults:
  policy_packs: [safety, aws-tags]
stacks:
  - name: terraform
    workspaces_glob: "workspaces/*"
    overrides:
      "prod-*": { policy_packs: [safety, aws-tags, aws-s3, aws-network, aws-iam, cost] }
```

`policy_packs` combines with `policy_dir` (your own rules). Packs are versioned with tfpls, so pinning `@v0` pins the rules. A pack that does not exist stops the run and lists the available ones.

`deny` fails the check (and blocks apply); `warn` is shown in the PR comment and never blocks.

| Pack | deny | warn |
|---|---|---|
| `safety` | destroying or replacing stateful resources (buckets, databases, DynamoDB, KMS keys, EFS/EBS, search domains, Cloudflare zones) | destroying anything else |
| `aws-tags` | | taggable AWS resources created or updated with no tags (`default_tags` count) |
| `aws-s3` | public ACLs; a public access block with any flag turned off | versioning disabled |
| `aws-network` | SSH (22) or RDP (3389) open to `0.0.0.0/0` or `::/0`, inline or standalone rules | |
| `aws-iam` | `Action: *` on `Resource: *`; `iam:*` or `sts:AssumeRole` on every resource; roles assumable by `Principal: *` | |
| `cost` | | 24xlarge and GPU-class instances, volumes over 1000 GiB, Multi-AZ memory-optimised databases |

## Parameters (`policy_params`)

Packs take optional parameters, set next to the pack list and handed to the policies as `data.params.<pack>`:

```yaml
defaults:
  policy_packs: [safety, aws-tags]
  policy_params:
    safety: { max_destroys: 3, protected_types: [aws_sqs_queue] }   # deny plans destroying more than 3 resources; treat queues as stateful
    aws-tags: { required_tags: [team, env] }                          # warn when these tags are missing
```

| Pack | Parameter | Meaning |
|---|---|---|
| `safety` | `max_destroys` | deny a plan that destroys more than N resources (replacements count) |
| `safety` | `protected_types` | extra resource types handled like the built-in stateful ones |
| `aws-tags` | `required_tags` | warn for every listed tag a taggable resource lacks |

Each pack lives in `policies/library/<name>/` and has Rego unit tests next to it (`<pack>_test.rego`, run with `conftest verify -p policies/library/<pack>`) and is tested in `tests/policy-library.test.sh` with a failing and a passing plan, with and without parameters.

To exempt a resource, write your own policy in `policy_dir` instead of enabling the pack, or enable a narrower pack: packs are small files you can copy.
