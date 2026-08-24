resource "azurerm_resource_group" "platform" {
  name     = "rg-platform-sre"
  location = "westeurope"

  tags = {
    project     = "azure-platform-sre"
    environment = "local"
    managed_by  = "terraform"
  }
}
