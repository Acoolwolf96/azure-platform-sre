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
