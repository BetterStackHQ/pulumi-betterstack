# Better Stack Pulumi provider

Manage [Better Stack](https://betterstack.com) with Pulumi: uptime monitors, heartbeats, on-call and
escalation, status pages, log and metric sources, collectors, dashboards, error tracking and the rest
of the Better Stack API.

- Docs: [Pulumi Registry](https://www.pulumi.com/registry/packages/betterstack/) and
  [`docs/_index.md`](docs/_index.md)
<!-- generated:packages -->
- Packages: npm [`@better_stack/pulumi`](https://www.npmjs.com/package/@better_stack/pulumi), PyPI [`pulumi_betterstack`](https://pypi.org/project/pulumi-betterstack/), NuGet [`Pulumi.BetterStack`](https://www.nuget.org/packages/Pulumi.BetterStack), Maven Central [`com.betterstack:pulumi`](https://central.sonatype.com/artifact/com.betterstack/pulumi) (package `com.betterstack.pulumi`), Go `github.com/betterstackhq/pulumi-betterstack/sdk/go/betterstack`
<!-- /generated:packages -->

This repository is published from Better Stack's API description, where the provider is built and
tested: each release replaces its contents. The provider bridges the Better Stack Terraform provider
(`upstream/`) with [pulumi-terraform-bridge](https://github.com/pulumi/pulumi-terraform-bridge).
Please report bugs and requests in this repository's issues.

Licensed under Apache-2.0 (`LICENSE`).
