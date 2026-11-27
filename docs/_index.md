---
title: Better Stack
meta_desc: Provides an overview of the Better Stack provider for Pulumi.
layout: package
---

The Better Stack provider manages [Better Stack](https://betterstack.com) resources with Pulumi. One provider and one API token cover uptime monitors, heartbeats, on-call calendars and escalation policies, status pages, log and metric sources, collectors, dashboards, error tracking and the rest of the Better Stack API.

## Installation

The provider is available as a package in these languages:

<!-- generated:install -->
* JavaScript/TypeScript: [`@better_stack/pulumi`](https://www.npmjs.com/package/@better_stack/pulumi)
* Python: [`pulumi_betterstack`](https://pypi.org/project/pulumi-betterstack/)
* Go: [`github.com/betterstackhq/pulumi-betterstack/sdk/go/betterstack`](https://pkg.go.dev/github.com/betterstackhq/pulumi-betterstack/sdk/go/betterstack)
* .NET: [`BetterStack.Pulumi`](https://www.nuget.org/packages/BetterStack.Pulumi)
* Java: [`com.betterstack:pulumi`](https://central.sonatype.com/artifact/com.betterstack/pulumi) (package `com.betterstack.pulumi`)
<!-- /generated:install -->

Pulumi downloads the provider plugin from the [GitHub releases](https://github.com/BetterStackHQ/pulumi-betterstack/releases) on the first `pulumi up`.

## Example Usage

{{< chooser language "typescript,python,go,csharp,java,yaml" >}}

{{% choosable language typescript %}}

```typescript
import * as betterstack from "@better_stack/pulumi";

const website = new betterstack.Monitor("website", {
    monitorType: "status",
    url: "https://example.com",
});

export const monitorId = website.id;
```

{{% /choosable %}}

{{% choosable language python %}}

```python
import pulumi
import pulumi_betterstack as betterstack

website = betterstack.Monitor("website",
    monitor_type="status",
    url="https://example.com")

pulumi.export("monitorId", website.id)
```

{{% /choosable %}}

{{% choosable language go %}}

```go
package main

import (
	"github.com/betterstackhq/pulumi-betterstack/sdk/go/betterstack"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		website, err := betterstack.NewMonitor(ctx, "website", &betterstack.MonitorArgs{
			MonitorType: pulumi.String("status"),
			Url:         pulumi.String("https://example.com"),
		})
		if err != nil {
			return err
		}
		ctx.Export("monitorId", website.ID())
		return nil
	})
}
```

{{% /choosable %}}

{{% choosable language csharp %}}

```csharp
using System.Collections.Generic;
using Pulumi;
using BetterStack = Pulumi.BetterStack;

return await Deployment.RunAsync(() =>
{
    var website = new BetterStack.Monitor("website", new()
    {
        MonitorType = "status",
        Url = "https://example.com",
    });

    return new Dictionary<string, object?>
    {
        ["monitorId"] = website.Id,
    };
});
```

{{% /choosable %}}

{{% choosable language java %}}

```java
package myproject;

import com.pulumi.Pulumi;
import com.betterstack.pulumi.Monitor;
import com.betterstack.pulumi.MonitorArgs;

public class App {
    public static void main(String[] args) {
        Pulumi.run(ctx -> {
            var website = new Monitor("website", MonitorArgs.builder()
                .monitorType("status")
                .url("https://example.com")
                .build());

            ctx.export("monitorId", website.id());
        });
    }
}
```

{{% /choosable %}}

{{% choosable language yaml %}}

```yaml
name: betterstack-example
runtime: yaml
resources:
  website:
    type: betterstack:index/monitor:Monitor
    properties:
      monitorType: status
      url: https://example.com
outputs:
  monitorId: ${website.id}
```

{{% /choosable %}}

{{< /chooser >}}

## Configuration

The provider needs a Better Stack API token. Use a global API token from [Better Stack → API tokens → Global tokens](https://betterstack.com/settings/global-api-tokens), or a team API token from [Better Stack → API tokens → Team-based tokens](https://betterstack.com/settings/api-tokens/0). Store it as a secret.

```bash
pulumi config set --secret betterstack:apiToken <your token>
```

Or set the `BETTERSTACK_API_TOKEN` environment variable, which the provider reads when `betterstack:apiToken` isn't set.

<!-- generated:config -->
| Setting | Description |
|---|---|
| `betterstack:apiToken` | Better Stack API token. Falls back to the BETTERSTACK_API_TOKEN environment variable. Secret. |
| `betterstack:teamId` | Selects the team when the API token can reach several. |
| `betterstack:apiRateBurst` | How many requests may go out at once above the rate. Defaults to twice the rate, at least 10, when set to 0. |
| `betterstack:apiRateLimit` | The most requests per second. Defaults to 8, and 0 turns the limit off. |
| `betterstack:apiRetryMax` | How many times a failed request is retried. Defaults to 4. |
| `betterstack:apiRetryWaitMax` | The longest wait before a retry, in seconds, also when Better Stack asks to wait longer. Defaults to 300. |
| `betterstack:apiRetryWaitMin` | The shortest wait before a retry, in seconds. Defaults to 10. |
| `betterstack:apiTimeout` | How long one request may take, in seconds. Defaults to 60, and 0 turns the timeout off. |
<!-- /generated:config -->

## Several teams

The provider's `betterstack:teamId` is the team a resource is created in when the resource doesn't name one, written as the team's ID from Better Stack URLs, such as `t123`. To manage resources in several teams with one API token, set `teamId` on the resources that belong to another team. Changing a resource's `teamId` replaces it, because an object can't move to another team. Set `betterstack:teamId` when the API token can reach several teams, as a global API token can, or set `teamId` on every resource that accepts one.

```typescript
import * as betterstack from "@better_stack/pulumi";

// Created in the team betterstack:teamId selects.
new betterstack.Heartbeat("nightly-backup", {
    name: "Nightly backup",
    period: 86400,
    grace: 3600,
});

new betterstack.Heartbeat("settlement-job", {
    teamId: "t456",
    name: "Settlement job",
    period: 3600,
    grace: 600,
});
```

To give each team its own token or settings, create one explicit provider per team and pass it in each resource's options:

```typescript
import * as pulumi from "@pulumi/pulumi";
import * as betterstack from "@better_stack/pulumi";

const apiToken = new pulumi.Config("betterstack").requireSecret("apiToken");
const production = new betterstack.Provider("production", { apiToken, teamId: "t123" });
const staging = new betterstack.Provider("staging", { apiToken, teamId: "t456" });

new betterstack.Monitor("website", {
    monitorType: "status",
    url: "https://example.com",
}, { provider: production });

new betterstack.Monitor("website-staging", {
    monitorType: "status",
    url: "https://staging.example.com",
}, { provider: staging });
```

An explicit provider doesn't read the stack's `betterstack:` settings, so the example passes the token on. Without `apiToken`, it reads `BETTERSTACK_API_TOKEN`.

## Deletion protection and `protect`

<!-- generated:deletion-protection -->
Applications, collectors, heartbeats, monitors, sources, status pages, team members and teams have a `deletionProtection` input, `true` by default.
<!-- /generated:deletion-protection -->

While it is on, the provider refuses any `pulumi up` or `pulumi destroy` that would delete the object or replace it, including one that changes a create-only input such as a source's `platform`. To delete or replace one, set `deletionProtection: false`, run `pulumi up`, then make the change.

`deletionProtection` exists only in the provider. It's kept in the stack's state, and Better Stack's API never sees it, so it doesn't stop a delete made in the web app or with another tool. Pulumi's own [`protect`](https://www.pulumi.com/docs/iac/concepts/options/protect/) resource option is separate. It makes Pulumi refuse to delete any resource that has it, and is lifted with `pulumi state unprotect`. Use either or both.

To stop managing a protected object without deleting it in Better Stack, set the [`retainOnDelete`](https://www.pulumi.com/docs/iac/concepts/options/retainondelete/) option and remove it from the program, or run `pulumi state delete <urn>`.

## Changes made outside Pulumi

Pulumi doesn't read Better Stack before each update. A change made in the Better Stack web app, or with another tool, shows up only after a refresh:

```bash
pulumi refresh
```

or with `pulumi up --refresh`. Without it, `pulumi preview` compares your program with the last state Pulumi saved, not with what is live.

## Importing existing resources

Every resource can be imported by its Better Stack ID, for example:

```bash
pulumi import betterstack:index/monitor:Monitor website 123456
```

An object that belongs to another one, such as a dashboard chart, is imported by both IDs, like `<dashboard_id>/12345`. Each resource's page lists its import ID.
