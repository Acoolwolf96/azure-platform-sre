resource "azurerm_resource_group" "platform" {
  name     = "rg-platform-sre"
  location = "northeurope"
}

resource "azurerm_virtual_network" "platform" {
  name                = "vnet-platform-sre"
  address_space       = ["10.50.0.0/16"]
  location            = azurerm_resource_group.platform.location
  resource_group_name = azurerm_resource_group.platform.name

  tags = {
    project     = "azure-platform-sre"
    environment = "local"
    managed_by  = "terraform"
  }
}

resource "azurerm_subnet" "aks" {
  name                 = "snet-aks"
  resource_group_name  = azurerm_resource_group.platform.name
  virtual_network_name = azurerm_virtual_network.platform.name
  address_prefixes     = ["10.50.1.0/24"]
}
