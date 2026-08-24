output "resource_group_name" {
  value = azurerm_resource_group.platform.name
}

output "resource_group_id" {
  value = azurerm_resource_group.platform.id
}

output "virtual_network_name" {
  value = azurerm_virtual_network.platform.name
}

output "aks_subnet_id" {
  value = azurerm_subnet.aks.id
}

output "container_registry_name" {
  value = azurerm_container_registry.platform.name
}

output "container_registry_login_server" {
  value = azurerm_container_registry.platform.login_server
}
