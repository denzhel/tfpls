<p align="center">
  <img src="docs/assets/tfpls-hero.svg" alt="tfpls: a git diff that removes hope and adds a plan someone read. Commit message: grow up. TF, please." width="720">
</p>

**Plan on every pull request. Apply with one comment.** Revolutionary, we know: you look at the plan *before* you apply it. tfpls is OpenTofu on GitHub Actions for AWS and S3 state: reusable workflows that run in your repository, on your Actions minutes. Nothing to host, no server of ours in the path.

> 0.x: young, built in spare time, and the interface can change between minor versions (see the [changelog](CHANGELOG.md)). Try it on a throwaway workspace first. Yes, before prod. Especially before prod.

**[See it](#see-it-in-sixty-seconds)** · **[Quick start](#quick-start)** · **[Is it safe?](#is-it-safe)** · **[Docs](docs/README.md)**

## Why you will like it
- **Your rules, not ours.** Approvals follow your repository's ruleset (CODEOWNERS too, when the ruleset asks for them); tfpls has no approval settings of its own.
- **No config file to start.** One folder per workspace under `workspaces/` needs none; later you write down only the exceptions.
- **The boring parts come along.** Drift issues you can fix by comment, state locks you can see and release, policy checks, an apply queue, an audit log and a dashboard.

## See it in sixty seconds
1. **You open a pull request.** Every workspace you touched gets a plan comment, so nobody can claim they did not know:
   ```markdown
   **Plan:** 1 to add, 1 to change, 0 to destroy.
   <details><summary>Click here to expand</summary> … only what changes, sensitive values hidden … </details>
   `/tofu apply dev` to apply
   ```
2. **A teammate actually reads it.** Then you comment `/tofu apply dev`, and exactly that saved plan is applied. Not a different one. Not "roughly that".
3. **If something is missing, it says no.** Politely, in writing, never a silent skip:
   > ⛔ **apply refused.** `dev`: apply needs 1 approving review(s) of the current commit (3f9a1c2); found 0 …

## Quick start
1. **Onboard** from the root of your repository. It reads what is there, writes `.github/workflows/tofu.yml` and tells you what is left:
   ```sh
   curl -fsSL https://raw.githubusercontent.com/denzhel/tfpls/v0/tools/onboard | bash
   ```
2. **One IAM role** that trusts this repository through GitHub OIDC, the slow part if OIDC is new to you ([credentials](docs/credentials.md)). Then: `gh variable set TFPLS_ROLE_ARN --body arn:aws:iam::<account>:role/<role>`
3. **Encrypt saved plans** (required on public repositories): `openssl rand -base64 32 | gh secret set PLAN_ENCRYPTION_KEY`
4. **Commit and open ONE pull request.** It plans every workspace; merge it (nothing is applied). From the next pull request on, comment `/tofu apply <workspace>` after a review.

## Is it safe?
As safe as your cloud credentials, and tfpls will not pretend otherwise. The full picture: [security model](docs/security-model.md); found a vulnerability? [SECURITY.md](SECURITY.md).
- **The cloud credentials are the only hard boundary.** Plans run the pull request's code, so anyone who can push a branch runs code with the plan role. For a real boundary, give apply its own role ([credentials](docs/credentials.md)).
- **Approvals and saved plans are process controls on top.** An apply needs the approvals your repository asks for, from people with write access; the saved plan is bound to the commit and single-use.
- **The program is verified.** Jobs run one Go binary, `tfpls`; on a release its checksum and build provenance are checked first, and you can check it by hand ([how](docs/security-model.md#the-tfpls-binary-and-the-toolchain)).
- **Public repositories** show comments and logs to everyone; saved plans are stored only encrypted, and fork pull requests are never planned and get no credentials. Never use self-hosted runners there.

## Where to go next
| [Getting started](docs/getting-started.md) | [Reference](docs/reference.md) | [Runbooks](docs/runbooks.md) | [All the docs](docs/README.md) |
|---|---|---|---|
| other layouts, secrets instead of OIDC, self-hosted runners | every setting, input and command | a first run going wrong, stuck locks, failed applies | every feature, the flow, cost, backends, upgrades |

- **Contributing:** pull requests are welcome, yes, even the ones that fix our typos ([CONTRIBUTING.md](CONTRIBUTING.md); sign off under the DCO).
- **Licence:** source-available under [FSL-1.1-ALv2](LICENSE): use, modify and contribute freely, no competing commercial product or service, and each release becomes Apache-2.0 after two years.

<p align="center"><sub>Made with care, a little tofu, and zero tolerance for "it worked on my machine". 🙏</sub></p>
