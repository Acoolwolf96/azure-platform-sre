resource "terraform_data" "keyvault_platform" {
  triggers_replace = [
    azurerm_resource_group.platform.id,
    "kv-platform-sre"
  ]

  provisioner "local-exec" {
    command = <<-EOT
      set -e

      curl -sf -X PUT \
        "http://localhost:4577/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-platform-sre/providers/Microsoft.KeyVault/vaults/kv-platform-sre?api-version=2023-07-01" \
        -H "Content-Type: application/json" \
        -d '{
          "location": "northeurope",
          "properties": {
            "tenantId": "00000000-0000-0000-0000-000000000002",
            "sku": {
              "family": "A",
              "name": "standard"
            },
            "accessPolicies": []
          }
        }' \
        > /dev/null

      echo "Key Vault kv-platform-sre is available."
    EOT
  }

  provisioner "local-exec" {
    when = destroy

    command = <<-EOT
      curl -sf -X DELETE \
        "http://localhost:4577/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-platform-sre/providers/Microsoft.KeyVault/vaults/kv-platform-sre?api-version=2023-07-01" \
        || true
    EOT
  }

  depends_on = [
    azurerm_resource_group.platform
  ]
}
