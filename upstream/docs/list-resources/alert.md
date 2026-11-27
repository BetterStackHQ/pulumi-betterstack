---
page_title: "betterstack_alert List Resource - terraform-provider-betterstack"
subcategory: "Telemetry"
description: |-
  Lists every alert, to import with terraform query.
---

# betterstack_alert (List Resource)

Lists every alert. Run `terraform query` to see them, or `terraform query -generate-config-out=generated.tf` to write a resource block and an `import` block for each. Needs Terraform 1.14 or later. See [Import a dashboard built in the web app](../guides/import-dashboard.md).

## Example Usage

```terraform
# In a .tfquery.hcl file
list "betterstack_alert" "all" {
  provider = betterstack
  config {
  }
}
```

## Schema

### Optional

- `dashboard_id` (String) Use it with `chart_id` to list only the alerts on that dashboard chart.
- `chart_id` (String) Use it with `dashboard_id` to list only the alerts on that dashboard chart.
- `exploration_id` (String) Only the alerts on this exploration.

## Identity

Each result names its object by `id`, the parts of its import id. The generated `import` blocks use them:

```terraform
import {
  to = betterstack_alert.example
  identity = {
    id = "12345"
  }
}
```
