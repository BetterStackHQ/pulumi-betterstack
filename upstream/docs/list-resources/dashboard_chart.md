---
page_title: "betterstack_dashboard_chart List Resource - terraform-provider-betterstack"
subcategory: "Telemetry"
description: |-
  Lists every dashboard chart under the given `dashboard_id`, to import with terraform query.
---

# betterstack_dashboard_chart (List Resource)

Lists every dashboard chart under the given `dashboard_id`. Run `terraform query` to see them, or `terraform query -generate-config-out=generated.tf` to write a resource block and an `import` block for each. Needs Terraform 1.14 or later. See [Import a dashboard built in the web app](../guides/import-dashboard.md).

## Example Usage

```terraform
# In a .tfquery.hcl file
list "betterstack_dashboard_chart" "all" {
  provider = betterstack
  config {
    dashboard_id = "12345"
  }
}
```

## Schema

### Required

- `dashboard_id` (String) ID of the dashboard to list from.

## Identity

Each result names its object by `dashboard_id`, `id`, the parts of its import id. The generated `import` blocks use them:

```terraform
import {
  to = betterstack_dashboard_chart.example
  identity = {
    dashboard_id = "12345"
    id = "12345"
  }
}
```
