variable "postgres_admin_password" {
  type      = string
  sensitive = true
}

resource "terraform_data" "postgres_platform" {
  triggers_replace = [
    azurerm_resource_group.platform.id,
    "pg-platform-sre"
  ]

  provisioner "local-exec" {
    environment = {
      POSTGRES_ADMIN_PASSWORD = var.postgres_admin_password
    }

    command = <<-EOT
      set -e

      PAYLOAD=$(jq -n \
        --arg password "$POSTGRES_ADMIN_PASSWORD" \
        '{
          location: "northeurope",
          sku: {
            name: "Standard_B1ms",
            tier: "Burstable"
          },
          properties: {
            administratorLogin: "platformadmin",
            administratorLoginPassword: $password,
            version: "16",
            storage: {
              storageSizeGB: 32
            }
          }
        }')

      curl -sf -X PUT \
        "http://localhost:4577/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-platform-sre/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg-platform-sre?api-version=2025-08-01" \
        -H "Content-Type: application/json" \
        -d "$PAYLOAD" \
        > /dev/null

      echo "PostgreSQL Flexible Server created."
    EOT
  }

  provisioner "local-exec" {
    when = destroy

    command = <<-EOT
      curl -sf -X DELETE \
        "http://localhost:4577/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-platform-sre/providers/Microsoft.DBforPostgreSQL/flexibleServers/pg-platform-sre?api-version=2025-08-01" \
        || true
    EOT
  }

  depends_on = [
    azurerm_resource_group.platform
  ]
}
